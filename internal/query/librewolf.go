package query

// LibreWolf profile discovery and places.sqlite/formhistory.sqlite
// queries backing WebSearchProvider's and SiteSearchProvider's "common
// usage" defaults: the user's own browsing history, never anything a
// provider invents. Every failure — no LibreWolf, no default profile,
// sqlite3 missing, a locked or unreadable database, a table that doesn't
// exist — degrades to nil silently, the same contract runCommand
// (exec.go) already gives every other provider that shells out.
// Read-only throughout: the databases are opened via a
// `file:...?immutable=1` URI specifically so a running LibreWolf's own
// lock is never contended.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	librewolfPlacesDB      = "places.sqlite"
	librewolfFormHistoryDB = "formhistory.sqlite"

	librewolfBookmarksLimit   = 15
	librewolfHistoryLimit     = 15
	librewolfSearchTermsLimit = 10
	librewolfRowSeparator     = "\t"
)

// librewolfProfileDir resolves LibreWolf's default profile directory via
// ~/.librewolf/profiles.ini, or ok=false if LibreWolf has never run here.
func librewolfProfileDir() (dir string, ok bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	root := filepath.Join(home, ".librewolf")
	data, err := os.ReadFile(filepath.Join(root, "profiles.ini"))
	if err != nil {
		return "", false
	}
	path, ok := profilesIniDefault(string(data))
	if !ok {
		return "", false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return "", false
	}
	return path, true
}

type iniSection struct {
	name string
	kv   map[string]string
}

// parseIni is a minimal INI reader for exactly what profiles.ini needs:
// ordered `[Section]` blocks of `key=value` lines. Not a general-purpose
// parser (no comments-mid-line, no quoting) — profiles.ini itself never
// needs either.
func parseIni(content string) []iniSection {
	var sections []iniSection
	var cur *iniSection
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sections = append(sections, iniSection{name: line[1 : len(line)-1], kv: map[string]string{}})
			cur = &sections[len(sections)-1]
			continue
		}
		if cur == nil {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		cur.kv[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return sections
}

// profilesIniDefault picks the profile path profiles.ini names as
// default, pure and independent of the filesystem so it can be unit
// tested without a real LibreWolf install. An `[Install...]` section's
// own `Default=` (added for Firefox/LibreWolf's profile-refresh feature)
// wins when present — it is what launching the browser normally actually
// uses, and can name a different profile than any `[ProfileN]` section's
// own `Default=1` flag. Failing that, the first `[ProfileN]` section with
// `Default=1` and a `Path=` is used. The returned path may be relative to
// profiles.ini's own directory or absolute; the caller (librewolfProfileDir)
// resolves that with filepath.IsAbs rather than trusting an `IsRelative=`
// key, since `[Install...]` sections carry no such key at all.
func profilesIniDefault(content string) (path string, ok bool) {
	sections := parseIni(content)

	for _, s := range sections {
		if !strings.HasPrefix(s.name, "Install") {
			continue
		}
		if p := s.kv["Default"]; p != "" {
			return p, true
		}
	}

	for _, s := range sections {
		if !strings.HasPrefix(s.name, "Profile") {
			continue
		}
		if s.kv["Default"] != "1" {
			continue
		}
		if p := s.kv["Path"]; p != "" {
			return p, true
		}
	}

	return "", false
}

// librewolfHostLike builds the `url LIKE` pattern matching any URL on
// host, for SQL WHERE clauses that need to scope a query to one site.
// Empty host means "no filter" (the caller passes "" straight through to
// librewolfBookmarksSQL/librewolfHistorySQL, which omit the clause
// entirely).
func librewolfHostLike(host string) string {
	if host == "" {
		return ""
	}
	return "%://" + host + "/%"
}

// sqlSanitize wraps a SQL column expression so embedded tabs, newlines or
// carriage returns (a real page title can contain any of these) can never
// land in sqlite3's tab-separated output and shift a row's columns —
// parseLibreWolfRows has no other way to tell a literal tab in a title
// apart from the column separator itself.
func sqlSanitize(col string) string {
	return "REPLACE(REPLACE(REPLACE(" + col + ", char(9), ' '), char(10), ' '), char(13), ' ')"
}

// librewolfBookmarksSQL selects (url, title) for every bookmark
// (moz_bookmarks.type = 1, "a real bookmark" in Places' own type enum —
// 2 is a folder, 3 a separator), newest first, optionally restricted to
// hostLike (see librewolfHostLike). title falls back from the bookmark's
// own title to the page's title to NULL (sqlite3 -separator renders NULL
// as an empty column, and librewolfLinkResults falls back to the URL).
func librewolfBookmarksSQL(hostLike string) string {
	where := "b.type = 1"
	if hostLike != "" {
		where += " AND p.url LIKE '" + hostLike + "'"
	}
	return "SELECT " + sqlSanitize("p.url") + ", " +
		sqlSanitize("COALESCE(NULLIF(b.title,''), NULLIF(p.title,''))") +
		" FROM moz_bookmarks b JOIN moz_places p ON b.fk = p.id WHERE " + where +
		" ORDER BY b.dateAdded DESC LIMIT " + strconv.Itoa(librewolfBookmarksLimit) + ";"
}

// librewolfHistorySQL selects (url, title) for the top history entries by
// Places' own frecency ranking, optionally restricted to hostLike.
func librewolfHistorySQL(hostLike string) string {
	where := "frecency > 0"
	if hostLike != "" {
		where += " AND url LIKE '" + hostLike + "'"
	}
	return "SELECT " + sqlSanitize("url") + ", " + sqlSanitize("NULLIF(title,'')") +
		" FROM moz_places WHERE " + where +
		" ORDER BY frecency DESC LIMIT " + strconv.Itoa(librewolfHistoryLimit) + ";"
}

// librewolfSearchTermsSQL selects recent address-bar search terms
// (fieldname 'searchbar-history'), newest first.
func librewolfSearchTermsSQL() string {
	return "SELECT " + sqlSanitize("value") + " FROM moz_formhistory WHERE fieldname = 'searchbar-history'" +
		" ORDER BY lastUsed DESC LIMIT " + strconv.Itoa(librewolfSearchTermsLimit) + ";"
}

// queryLibreWolfDB runs sql against dbFile (one of librewolfPlacesDB or
// librewolfFormHistoryDB) inside the default LibreWolf profile, read-only
// and immutable so a running browser's own lock never blocks or corrupts
// anything. Every failure (no profile, no sqlite3 on PATH, the file
// missing — formhistory.sqlite in particular may never have been
// created) is silent: ok is false, never an error a caller must handle.
// One invocation per database/query, deliberately: a missing table in one
// query (formhistory.sqlite before any search has ever been typed, say)
// must not take down a sibling query's own results.
func queryLibreWolfDB(ctx context.Context, dbFile, sql string) (rows [][]string, ok bool) {
	profile, ok := librewolfProfileDir()
	if !ok {
		return nil, false
	}
	dbPath := filepath.Join(profile, dbFile)
	if _, err := os.Stat(dbPath); err != nil {
		return nil, false
	}
	uri := "file:" + dbPath + "?immutable=1"
	out, available := runCommand(ctx, "sqlite3", "-separator", librewolfRowSeparator, uri, sql)
	if !available || out == "" {
		return nil, false
	}
	return parseLibreWolfRows(out), true
}

// parseLibreWolfRows splits sqlite3's tab-separated stdout into rows of
// fields — factored out from queryLibreWolfDB so it can be unit-tested
// without a real sqlite3 binary or database (this project's own testing
// convention for every runCommand-backed provider — see e.g. windows.go's
// parseHyprctlClients). runCommand TrimSpaces its output, which can eat a
// final row's trailing empty column along with the trailing newline
// (".../q\n" -> "...q"); callers already tolerate a short row (see
// librewolfLinkResults), so this makes no attempt to restore it.
func parseLibreWolfRows(out string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		rows = append(rows, strings.Split(line, librewolfRowSeparator))
	}
	return rows
}

// librewolfLinkResults turns (url, title) rows from
// librewolfBookmarksSQL/librewolfHistorySQL into Results: title falls
// back to the URL itself when the query's own title column came back
// empty (a NULL title, or a row parseLibreWolfRows had to read short —
// see its own comment). idPrefix plus the URL forms the ID, so the same
// URL surfacing from both the bookmarks and the history query — a common
// case — collapses to one entry wherever a caller dedupes by ID
// (lockedTagDefaults, query.go).
func librewolfLinkResults(provider, idPrefix string, rows [][]string) []Result {
	var out []Result
	for _, row := range rows {
		if len(row) == 0 || strings.TrimSpace(row[0]) == "" {
			continue
		}
		pageURL := row[0]
		title := pageURL
		if len(row) > 1 && strings.TrimSpace(row[1]) != "" {
			title = row[1]
		}
		out = append(out, Result{
			ID: idPrefix + pageURL, Provider: provider,
			Title: title, Subtitle: pageURL,
			Action: Action{Kind: ActionOpenURL, Data: map[string]string{"url": pageURL}},
		})
	}
	return out
}

// librewolfSearchTermResults turns (value) rows from
// librewolfSearchTermsSQL into Results shaped exactly like
// WebSearchProvider's own live search results ("websearch:"+term ID,
// webSearchURL), so a term that was both typed live and remembered from
// formhistory collapses to one entry wherever a caller dedupes by ID.
func librewolfSearchTermResults(provider string, rows [][]string) []Result {
	var out []Result
	for _, row := range rows {
		if len(row) == 0 || strings.TrimSpace(row[0]) == "" {
			continue
		}
		term := row[0]
		out = append(out, Result{
			ID: "websearch:" + term, Provider: provider,
			Title: "Search the web for \"" + term + "\"", Subtitle: term,
			Action: Action{Kind: ActionOpenURL, Data: map[string]string{"url": webSearchURL(term)}},
		})
	}
	return out
}
