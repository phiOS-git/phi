package query

import (
	"context"
	"net/url"
	"strings"
)

// WebSearchProvider is the fallback of last resort: nothing else matched
// well, so offer to search the web for the literal query. No API key, no
// account (S-33 AGENT's own constraint on the currency provider — the same
// bar applies here). DuckDuckGo is this agent's own pick, not a document's:
// no plan file names a search engine, and DuckDuckGo needs no key and
// matches the privacy stance the browser candidates in this project
// §8.6 already lean toward — flagged for cheap veto.
type WebSearchProvider struct{}

func (WebSearchProvider) Name() string { return "websearch" }

func (p WebSearchProvider) Query(_ context.Context, q string) []Result {
	// runner-bar prefix feature: "web <anything>" searches
	// for exactly <anything>, not the literal text "web <anything>" — strip
	// the keyword before it reaches the search URL.
	term := q
	if len(q) >= 4 && strings.EqualFold(q[:4], "web ") {
		term = strings.TrimSpace(q[4:])
	}
	// A single short token is far more likely to be an application or a
	// command than a real web query — require at least two words so this
	// does not crowd out better matches on every keystroke of "fi" while
	// typing "Firefox". The explicit "web " prefix bypasses that guard: it
	// is an unambiguous request to search, even for one word.
	if term == q && len(strings.Fields(q)) < 2 {
		return nil
	}
	if term == "" {
		return nil
	}
	return []Result{{
		ID: "websearch:" + term, Provider: p.Name(),
		Title: "Search the web for \"" + term + "\"", Subtitle: term, Score: 10,
		Action: Action{Kind: ActionOpenURL, Data: map[string]string{"url": webSearchURL(term)}},
	}}
}

func webSearchURL(q string) string {
	return "https://duckduckgo.com/?q=" + url.QueryEscape(q)
}
