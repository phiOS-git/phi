package query

import (
	"context"
	"testing"
)

func TestWebSearchTwoWordFallback(t *testing.T) {
	r := WebSearchProvider{}.Query(context.Background(), "firefox settings")
	if len(r) != 1 || r[0].Subtitle != "firefox settings" {
		t.Fatalf("Query(%q) = %v", "firefox settings", r)
	}
}

func TestWebSearchSingleWordYieldsNothing(t *testing.T) {
	if r := (WebSearchProvider{}).Query(context.Background(), "firefox"); r != nil {
		t.Errorf("Query(%q) = %v, want nil — a single word is more likely an app or command", "firefox", r)
	}
}

// runner-bar prefix feature: "web <anything>" searches
// exactly <anything>, not the literal text "web <anything>".
func TestWebSearchPrefixStripsKeyword(t *testing.T) {
	r := WebSearchProvider{}.Query(context.Background(), "web jeans")
	if len(r) != 1 || r[0].Subtitle != "jeans" {
		t.Fatalf("Query(%q) = %v, want the search term to be %q", "web jeans", r, "jeans")
	}
	if r[0].Action.Data["url"] != webSearchURL("jeans") {
		t.Errorf("url = %q, want a search for %q, not the literal prefixed text", r[0].Action.Data["url"], "jeans")
	}
}

func TestWebSearchPrefixBypassesSingleWordGuard(t *testing.T) {
	// "web jeans" is two words overall, but the explicit keyword should
	// also work for what becomes a single-word search term.
	r := WebSearchProvider{}.Query(context.Background(), "web arch")
	if len(r) != 1 || r[0].Subtitle != "arch" {
		t.Fatalf("Query(%q) = %v, want a single-word search for %q via the explicit prefix", "web arch", r, "arch")
	}
}

func TestWebSearchPrefixCaseInsensitive(t *testing.T) {
	r := WebSearchProvider{}.Query(context.Background(), "WEB jeans")
	if len(r) != 1 || r[0].Subtitle != "jeans" {
		t.Fatalf("Query(%q) = %v, want %q stripped case-insensitively", "WEB jeans", r, "jeans")
	}
}

func TestWebSearchWordStartingWithWebNotMisread(t *testing.T) {
	// "webcam settings" must not be read as prefix "web" + "cam settings".
	r := WebSearchProvider{}.Query(context.Background(), "webcam settings")
	if len(r) != 1 || r[0].Subtitle != "webcam settings" {
		t.Fatalf("Query(%q) = %v, want the full literal text, not a false prefix strip", "webcam settings", r)
	}
}
