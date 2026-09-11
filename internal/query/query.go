// Package query is phi's launcher backend (S-33, master plan §7.3, ADR
// 018: ranking, providers and actions live here, the launcher in phi-shell
// is a renderer only — it invokes `phi query` and draws what comes back,
// it never ranks or decides an action for itself).
//
// Cold start is the constraint that shapes everything below (phi/CLAUDE.md:
// "the launcher invokes it on every keystroke... order of milliseconds"):
// `phi query <text>` is a FRESH PROCESS on every keystroke, not a
// persistent server queried repeatedly. Every provider that shells out
// therefore runs concurrently and is bounded by providerTimeout — a slow
// or hung provider degrades that one provider's results, never the whole
// query, and never blocks typing (S-33 AGENT: "results arrive with
// different latencies and must NEVER block typing" — met here by never
// letting one provider hold up the others, not by streaming partial
// results out of one process invocation, which a run-once-and-exit CLI has
// no clean way to do).
//
// Open windows is the one provider that could, in principle, read the
// state phi-shell's own ToplevelManager already holds live — reading it
// through some IPC back into the running shell instance was considered and
// rejected: it would mean `phi query` behaves differently depending on
// whether a shell happens to be running, breaks testability (this
// package's own tests construct providers and call them directly, with no
// shell in the loop), and quietly moves ranking authority into whichever
// side answers first, undermining ADR 018 rather than applying it. The
// windows provider instead shells out to `hyprctl clients -j` itself,
// exactly like every other provider here — phi stays self-contained.
package query

import (
	"context"
	"sync"
	"time"
)

// Result is one candidate the launcher can show and act on. JSON field
// names are lowercase to match this project's own convention elsewhere
// (Bar/modules.json, Panels/tabs.json in phi-shell) — the shell's Launcher
// is this type's one JSON consumer.
type Result struct {
	ID       string  `json:"id"`       // stable across invocations, for frecency — e.g. "app:firefox.desktop"
	Provider string  `json:"provider"` // which Provider produced this, for the shell's grouping/icon choices
	Title    string  `json:"title"`
	Subtitle string  `json:"subtitle"`
	Score    float64 `json:"score"`
	Action   Action  `json:"action"`
	// Rich is an optional structured payload for results a single line
	// cannot express (calculator steps, plots, a converter's alternate
	// units). nil for every other provider; the shell renders it as an
	// expanded card and still honours Action for copy/select.
	Rich *RichResult `json:"rich,omitempty"`
}

// Action is what selecting a Result does. Kind is a closed set the shell
// switches on; Data carries whatever that Kind needs. System-level actions
// (lock, suspend, volume, brightness, screenshot) are deliberately never
// phi verbs (ADR 021's own counter-examples) — ActionSystem hands the
// shell a plain action name and the shell itself performs it.
type Action struct {
	Kind string            `json:"kind"`
	Data map[string]string `json:"data"`
}

// Closed set of Action.Kind values.
const (
	ActionExec           = "exec"           // Data["command"]: run detached, no terminal
	ActionExecTerminal   = "execTerminal"   // Data["command"]: open in a new terminal window
	ActionActivateWindow = "activateWindow" // Data["address"]: focus an existing Hyprland window
	ActionOpenURL        = "openURL"        // Data["url"]
	ActionCopyText       = "copyText"       // Data["text"]: e.g. a calculator result
	ActionSystem         = "system"         // Data["action"]: lock | suspend | logout
	ActionChangeDir      = "changeDir"      // Data["path"]: open a terminal there
	ActionPushView       = "pushView"       // Data["view"]: sub-view navigation (ADR 022)
	// ActionLoading marks a transient result a provider could not answer
	// YET, not one it will never answer (nil/no result stays the signal
	// for that) — added for CurrencyProvider (currency.go's own header has
	// the real bug this closes), generic so any future slow provider can
	// use the same shape instead of inventing another. Not actionable:
	// the shell should not wire selecting one to anything, only re-query
	// the same text shortly to see if a real result has arrived.
	ActionLoading = "loading"
)

// Provider produces candidate Results for a query. q is already trimmed.
// An empty q means nothing has been typed yet; most providers should
// return nothing rather than guess at a default listing — frecency-only
// browsing on an empty query is a real feature but not this step's DONE
// WHEN, which is about ranking real queries correctly.
type Provider interface {
	Name() string
	Query(ctx context.Context, q string) []Result
}

// providerTimeout bounds every external-command provider individually.
// 120ms keeps the whole query well inside the "order of milliseconds"
// budget even if every provider races to the deadline at once — this
// number has no document behind it, chosen as a round, generous bound for
// a local IPC call (hyprctl) or a warm on-disk index (zoxide), flagged for
// retuning once this runs on real hardware.
const providerTimeout = 120 * time.Millisecond

// Providers is the full provider set S-33 wires up. A function, not a
// package-level slice: tests construct their own smaller sets directly
// (see rank_test.go, calculator_test.go) rather than going through this,
// so it has exactly one caller — internal/cli's query verb. phiVerbs is
// PhiCommandProvider's recognised verb set, built by that caller from
// view.Commands (see phicommand.go's own header for why it cannot be built
// here instead) — nil or empty simply turns that provider into a no-op.
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
	}
}

// Run executes every provider concurrently, each bounded by
// providerTimeout, then merges and ranks the combined results. frecency
// may be nil — Rank simply skips the frecency term for every candidate,
// which is what a first-run machine with no history yet should do anyway.
func Run(ctx context.Context, providers []Provider, q string, frecency *Frecency) []Result {
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
