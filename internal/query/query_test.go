package query

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectPrefix(t *testing.T) {
	cases := []struct {
		q    string
		want string
	}{
		{"web jeans", "web"},
		{"WEB jeans", "web"}, // case-insensitive
		{"phi theme set dark", "phi"},
		{"wiki linux kernel", "wiki"},
		{"web", ""},          // keyword alone, nothing after it
		{"web ", ""},         // same — Fields collapses the trailing space away
		{"webcam thing", ""}, // "webcam" is not the "web" keyword
		{"firefox", ""},      // not a known keyword at all
		{"", ""},
	}
	for _, c := range cases {
		if got := detectPrefix(c.q); got != c.want {
			t.Errorf("detectPrefix(%q) = %q, want %q", c.q, got, c.want)
		}
	}
}

func TestBoostProvidersMovesMatchingToFront(t *testing.T) {
	ranked := []Result{
		{ID: "a", Provider: "agent"},
		{ID: "w1", Provider: "websearch"},
		{ID: "b", Provider: "agent"},
		{ID: "w2", Provider: "websearch"},
	}
	got := boostProviders(ranked, []string{"websearch"})
	want := []string{"w1", "w2", "a", "b"}
	if len(got) != len(want) {
		t.Fatalf("boostProviders = %v, want %v", got, want)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("boostProviders[%d].ID = %q, want %q (order: %v)", i, got[i].ID, id, got)
		}
	}
}

func TestFilterProviders(t *testing.T) {
	all := []Provider{WebSearchProvider{}, AskAgentProvider{}, CalculatorProvider{}}
	got := filterProviders(all, []string{"websearch"})
	if len(got) != 1 || got[0].Name() != "websearch" {
		t.Fatalf("filterProviders = %v, want only websearch", got)
	}
}

// TestRunUnlockedBoostsMatchingProvider is the end-to-end check that
// typing "web <anything>" automatically moves the web-search result to
// the front while still ranking the rest normally.
// AskAgentProvider's own tier (tierAskAgent, 3000) outranks websearch's
// (tierWebSearch, 2000) — without the boost, websearch would sort BELOW
// the agent result for this exact query.
func TestRunUnlockedBoostsMatchingProvider(t *testing.T) {
	providers := []Provider{WebSearchProvider{}, AskAgentProvider{}}
	got := Run(context.Background(), providers, "web jeans", nil, "")
	if len(got) != 2 {
		t.Fatalf("Run(%q) = %v, want both providers to answer", "web jeans", got)
	}
	if got[0].Provider != "websearch" {
		t.Errorf("Run(%q)[0].Provider = %q, want %q boosted to the front", "web jeans", got[0].Provider, "websearch")
	}
}

// TestRunLockedFiltersToOneProvider is the "Tab locks the prefix" half:
// once a prefix word is locked, only that prefix's own provider results
// are shown.
func TestRunLockedFiltersToOneProvider(t *testing.T) {
	providers := []Provider{WebSearchProvider{}, AskAgentProvider{}}
	got := Run(context.Background(), providers, "web jeans", nil, "web")
	if len(got) != 1 || got[0].Provider != "websearch" {
		t.Fatalf("Run(%q, prefix=web) = %v, want only the websearch result", "web jeans", got)
	}
}

func TestRunUnknownLockedPrefixRunsUnfiltered(t *testing.T) {
	providers := []Provider{WebSearchProvider{}, AskAgentProvider{}}
	got := Run(context.Background(), providers, "web jeans", nil, "not-a-real-prefix")
	if len(got) != 2 {
		t.Fatalf("Run with an unrecognised --prefix = %v, want every provider to still answer", got)
	}
}

func TestRunNoPrefixKeywordLeavesOrderUnchanged(t *testing.T) {
	// A query matching no known prefix keyword should rank exactly as
	// before this feature existed — no reordering applied.
	providers := []Provider{WebSearchProvider{}, AskAgentProvider{}}
	got := Run(context.Background(), providers, "firefox settings", nil, "")
	if len(got) != 2 || got[0].Provider != "agent" {
		t.Fatalf("Run(%q) = %v, want the agent result first, by tier alone", "firefox settings", got)
	}
}

// withFakeApps points XDG_DATA_HOME/XDG_DATA_DIRS at a fresh temp dir
// holding one .desktop file per name, so tests can exercise the
// empty-query "every application" path with more than one entry to
// order.
func withFakeApps(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	appsDir := filepath.Join(dir, "applications")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		fname := strings.ToLower(strings.ReplaceAll(name, " ", "-")) + ".desktop"
		content := "[Desktop Entry]\nType=Application\nName=" + name + "\nExec=" + fname + "\n"
		if err := os.WriteFile(filepath.Join(appsDir, fname), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
}

// TestRunEmptyQueryReturnsAllAppsOrderedByFrecencyThenTitle is the
// end-to-end check that a bare `phi query ""` returns every application —
// nothing else — since all ranking and suggestions come from phi itself,
// with frecency breaking the tie ahead of the plain alphabetical order
// the rest fall back to. The providers argument is passed as nil
// deliberately: this path answers from ApplicationsProvider directly
// (query.go's emptyQueryApps), never consulting whatever provider list
// the caller happened to build.
func TestRunEmptyQueryReturnsAllAppsOrderedByFrecencyThenTitle(t *testing.T) {
	withFakeApps(t, "Zathura", "Firefox", "GIMP")
	f := newTestFrecency(t)
	if err := f.Record("app:gimp.desktop"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got := Run(context.Background(), nil, "", f, "")
	if len(got) != 3 {
		t.Fatalf("Run(\"\") = %v, want all 3 apps", got)
	}
	want := []string{"GIMP", "Firefox", "Zathura"} // GIMP has frecency; the rest fall back to alphabetical
	for i, title := range want {
		if got[i].Title != title {
			t.Fatalf("Run(\"\")[%d].Title = %q, want %q (full order: %v)", i, got[i].Title, title, got)
		}
	}
	for _, r := range got {
		if r.Provider != "application" {
			t.Errorf("Run(\"\") returned a non-application result: %+v, want applications only", r)
		}
	}
}

func TestRemainderEmpty(t *testing.T) {
	cases := []struct {
		prefix, q string
		want      bool
	}{
		{"web", "web", true},
		{"web", "web ", true},
		{"web", "WEB", true}, // case-insensitive
		{"web", "web jeans", false},
		{"web", "", true},
		{"wiki", "wiki   ", true},
		{"web", "webcam", false}, // the whole first field must equal the keyword, not merely start with it
	}
	for _, c := range cases {
		if got := remainderEmpty(c.prefix, c.q); got != c.want {
			t.Errorf("remainderEmpty(%q, %q) = %v, want %v", c.prefix, c.q, got, c.want)
		}
	}
}

// stubTagDefaultsProvider is a minimal TagDefaultsProvider for testing
// lockedTagDefaults's own merge/dedup/cap logic in isolation from any real
// provider's own default-building.
type stubTagDefaultsProvider struct {
	name     string
	defaults []Result
}

func (s stubTagDefaultsProvider) Name() string                               { return s.name }
func (s stubTagDefaultsProvider) Query(_ context.Context, _ string) []Result { return nil }
func (s stubTagDefaultsProvider) TagDefaults(_ context.Context, _ string) []Result {
	return s.defaults
}

// TestLockedTagDefaultsSnapshotsBeforeDefaultsDeduped covers snapshot
// storage and replay under a locked tag: a previously-selected result's
// own stored snapshot must come first, and a default that happens to
// share its ID must not appear a second time with its own (different)
// Title.
func TestLockedTagDefaultsSnapshotsBeforeDefaultsDeduped(t *testing.T) {
	f := newTestFrecency(t)
	snap := Result{ID: "stub:used", Provider: "stub", Title: "Used Before"}
	if err := f.RecordResult("stub:used", &snap); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	providers := []Provider{stubTagDefaultsProvider{
		name: "stub",
		defaults: []Result{
			{ID: "stub:used", Provider: "stub", Title: "duplicate default — must be deduped away"},
			{ID: "stub:new", Provider: "stub", Title: "Brand New"},
		},
	}}

	got := lockedTagDefaults(context.Background(), providers, f, "stub")
	if len(got) != 2 {
		t.Fatalf("lockedTagDefaults = %v, want 2 entries (the snapshot, plus the one genuinely new default)", got)
	}
	if got[0].ID != "stub:used" || got[0].Title != "Used Before" {
		t.Errorf("lockedTagDefaults[0] = %+v, want the snapshot's own Title, not the default's duplicate", got[0])
	}
	if got[1].ID != "stub:new" {
		t.Errorf("lockedTagDefaults[1].ID = %q, want %q", got[1].ID, "stub:new")
	}
}

func TestLockedTagDefaultsCapsAtMax(t *testing.T) {
	var defaults []Result
	for i := 0; i < lockedTagDefaultsMax+10; i++ {
		defaults = append(defaults, Result{ID: fmt.Sprintf("stub:%d", i), Provider: "stub", Title: fmt.Sprintf("Item %d", i)})
	}
	providers := []Provider{stubTagDefaultsProvider{name: "stub", defaults: defaults}}

	got := lockedTagDefaults(context.Background(), providers, nil, "stub")
	if len(got) != lockedTagDefaultsMax {
		t.Fatalf("lockedTagDefaults returned %d entries, want the cap of %d", len(got), lockedTagDefaultsMax)
	}
}

// TestSnapshotsForKeywordFiltersSiteHost checks that SiteSearchProvider's
// four keywords ("wiki", "yt", "arch", "rddt"), all answered under one
// Provider name ("sitesearch"), don't cross-contaminate: filtering a
// snapshot by Provider name alone would replay a YouTube snapshot under
// the "wiki" tag. Only a snapshot whose own stored URL host matches the
// locked keyword's site must survive.
func TestSnapshotsForKeywordFiltersSiteHost(t *testing.T) {
	f := newTestFrecency(t)
	wikiSnap := Result{
		ID: "sitesearch:wiki:https://en.wikipedia.org/wiki/Go", Provider: "sitesearch", Title: "Go (wiki)",
		Action: Action{Kind: ActionOpenURL, Data: map[string]string{"url": "https://en.wikipedia.org/wiki/Go"}},
	}
	ytSnap := Result{
		ID: "sitesearch:yt:https://www.youtube.com/watch?v=x", Provider: "sitesearch", Title: "Some video",
		Action: Action{Kind: ActionOpenURL, Data: map[string]string{"url": "https://www.youtube.com/watch?v=x"}},
	}
	if err := f.RecordResult(wikiSnap.ID, &wikiSnap); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	if err := f.RecordResult(ytSnap.ID, &ytSnap); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}

	got := snapshotsForKeyword(f, map[string]bool{"sitesearch": true}, "wiki")
	if len(got) != 1 || got[0].ID != wikiSnap.ID {
		t.Fatalf("snapshotsForKeyword(..., \"wiki\") = %v, want only the Wikipedia snapshot", got)
	}
}

// fakeProvider is a minimal Provider that always returns the same fixed
// results regardless of q, for tests that need full control over which
// providers "matched" independent of any real provider's own query
// parsing.
type fakeProvider struct {
	name    string
	results []Result
}

func (f fakeProvider) Name() string                               { return f.name }
func (f fakeProvider) Query(_ context.Context, _ string) []Result { return f.results }

// TestRunBoostsMathOnConversionQuery covers looksLikeMath's own path: a
// query that plainly asks for a unit conversion must rank the calculator
// result first even though an application also matched (and normally
// outranks it — tierApps sits well above tierMath, rank.go).
func TestRunBoostsMathOnConversionQuery(t *testing.T) {
	providers := []Provider{
		fakeProvider{name: "application", results: []Result{
			{ID: "app:meters", Provider: "application", Title: "Meters Converter", Score: 500},
		}},
		CalculatorProvider{},
	}
	q := "100 km to m"
	got := Run(context.Background(), providers, q, nil, "")
	if len(got) == 0 || got[0].Provider != "calculator" {
		t.Fatalf("Run(%q) = %v, want the calculator conversion boosted to the front", q, got)
	}
}

// TestRunBoostsMathWhenOnlyFallbacksMatch covers onlyFallbackMatches: a
// query that does not itself look like math (looksLikeMath is false) must
// still boost a calculator/currency result to the front when everything
// else in the result set is one of the always-present
// websearch/sitesearch/agent fallbacks, since none of those is a real
// match either.
func TestRunBoostsMathWhenOnlyFallbacksMatch(t *testing.T) {
	q := "banana split"
	if looksLikeMath(q) {
		t.Fatalf("test setup: %q must not itself look like math, to isolate the only-fallback-matches path", q)
	}
	providers := []Provider{
		fakeProvider{name: "websearch", results: []Result{{ID: "w1", Provider: "websearch", Title: "Search the web", Score: 10}}},
		fakeProvider{name: "agent", results: []Result{{ID: "a1", Provider: "agent", Title: "Ask AI", Score: 10}}},
		fakeProvider{name: "calculator", results: []Result{{ID: "calc1", Provider: "calculator", Title: "42", Score: 100}}},
	}
	got := Run(context.Background(), providers, q, nil, "")
	if len(got) == 0 || got[0].Provider != "calculator" {
		t.Fatalf("Run(%q) = %v, want the calculator result boosted to the front since only fallbacks otherwise matched", q, got)
	}
}

// TestRunKeepsApplicationFirstForGameLikeQuery guards against the math
// boost overreaching: "2048 game" contains a digit but plainly means an
// application, not arithmetic (looksLikeMath must stay false, see
// TestLooksLikeMath), and a real application match means the
// only-fallback-matches condition does not hold either, so the
// application must keep its normal tier-order lead.
func TestRunKeepsApplicationFirstForGameLikeQuery(t *testing.T) {
	q := "2048 game"
	if looksLikeMath(q) {
		t.Fatalf("test setup: %q must not look like math", q)
	}
	providers := []Provider{
		fakeProvider{name: "application", results: []Result{{ID: "app:2048", Provider: "application", Title: "2048", Score: 500}}},
		fakeProvider{name: "calculator", results: []Result{{ID: "calc1", Provider: "calculator", Title: "2048", Score: 100}}},
	}
	got := Run(context.Background(), providers, q, nil, "")
	if len(got) == 0 || got[0].Provider != "application" {
		t.Fatalf("Run(%q) = %v, want the application result to stay first, not displaced by a math boost", q, got)
	}
}

// TestRunMathBoostDoesNotOverrideADifferentExplicitPrefix guards
// prefixRoutesToMath: "phi" is itself a known constant to
// CalculatorProvider (the golden ratio), so a query like "phi x=2" can
// parse as a real equation even though the user's "phi" is the runner-bar
// prefix keyword, not the constant. The prefix's own boost (routing to
// PhiCommandProvider) must win; the math boost must not override an
// explicit different-prefix routing decision just because the raw text
// happens to also parse as math.
func TestRunMathBoostDoesNotOverrideADifferentExplicitPrefix(t *testing.T) {
	q := "phi x=2"
	if !looksLikeMath(q) {
		t.Fatalf("test setup: %q must look like math, to actually exercise the guard", q)
	}
	if key := detectPrefix(q); key != "phi" {
		t.Fatalf("test setup: detectPrefix(%q) = %q, want \"phi\"", q, key)
	}
	providers := []Provider{
		fakeProvider{name: "phi", results: []Result{{ID: "phi:x", Provider: "phi", Title: "phi x", Score: 500}}},
		fakeProvider{name: "calculator", results: []Result{{ID: "calc1", Provider: "calculator", Title: "x = 1.236", Score: 100}}},
	}
	got := Run(context.Background(), providers, q, nil, "")
	if len(got) == 0 || got[0].Provider != "phi" {
		t.Fatalf("Run(%q) = %v, want the explicit \"phi\" prefix boost to win over the coincidental math parse", q, got)
	}
}

// TestLooksLikeMath covers the conservative digit-plus-operator/conversion
// rule directly: a bare digit is never enough on its own ("7zip", "2048
// game", "mp3" all have one but mean an application/format), it also
// needs an arithmetic character or to parse as a real conversion.
func TestLooksLikeMath(t *testing.T) {
	cases := map[string]bool{
		"7zip":              false,
		"2048 game":         false,
		"mp3":               false,
		"firefox":           false,
		"gimp2":             false,
		"":                  false,
		"km to m":           false, // a real conversion shape, but no digit at all
		"2 + 2":             true,
		"10% of 50":         true,
		"(1+2)*3":           true,
		"solve x^2 - 4 = 0": true,
		"100 km to m":       true,
		"100 usd to eur":    true,
	}
	for q, want := range cases {
		if got := looksLikeMath(q); got != want {
			t.Errorf("looksLikeMath(%q) = %v, want %v", q, got, want)
		}
	}
}
