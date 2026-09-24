package query

import (
	"context"
	"net/url"
	"strings"
)

// WebSearchProvider is the fallback of last resort: nothing else matched
// well, so offer to search the web. No API key, no account required.
// DuckDuckGo is chosen for needing no key and matching the privacy stance
// already established in this project.
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

// TagDefaults returns the "web" tag's default list: the user's own
// LibreWolf bookmarks, top history by frecency, and recent search-bar
// terms — "common usage" for the "web" tag locked with nothing typed yet,
// never content this provider invents. Every missing piece (no LibreWolf
// profile, no sqlite3, a table that doesn't exist) degrades to
// contributing nothing, the same graceful-degradation contract every
// other provider gets from runCommand.
func (p WebSearchProvider) TagDefaults(ctx context.Context, _ string) []Result {
	var out []Result
	if rows, ok := queryLibreWolfDB(ctx, librewolfPlacesDB, librewolfBookmarksSQL("")); ok {
		out = append(out, librewolfLinkResults(p.Name(), "websearch:", rows)...)
	}
	if rows, ok := queryLibreWolfDB(ctx, librewolfPlacesDB, librewolfHistorySQL("")); ok {
		out = append(out, librewolfLinkResults(p.Name(), "websearch:", rows)...)
	}
	if rows, ok := queryLibreWolfDB(ctx, librewolfFormHistoryDB, librewolfSearchTermsSQL()); ok {
		out = append(out, librewolfSearchTermResults(p.Name(), rows)...)
	}
	return out
}
