package query

import "testing"

// TestMatchWeightTiers is the off-machine substitute S-33's own card asks
// for: "validate phi query in a terminal against fzf BEFORE any GUI work."
// This agent cannot run fzf or generate real usage, so these are the
// twenty-queries-the-user-actually-types the card wants, guessed at from
// what a launcher on this project's own machines would plausibly see —
// flagged for cheap veto and extension once the user reports what actually
// ranked wrong (S-33's real VERIFY, on real hardware).
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
// docs/TODO.md's own complaint: "it has latest features appearing first
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
