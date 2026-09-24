package query

import (
	"context"
	"net/url"
	"regexp"
	"strings"
)

// SiteSearchProvider implements the runner-bar prefix feature's named
// per-site shortcuts: one keyword per site (wiki/yt/arch/rddt), each
// opening that site's own search-results page for the rest of the
// query. No API key or account for any of the four — the same constraint
// WebSearchProvider's own header already states. Wikipedia and the Arch
// Wiki are both MediaWiki, so both use MediaWiki's own standard search
// entry point (index.php?search=); YouTube and Reddit use their own
// documented results-page query parameters. Modeled directly on
// TimerProvider (timer.go): a self-contained, regex-anchored keyword
// match per query, never relying on the CLI layer to strip anything.
type SiteSearchProvider struct{}

func (SiteSearchProvider) Name() string { return "sitesearch" }

type siteSearch struct {
	key   string
	label string
	host  string // the site's own hostname — TagDefaults' LibreWolf history/bookmarks filter, and snapshotsForKeyword's (query.go) snapshot filter
	re    *regexp.Regexp
	url   func(term string) string
}

var siteSearches = []siteSearch{
	{"wiki", "Wikipedia", "en.wikipedia.org", regexp.MustCompile(`(?i)^wiki\s+(.+)$`),
		func(term string) string {
			return "https://en.wikipedia.org/w/index.php?search=" + url.QueryEscape(term)
		}},
	{"yt", "YouTube", "www.youtube.com", regexp.MustCompile(`(?i)^yt\s+(.+)$`),
		func(term string) string {
			return "https://www.youtube.com/results?search_query=" + url.QueryEscape(term)
		}},
	{"arch", "the Arch Wiki", "wiki.archlinux.org", regexp.MustCompile(`(?i)^arch\s+(.+)$`),
		func(term string) string {
			return "https://wiki.archlinux.org/index.php?search=" + url.QueryEscape(term)
		}},
	{"rddt", "Reddit", "www.reddit.com", regexp.MustCompile(`(?i)^rddt\s+(.+)$`),
		func(term string) string { return "https://www.reddit.com/search/?q=" + url.QueryEscape(term) }},
}

// siteSearchByKey looks up siteSearches by its keyword (case-insensitive),
// e.g. "wiki" — used by TagDefaults to know which site a locked tag names
// (all four share the "sitesearch" provider name) and by query.go's
// snapshotsForKeyword for the same reason.
func siteSearchByKey(key string) (siteSearch, bool) {
	for _, s := range siteSearches {
		if strings.EqualFold(s.key, key) {
			return s, true
		}
	}
	return siteSearch{}, false
}

func (p SiteSearchProvider) Query(_ context.Context, q string) []Result {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	for _, s := range siteSearches {
		m := s.re.FindStringSubmatch(q)
		if m == nil {
			continue
		}
		term := strings.TrimSpace(m[1])
		if term == "" {
			return nil
		}
		return []Result{{
			ID: s.key + ":" + term, Provider: p.Name(),
			Title:    "Search " + s.label + " for \"" + term + "\"",
			Subtitle: term,
			// Trusted as-is (rank.go): the generated Title doesn't
			// textually resemble the raw query ("wiki linux kernel") the
			// way matchWeight expects — same reasoning as TimerProvider's
			// own Score, see its comment.
			Score:  100,
			Action: Action{Kind: ActionOpenURL, Data: map[string]string{"url": s.url(term)}},
		}}
	}
	return nil
}

// TagDefaults returns the per-site default: the user's own LibreWolf
// bookmarks and top history for that one site's host — "common usage"
// for a tag locked with nothing typed yet, never content this provider
// invents. keyword picks which of the four sites (see siteSearchByKey);
// an unrecognised keyword (should not happen — Run only calls this for a
// keyword prefixProviders already routed here) yields nothing. Every
// missing piece (no LibreWolf profile, no sqlite3, a table that doesn't
// exist) degrades to contributing nothing, the same graceful-degradation
// contract every other provider gets from runCommand.
func (p SiteSearchProvider) TagDefaults(ctx context.Context, keyword string) []Result {
	site, ok := siteSearchByKey(keyword)
	if !ok {
		return nil
	}
	hostLike := librewolfHostLike(site.host)
	idPrefix := "sitesearch:" + site.key + ":"

	var out []Result
	if rows, ok := queryLibreWolfDB(ctx, librewolfPlacesDB, librewolfBookmarksSQL(hostLike)); ok {
		out = append(out, librewolfLinkResults(p.Name(), idPrefix, rows)...)
	}
	if rows, ok := queryLibreWolfDB(ctx, librewolfPlacesDB, librewolfHistorySQL(hostLike)); ok {
		out = append(out, librewolfLinkResults(p.Name(), idPrefix, rows)...)
	}
	return out
}
