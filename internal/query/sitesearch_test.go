package query

import (
	"context"
	"testing"
)

func siteQuery(t *testing.T, q string) []Result {
	t.Helper()
	return SiteSearchProvider{}.Query(context.Background(), q)
}

func TestSiteSearchWikipedia(t *testing.T) {
	r := siteQuery(t, "wiki linux kernel")
	if len(r) != 1 {
		t.Fatalf("Query(%q) = %v, want exactly one result", "wiki linux kernel", r)
	}
	if r[0].Action.Kind != ActionOpenURL {
		t.Errorf("Action.Kind = %q, want openURL", r[0].Action.Kind)
	}
	want := "https://en.wikipedia.org/w/index.php?search=linux+kernel"
	if r[0].Action.Data["url"] != want {
		t.Errorf("url = %q, want %q", r[0].Action.Data["url"], want)
	}
}

func TestSiteSearchYouTube(t *testing.T) {
	r := siteQuery(t, "yt lofi beats")
	want := "https://www.youtube.com/results?search_query=lofi+beats"
	if len(r) != 1 || r[0].Action.Data["url"] != want {
		t.Fatalf("Query(%q) = %v, want url %q", "yt lofi beats", r, want)
	}
}

func TestSiteSearchArchWiki(t *testing.T) {
	r := siteQuery(t, "arch pacman -Syu")
	want := "https://wiki.archlinux.org/index.php?search=pacman+-Syu"
	if len(r) != 1 || r[0].Action.Data["url"] != want {
		t.Fatalf("Query(%q) = %v, want url %q", "arch pacman -Syu", r, want)
	}
}

func TestSiteSearchReddit(t *testing.T) {
	r := siteQuery(t, "rddt archlinux")
	want := "https://www.reddit.com/search/?q=archlinux"
	if len(r) != 1 || r[0].Action.Data["url"] != want {
		t.Fatalf("Query(%q) = %v, want url %q", "rddt archlinux", r, want)
	}
}

func TestSiteSearchKeywordAloneYieldsNothing(t *testing.T) {
	if r := siteQuery(t, "wiki"); r != nil {
		t.Errorf("Query(%q) = %v, want nil — keyword with nothing after it", "wiki", r)
	}
}

func TestSiteSearchUnknownKeyword(t *testing.T) {
	if r := siteQuery(t, "wikipedia linux"); r != nil {
		t.Errorf("Query(%q) = %v, want nil — \"wikipedia\" is not the \"wiki\" keyword", "wikipedia linux", r)
	}
}

func TestSiteSearchByKey(t *testing.T) {
	s, ok := siteSearchByKey("WIKI") // case-insensitive
	if !ok || s.host != "en.wikipedia.org" {
		t.Fatalf("siteSearchByKey(\"WIKI\") = %+v, %v, want the Wikipedia entry", s, ok)
	}
	if _, ok := siteSearchByKey("not-a-site"); ok {
		t.Error("siteSearchByKey(\"not-a-site\") = ok, want not found")
	}
}

// TestSiteSearchTagDefaultsUnknownKeyword covers the defensive branch
// query.go's lockedTagDefaults should never actually reach (Run only ever
// calls TagDefaults with a keyword prefixProviders already routed to this
// provider), but TagDefaults must still degrade quietly rather than panic.
func TestSiteSearchTagDefaultsUnknownKeyword(t *testing.T) {
	if got := (SiteSearchProvider{}).TagDefaults(context.Background(), "not-a-site"); got != nil {
		t.Errorf("TagDefaults(\"not-a-site\") = %v, want nil", got)
	}
}

// TestSiteSearchTagDefaultsDegradesWithoutLibreWolf covers the "skip
// anything needing sqlite3" contract: with no ~/.librewolf at all,
// TagDefaults must return nil rather than erroring — home is pointed at a
// fresh temp dir so this never touches the real machine's own LibreWolf
// profile.
func TestSiteSearchTagDefaultsDegradesWithoutLibreWolf(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := (SiteSearchProvider{}).TagDefaults(context.Background(), "wiki"); got != nil {
		t.Errorf("TagDefaults(\"wiki\") with no LibreWolf profile = %v, want nil", got)
	}
}

func TestSiteSearchExplicitScoreSurvivesRanking(t *testing.T) {
	// Guards against the same failure class as TimerProvider's own
	// regression test: a Result whose Score is left at Rank's zero
	// default, and whose Title doesn't fuzzy-match the raw query, is
	// silently dropped rather than ranked low.
	r := siteQuery(t, "wiki linux kernel")
	ranked := Rank(r, "wiki linux kernel", nil)
	if len(ranked) != 1 {
		t.Fatalf("Rank(%v) = %v, want the sitesearch result to survive ranking", r, ranked)
	}
}
