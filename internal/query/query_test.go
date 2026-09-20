package query

import (
	"context"
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

// TestRunUnlockedBoostsMatchingProvider is the end-to-end version of the
// Requested: "writing 'web <anything>' will automatically set the
// 'search on web' first (but still perform the rest of the ranking)."
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
// "while a prefix word is selected, the only results shown will be
// determined by the prefix."
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
