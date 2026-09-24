package query

import "testing"

// TestMatchWeightTiers validates ranking with fixed cases, since this agent
// cannot generate real usage. These are representative queries that the
// launcher would plausibly see, flagged for extension once the user reports
// what actually ranked wrong.
func TestMatchWeightTiers(t *testing.T) {
	cases := []struct {
		title, q string
		want     float64
	}{
		{"Firefox", "Firefox", 100},
		{"Firefox", "firefox", 100}, // case-insensitive
		{"Firefox", "fire", 80},
		{"Kitty Terminal", "term", 60},   // word-prefix, not string-prefix
		{"Kitty Terminal", "y term", 40}, // substring, not a prefix or word-prefix
		{"Firefox", "fx", 20},            // subsequence only
		{"Firefox", "xyz", 0},
		{"", "anything", 0},
		{"Anything", "", 0}, // empty query matches nothing — see Rank's own comment
	}
	for _, c := range cases {
		got := matchWeight(c.title, c.q)
		if got != c.want {
			t.Errorf("matchWeight(%q, %q) = %v, want %v", c.title, c.q, got, c.want)
		}
	}
}

func TestRankExcludesNonMatches(t *testing.T) {
	results := []Result{
		{ID: "a", Title: "Firefox"},
		{ID: "b", Title: "Zathura"},
	}
	ranked := Rank(results, "fire", nil)
	if len(ranked) != 1 || ranked[0].ID != "a" {
		t.Fatalf("Rank(%v, %q) = %v, want only Firefox", results, "fire", ranked)
	}
}

func TestRankOrdersByScoreThenShorterTitle(t *testing.T) {
	results := []Result{
		{ID: "long", Title: "Firefox Developer Edition"},
		{ID: "short", Title: "Firefox"},
	}
	ranked := Rank(results, "firefox", nil)
	if len(ranked) != 2 || ranked[0].ID != "short" {
		t.Fatalf("Rank tie-break by title length failed: got %v", ranked)
	}
}

func TestRankFrecencyBreaksATie(t *testing.T) {
	f := newTestFrecency(t)
	f.Record("used")
	f.Record("used")
	f.Record("used")

	results := []Result{
		{ID: "unused", Title: "Terminal"},
		{ID: "used", Title: "Terminal"},
	}
	ranked := Rank(results, "term", f)
	if len(ranked) != 2 || ranked[0].ID != "used" {
		t.Fatalf("Rank did not let frecency break an equal-title tie: got %v", ranked)
	}
}

// TestRankTrustsExplicitProviderScore covers CalculatorProvider's own
// contract: its Title is the numeric answer, which will almost never
// match the query text itself (matchWeight("4", "2+2") is 0), so a
// provider that already knows its own confidence must be trusted, not
// re-scored against a title that was never meant to be compared to q. The
// provider's own tier (tierMath) still lands on top of that trusted score.
func TestRankTrustsExplicitProviderScore(t *testing.T) {
	results := []Result{
		{ID: "calc", Provider: "calculator", Title: "4", Score: 100},
	}
	ranked := Rank(results, "2+2", nil)
	if len(ranked) != 1 || ranked[0].Score != 100+tierMath {
		t.Fatalf("Rank did not trust an explicit provider score: got %v", ranked)
	}
}

// TestRankOrdersByCategoryBeforeMatchQuality is the regression test for
// complaint: "it has latest features appearing first
// (like the calculator) but it does not make sense. Apps should be always
// first, non hidden files second, math when obvious." A whole category
// must outrank the next one even when its own match within that category
// is the weakest possible, as long as it matched at all.
func TestRankOrdersByCategoryBeforeMatchQuality(t *testing.T) {
	results := []Result{
		{ID: "calc", Provider: "calculator", Title: "4", Score: 100},        // math: top confidence
		{ID: "file", Provider: "file", Title: "firefox.desktop", Score: 30}, // file: flat score
		{ID: "app", Provider: "application", Title: "Firefox"},              // app: weakest match tier
	}
	// "fx" is only a subsequence of "Firefox" (matchWeight tier 20, the
	// weakest non-zero tier — see TestMatchWeightTiers).
	ranked := Rank(results, "fx", nil)
	if len(ranked) != 3 {
		t.Fatalf("Rank dropped a result: got %v", ranked)
	}
	want := []string{"app", "file", "calc"}
	for i, id := range want {
		if ranked[i].ID != id {
			t.Fatalf("Rank did not order by category tier: got %v, want order %v", ranked, want)
		}
	}
}

// TestRankAppliesFullCategoryOrder covers full category
// list end to end: apps, HOME files, commands, phi commands, ask ai agent,
// search web, math, conversion (search any file is skipped — no such
// provider exists yet, see rank.go's own comment). Each Result's Title is
// deliberately a poor match for q so category alone, not match quality,
// has to carry the order.
func TestRankAppliesFullCategoryOrder(t *testing.T) {
	results := []Result{
		{ID: "currency", Provider: "currency", Title: "9.14 zz-coin", Score: 100},
		{ID: "calc", Provider: "calculator", Title: "zz42", Score: 100},
		{ID: "websearch", Provider: "websearch", Title: "Search the web for \"zz\"", Score: 10},
		{ID: "agent", Provider: "agent", Title: "Ask AI: \"zz\"", Score: 10},
		{ID: "other", Provider: "ssh", Title: "some-zz-host"},
		{ID: "phi", Provider: "phi", Title: "phi zz-verb"},
		{ID: "command", Provider: "command", Title: "Run: zz"},
		{ID: "file", Provider: "file", Title: "some-zz.txt"},
		{ID: "app", Provider: "application", Title: "The zz App"},
	}
	ranked := Rank(results, "zz", nil)
	want := []string{"app", "file", "command", "phi", "other", "agent", "websearch", "calc", "currency"}
	if len(ranked) != len(want) {
		t.Fatalf("Rank dropped results: got %v, want %d entries", ranked, len(want))
	}
	for i, id := range want {
		if ranked[i].ID != id {
			t.Fatalf("Rank did not apply the full category order: got %v, want order %v", ranked, want)
		}
	}
}

func TestAskAgentProviderRequiresTwoWords(t *testing.T) {
	p := AskAgentProvider{}
	if got := p.Query(nil, "single"); got != nil {
		t.Fatalf("AskAgentProvider.Query(%q) = %v, want nil for a single word", "single", got)
	}
	got := p.Query(nil, "what time is it")
	if len(got) != 1 {
		t.Fatalf("AskAgentProvider.Query(%q) = %v, want one result", "what time is it", got)
	}
	r := got[0]
	if r.Provider != "agent" || r.Action.Kind != ActionExecTerminal {
		t.Fatalf("AskAgentProvider result malformed: %+v", r)
	}
	if r.Action.Data["command"] != "phi agent ask 'what time is it'" {
		t.Fatalf("AskAgentProvider command = %q, want the query shell-quoted", r.Action.Data["command"])
	}
}

// TestRankSkipsFrecencyForClipboard guards ClipboardProvider's own
// pinned-first, newest-first order (clipboard.go): adding selection
// frecency on top, the way every other provider gets, would let an old
// but frequently-copied entry outrank one just copied, the opposite of
// what "newest first" means for a clipboard history.
func TestRankSkipsFrecencyForClipboard(t *testing.T) {
	f := newTestFrecency(t)
	for i := 0; i < 20; i++ {
		f.Record("clip:old")
	}

	results := []Result{
		{ID: "clip:old", Provider: "clipboard", Title: "old but frequently copied", Score: 10},
		{ID: "clip:new", Provider: "clipboard", Title: "just copied", Score: 20},
	}
	ranked := Rank(results, "", f)
	if len(ranked) != 2 || ranked[0].ID != "clip:new" {
		t.Fatalf("Rank did not respect ClipboardProvider's own explicit Score order: got %v", ranked)
	}
	if ranked[0].Score != 20+tierOtherAction || ranked[1].Score != 10+tierOtherAction {
		t.Errorf("Rank added frecency on top of a clipboard result's own Score: got %v", ranked)
	}
}

func TestIsSubsequence(t *testing.T) {
	if !isSubsequence("firefox", "fx") {
		t.Error("expected fx to be a subsequence of firefox")
	}
	if isSubsequence("firefox", "xf") {
		t.Error("xf is not a subsequence of firefox (wrong order)")
	}
	if isSubsequence("firefox", "") {
		t.Error("empty needle should not match as a subsequence")
	}
}
