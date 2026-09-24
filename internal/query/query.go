// Package query is phi's launcher backend: ranking, providers, actions.
// Each keystroke spawns fresh concurrent providers bounded by timeout.
package query

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Result is a launcher candidate.
type Result struct {
	ID       string  `json:"id"`       // stable across invocations, for frecency — e.g. "app:firefox.desktop"
	Provider string  `json:"provider"` // which Provider produced this, for the shell's grouping/icon choices
	Title    string  `json:"title"`
	Subtitle string  `json:"subtitle"`
	Score    float64 `json:"score"`
	Action   Action  `json:"action"`
	// Rich is optional structured data (calculator steps, plots, alternate units).
	Rich *RichResult `json:"rich,omitempty"`
}

// Action is what selecting a Result does; Kind is closed set, Data
// carries Kind-specific details.
type Action struct {
	Kind string            `json:"kind"`
	Data map[string]string `json:"data"`
}

// Action.Kind is a closed set of action types.
const (
	ActionExec           = "exec"           // Data["command"]: run detached, no terminal
	ActionExecTerminal   = "execTerminal"   // Data["command"]: open in a new terminal window
	ActionActivateWindow = "activateWindow" // Data["address"]: focus an existing Hyprland window
	ActionOpenURL        = "openURL"        // Data["url"]
	ActionCopyText       = "copyText"       // Data["text"]: e.g. a calculator result
	ActionSystem         = "system"         // Data["action"]: lock | suspend | hibernate | logout | reboot | shutdown
	ActionChangeDir      = "changeDir"      // Data["path"]: open a terminal there
	ActionPushView       = "pushView"       // Data["view"]: sub-view navigation
	// ActionLoading marks a transient result a provider could not answer yet,
	// distinct from a result it will never provide (nil). Useful for slow
	// providers like currency conversion. Not actionable: the shell should
	// only re-query to see if a real result has arrived.
	ActionLoading = "loading"
)

// Provider produces candidate Results for query q (trimmed,
// possibly empty).
type Provider interface {
	Name() string
	Query(ctx context.Context, q string) []Result
}

// TagDefaultsProvider is implemented by a provider that has real "common
// usage" suggestions for its own runner-bar prefix locked with nothing
// typed yet — a bare Query("") cannot serve this, since for most
// providers an empty *unlocked* query must stay silent (Providers()'s
// empty-query contract), while a *locked* tag with an empty remainder
// wants a real default list. keyword is the locked prefix itself (as
// looked up in prefixProviders): most implementations ignore it, but a
// provider registered under more than one keyword — SiteSearchProvider,
// under "wiki"/"yt"/"arch"/"rddt" — needs it to know which one. A
// provider with nothing better than plain history simply does not
// implement this interface; lockedTagDefaults treats that exactly like an
// empty return.
type TagDefaultsProvider interface {
	TagDefaults(ctx context.Context, keyword string) []Result
}

// providerTimeout: 120ms per provider to keep queries under the
// "order of milliseconds" launcher requirement.
const providerTimeout = 120 * time.Millisecond

// Providers returns the full provider set (phiVerbs configures
// PhiCommandProvider).
func Providers(frecency *Frecency, phiVerbs map[string]bool) []Provider {
	return []Provider{
		CalculatorProvider{},
		CurrencyProvider{},
		ApplicationsProvider{},
		WindowsProvider{},
		PhiCommandProvider{Verbs: phiVerbs},
		CommandProvider{},
		ZoxideProvider{},
		SSHHostsProvider{},
		WebSearchProvider{},
		FilesProvider{},
		SystemActionsProvider{},
		AskAgentProvider{},
		TimerProvider{},
		StopwatchProvider{},
		SiteSearchProvider{},
		ClipboardProvider{},
	}
}

// prefixProviders maps a runner-bar prefix keyword to the provider Name()(s)
// it routes to. This map only decides routing; actual keyword parsing stays
// inside each provider. phicommand.go, websearch.go, command.go and
// calculator.go each strip their own leading keyword, and sitesearch.go
// matches its keywords itself. "convert" routes to "calculator", which
// already has its own keyword-stripping via mathx.ParseConversion.
var prefixProviders = map[string][]string{
	"web":     {"websearch"},
	"convert": {"calculator"},
	"math":    {"calculator"},
	"ask":     {"agent"},
	"file":    {"file"},
	"app":     {"application"},
	"run":     {"command"},
	"phi":     {"phi"},
	"wiki":    {"sitesearch"},
	"yt":      {"sitesearch"},
	"arch":    {"sitesearch"},
	"rddt":    {"sitesearch"},
	"copy":    {"clipboard"},
	"clip":    {"clipboard"},
	"cp":      {"clipboard"},
}

// detectPrefix reports the prefix keyword leading q (only if 2+ words).
func detectPrefix(q string) string {
	fields := strings.Fields(q)
	if len(fields) < 2 {
		return ""
	}
	if key := strings.ToLower(fields[0]); prefixProviders[key] != nil {
		return key
	}
	return ""
}

// Run executes providers concurrently (each bounded by providerTimeout),
// then merges and ranks results. lockedPrefix filters to specific
// providers (empty runs all).
//
// Two cases never reach the normal per-provider Rank pass at all, both
// because there is no query text to rank against and Rank's own
// text-match filter (rank.go's matchWeight) would either drop everything
// or fall back to an arbitrary tie-break that is the wrong shape here:
//
//   - No locked tag and nothing typed: phi query is the shell's own
//     browse-everything list, not merely "no results yet."
//     emptyQueryApps answers this directly rather than depending on
//     every other provider separately choosing to stay silent for q == "".
//   - A locked tag whose remainder (the text after its own keyword) is
//     empty: lockedTagDefaults answers with frecency history first, then
//     each provider's own notion of a sensible default.
func Run(ctx context.Context, providers []Provider, q string, frecency *Frecency, lockedPrefix string) []Result {
	names, locked := prefixProviders[lockedPrefix]
	active := providers
	if locked {
		active = filterProviders(providers, names)
		if remainderEmpty(lockedPrefix, q) {
			return lockedTagDefaults(ctx, active, frecency, lockedPrefix)
		}
	} else if strings.TrimSpace(q) == "" {
		return emptyQueryApps(ctx, frecency)
	}

	all := runAndRank(ctx, active, q, frecency)

	if lockedPrefix == "" {
		if key := detectPrefix(q); key != "" {
			all = boostProviders(all, prefixProviders[key])
		}
	}

	return all
}

// remainderEmpty reports whether q, once its locked keyword is accounted
// for, has nothing left to rank against: nothing typed at all, or exactly
// the keyword itself (optionally with trailing whitespace, which
// strings.Fields already collapses away) — the runner bar's "tag just
// locked, nothing typed yet" state.
func remainderEmpty(lockedPrefix, q string) bool {
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return true
	}
	return len(fields) == 1 && strings.EqualFold(fields[0], lockedPrefix)
}

// emptyQueryApps handles a bare `phi query ""` (no locked tag): it
// returns every application, since phi is the single source of ranking
// and suggestions, replacing the shell's own separate browse list rather
// than adding to it. Every other provider contributes nothing here by
// construction (only ApplicationsProvider is ever asked), rather than by
// trusting that each of them separately happens to guard its own q == ""
// case.
func emptyQueryApps(ctx context.Context, frecency *Frecency) []Result {
	pctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	apps := ApplicationsProvider{}.Query(pctx, "")
	sortByFrecencyThenTitle(apps, frecency)
	return apps
}

// sortByFrecencyThenTitle orders results by frecency.Score descending,
// then title (case-insensitive), then ID — deterministic regardless of a
// provider's own scan order, and independent of Rank's own tie-break
// (shorter title first, rank.go), which is the wrong shape for a plain
// browse or defaults list. frecency may be nil (pure alphabetical order).
// It also overwrites each Result's Score with a value that strictly
// decreases down the slice, so the JSON payload's own SCORE field (which
// the shell may itself sort or display by) agrees with the order below
// rather than whatever a provider set internally (commonly 0, letting
// Rank decide, which does not apply on this path).
func sortByFrecencyThenTitle(results []Result, frecency *Frecency) {
	sort.SliceStable(results, func(i, j int) bool {
		si, sj := frecency.Score(results[i].ID), frecency.Score(results[j].ID)
		if si != sj {
			return si > sj
		}
		ti, tj := strings.ToLower(results[i].Title), strings.ToLower(results[j].Title)
		if ti != tj {
			return ti < tj
		}
		return results[i].ID < results[j].ID
	})
	for i := range results {
		results[i].Score = float64(len(results) - i)
	}
}

// lockedTagDefaultsMax caps the merged snapshots+defaults list at a
// sensible number for a runner-bar dropdown. Deliberately applied to
// every locked tag uniformly, "app" included — ApplicationsProvider
// contributing every application to that merge is that provider's own
// share of the list, not an exemption from the cap placed on the whole
// locked-tag-defaults list; the truly uncapped full app list stays the
// no-tag case (emptyQueryApps).
const lockedTagDefaultsMax = 40

// lockedTagDefaults shows "common usage" for a tag just locked with
// nothing typed yet — the user's own frecency history for these
// providers first, ranked by frecency score (snapshotsForKeyword), then
// each provider's own TagDefaults (recent files, every phi verb, browser
// history — see each provider's own implementation), in that provider's
// own order, deduplicated by ID and capped. A provider with no
// TagDefaults contributes nothing here: agent/calculator/command show
// history only, never an invented default.
func lockedTagDefaults(ctx context.Context, providers []Provider, frecency *Frecency, lockedPrefix string) []Result {
	names := make(map[string]bool, len(providers))
	for _, p := range providers {
		names[p.Name()] = true
	}

	seen := map[string]bool{}
	var out []Result
	add := func(results []Result) {
		for _, r := range results {
			if seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			out = append(out, r)
		}
	}

	add(snapshotsForKeyword(frecency, names, lockedPrefix))

	for _, p := range providers {
		dp, ok := p.(TagDefaultsProvider)
		if !ok {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, providerTimeout)
		results := dp.TagDefaults(pctx, lockedPrefix)
		cancel()
		add(results)
	}

	if len(out) > lockedTagDefaultsMax {
		out = out[:lockedTagDefaultsMax]
	}
	for i := range out {
		out[i].Score = float64(len(out) - i)
	}
	return out
}

// snapshotsForKeyword is frecency.Snapshots filtered to the providers
// active for lockedPrefix, with one extra narrowing: SiteSearchProvider
// answers four different keywords ("wiki"/"yt"/"arch"/"rddt") under the
// single provider name "sitesearch", so a plain Provider-name match would
// replay a stored YouTube result under the Wikipedia tag. When
// lockedPrefix is one of those site keywords, a "sitesearch" snapshot is
// kept only if its stored URL's host matches that site's own host
// (sitesearch.go's siteSearches table) — the same host this same keyword's
// own TagDefaults filters its LibreWolf query by.
func snapshotsForKeyword(frecency *Frecency, providerNames map[string]bool, lockedPrefix string) []Result {
	snapshots := frecency.Snapshots(providerNames)
	site, isSite := siteSearchByKey(lockedPrefix)
	if !isSite {
		return snapshots
	}
	var out []Result
	for _, r := range snapshots {
		if r.Provider != "sitesearch" {
			out = append(out, r)
			continue
		}
		u, err := url.Parse(r.Action.Data["url"])
		if err != nil || !strings.EqualFold(u.Hostname(), site.host) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func runAndRank(ctx context.Context, providers []Provider, q string, frecency *Frecency) []Result {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var all []Result

	for _, p := range providers {
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, providerTimeout)
			defer cancel()
			results := p.Query(pctx, q)
			mu.Lock()
			all = append(all, results...)
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	return Rank(all, q, frecency)
}

func filterProviders(providers []Provider, names []string) []Provider {
	keep := make(map[string]bool, len(names))
	for _, n := range names {
		keep[n] = true
	}
	out := make([]Provider, 0, len(names))
	for _, p := range providers {
		if keep[p.Name()] {
			out = append(out, p)
		}
	}
	return out
}

// boostProviders moves every result from the named provider(s) to the
// front of an already-ranked list, preserving relative order within each
// partition — "automatically set it first, but still perform the rest of
// the ranking": everything else keeps its normal
// tier/score order, only the matched category moves up.
func boostProviders(ranked []Result, names []string) []Result {
	keep := make(map[string]bool, len(names))
	for _, n := range names {
		keep[n] = true
	}
	boosted := make([]Result, 0, len(ranked))
	rest := make([]Result, 0, len(ranked))
	for _, r := range ranked {
		if keep[r.Provider] {
			boosted = append(boosted, r)
		} else {
			rest = append(rest, r)
		}
	}
	return append(boosted, rest...)
}
