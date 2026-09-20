package query

import (
	"context"
	"net/url"
	"regexp"
	"strings"
)

// SiteSearchProvider answers the runner-bar prefix feature's named
// per-site shortcuts (Requested: "Add prefix feature to the runner
// bar... website specific like wiki/yt/arch/rddt"): one keyword per site,
// each opening that site's own search-results page for the rest of the
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
	re    *regexp.Regexp
	url   func(term string) string
}

var siteSearches = []siteSearch{
	{"wiki", "Wikipedia", regexp.MustCompile(`(?i)^wiki\s+(.+)$`),
		func(term string) string {
			return "https://en.wikipedia.org/w/index.php?search=" + url.QueryEscape(term)
		}},
	{"yt", "YouTube", regexp.MustCompile(`(?i)^yt\s+(.+)$`),
		func(term string) string {
			return "https://www.youtube.com/results?search_query=" + url.QueryEscape(term)
		}},
	{"arch", "the Arch Wiki", regexp.MustCompile(`(?i)^arch\s+(.+)$`),
		func(term string) string {
			return "https://wiki.archlinux.org/index.php?search=" + url.QueryEscape(term)
		}},
	{"rddt", "Reddit", regexp.MustCompile(`(?i)^rddt\s+(.+)$`),
		func(term string) string { return "https://www.reddit.com/search/?q=" + url.QueryEscape(term) }},
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
