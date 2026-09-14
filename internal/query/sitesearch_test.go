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

func TestSiteSearchExplicitScoreSurvivesRanking(t *testing.T) {
	// The regression this whole shape guards against (docs/TODO.md's
	// timer/alarm entry already found this once): a Result whose Score is
	// left at Rank's zero default, and whose Title doesn't fuzzy-match the
	// raw query, is silently dropped rather than ranked low.
	r := siteQuery(t, "wiki linux kernel")
	ranked := Rank(r, "wiki linux kernel", nil)
	if len(ranked) != 1 {
		t.Fatalf("Rank(%v) = %v, want the sitesearch result to survive ranking", r, ranked)
	}
}
