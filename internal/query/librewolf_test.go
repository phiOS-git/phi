package query

import (
	"context"
	"strings"
	"testing"
)

func TestProfilesIniDefaultPrefersInstallSection(t *testing.T) {
	content := `[Profile0]
Name=default
IsRelative=1
Path=abc123.default-release
Default=1

[Install4F96D1932A9F858E]
Default=xyz789.default-release
Locked=1
`
	path, ok := profilesIniDefault(content)
	if !ok || path != "xyz789.default-release" {
		t.Fatalf("profilesIniDefault = %q, %v, want the Install section's Default", path, ok)
	}
}

func TestProfilesIniDefaultFallsBackToProfileDefault(t *testing.T) {
	content := `[Profile1]
Name=other
Default=0
Path=other.profile

[Profile0]
Name=default
IsRelative=1
Path=abc123.default-release
Default=1
`
	path, ok := profilesIniDefault(content)
	if !ok || path != "abc123.default-release" {
		t.Fatalf("profilesIniDefault = %q, %v, want the Default=1 profile's Path", path, ok)
	}
}

func TestProfilesIniDefaultNoDefaultAnywhere(t *testing.T) {
	content := "[Profile0]\nName=default\nPath=abc\n"
	if _, ok := profilesIniDefault(content); ok {
		t.Error("profilesIniDefault with no Default=1 anywhere = ok, want not found")
	}
}

func TestProfilesIniDefaultMalformed(t *testing.T) {
	if _, ok := profilesIniDefault("not an ini file at all"); ok {
		t.Error("profilesIniDefault(malformed) = ok, want not found")
	}
}

func TestParseLibreWolfRows(t *testing.T) {
	out := "https://example.com\tExample\nhttps://example.org\tExample Org\n"
	got := parseLibreWolfRows(out)
	want := [][]string{{"https://example.com", "Example"}, {"https://example.org", "Example Org"}}
	if len(got) != len(want) {
		t.Fatalf("parseLibreWolfRows = %v, want %v", got, want)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) || got[i][0] != want[i][0] || got[i][1] != want[i][1] {
			t.Errorf("parseLibreWolfRows[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestParseLibreWolfRowsHandlesShortTrailingRow guards runCommand's own
// TrimSpace, which can eat a trailing empty column's tab off the very
// last row of sqlite3's output (queryLibreWolfDB's own comment) — the row
// parser must not panic or misbehave, just produce a short row every
// caller already tolerates (librewolfLinkResults falls back to the URL).
func TestParseLibreWolfRowsHandlesShortTrailingRow(t *testing.T) {
	out := "https://example.com\tExample\nhttps://example.org"
	got := parseLibreWolfRows(out)
	if len(got) != 2 || len(got[1]) != 1 || got[1][0] != "https://example.org" {
		t.Fatalf("parseLibreWolfRows(short trailing row) = %v", got)
	}
}

func TestParseLibreWolfRowsEmpty(t *testing.T) {
	if got := parseLibreWolfRows(""); got != nil {
		t.Errorf("parseLibreWolfRows(\"\") = %v, want nil", got)
	}
}

func TestLibrewolfLinkResultsFallsBackToURL(t *testing.T) {
	rows := [][]string{{"https://example.com", ""}, {"https://example.org", "Example Org"}}
	got := librewolfLinkResults("websearch", "websearch:", rows)
	if len(got) != 2 {
		t.Fatalf("librewolfLinkResults = %v, want 2 entries", got)
	}
	if got[0].Title != "https://example.com" {
		t.Errorf("Title = %q, want the URL itself when the title column is empty", got[0].Title)
	}
	if got[1].Title != "Example Org" {
		t.Errorf("Title = %q, want the row's own title", got[1].Title)
	}
	if got[0].ID != "websearch:https://example.com" {
		t.Errorf("ID = %q, want idPrefix+url", got[0].ID)
	}
}

func TestLibrewolfSearchTermResultsMatchWebSearchProviderIDScheme(t *testing.T) {
	rows := [][]string{{"golang tutorial"}}
	got := librewolfSearchTermResults("websearch", rows)
	if len(got) != 1 || got[0].ID != "websearch:golang tutorial" {
		t.Fatalf("librewolfSearchTermResults = %v, want the same ID scheme a live web search uses", got)
	}
	if got[0].Action.Data["url"] != webSearchURL("golang tutorial") {
		t.Errorf("url = %q, want webSearchURL(term)", got[0].Action.Data["url"])
	}
}

func TestLibrewolfHostLike(t *testing.T) {
	if got := librewolfHostLike(""); got != "" {
		t.Errorf("librewolfHostLike(\"\") = %q, want \"\" (no filter)", got)
	}
	if got := librewolfHostLike("en.wikipedia.org"); got != "%://en.wikipedia.org/%" {
		t.Errorf("librewolfHostLike(\"en.wikipedia.org\") = %q", got)
	}
}

// TestLibrewolfBookmarksSQLFiltersHostBeforeLimit guards against
// filtering by host in Go after an unfiltered top-N SQL query, which
// would leave a low-traffic site's own defaults nearly empty — the WHERE
// clause carrying the host filter must appear before the LIMIT.
func TestLibrewolfBookmarksSQLFiltersHostBeforeLimit(t *testing.T) {
	sql := librewolfBookmarksSQL(librewolfHostLike("en.wikipedia.org"))
	if !strings.Contains(sql, "en.wikipedia.org") {
		t.Fatalf("librewolfBookmarksSQL missing the host filter: %s", sql)
	}
	if strings.Index(sql, "en.wikipedia.org") > strings.Index(sql, "LIMIT") {
		t.Errorf("librewolfBookmarksSQL filters after LIMIT: %s", sql)
	}
}

func TestLibrewolfHistorySQLFiltersHostBeforeLimit(t *testing.T) {
	sql := librewolfHistorySQL(librewolfHostLike("www.reddit.com"))
	if !strings.Contains(sql, "www.reddit.com") {
		t.Fatalf("librewolfHistorySQL missing the host filter: %s", sql)
	}
	if strings.Index(sql, "www.reddit.com") > strings.Index(sql, "LIMIT") {
		t.Errorf("librewolfHistorySQL filters after LIMIT: %s", sql)
	}
}

func TestLibrewolfSQLSanitizesTabsAndNewlines(t *testing.T) {
	for _, sql := range []string{librewolfBookmarksSQL(""), librewolfHistorySQL(""), librewolfSearchTermsSQL()} {
		if !strings.Contains(sql, "char(9)") || !strings.Contains(sql, "char(10)") {
			t.Errorf("SQL missing tab/newline sanitization, would break the tab-separated row parser: %s", sql)
		}
	}
}

// TestQueryLibreWolfDBDegradesWithoutProfile is the "skip anything
// needing sqlite3" contract at the querying layer itself: with no
// ~/.librewolf at all (HOME points at a fresh temp dir, never the real
// machine's own profile), this must return nil/false rather than error.
func TestQueryLibreWolfDBDegradesWithoutProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if rows, ok := queryLibreWolfDB(context.Background(), librewolfPlacesDB, "SELECT 1;"); ok || rows != nil {
		t.Errorf("queryLibreWolfDB with no LibreWolf profile = %v, %v, want nil, false", rows, ok)
	}
}
