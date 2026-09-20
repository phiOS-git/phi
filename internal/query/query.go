// Package query is phi's launcher backend: ranking, providers, actions.
// Each keystroke spawns fresh concurrent providers bounded by timeout.
package query

import (
	"context"
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
func Run(ctx context.Context, providers []Provider, q string, frecency *Frecency, lockedPrefix string) []Result {
	active := providers
	if names, ok := prefixProviders[lockedPrefix]; ok {
		active = filterProviders(providers, names)
	}

	all := runAndRank(ctx, active, q, frecency)

	if lockedPrefix == "" {
		if key := detectPrefix(q); key != "" {
			all = boostProviders(all, prefixProviders[key])
		}
	}

	return all
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
