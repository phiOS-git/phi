package query

// FilesProvider's default list for the "file" tag locked with nothing
// typed yet: recently-used files (parsed from the freedesktop
// recently-used.xbel GTK/Qt applications already maintain), then the
// common directories a user is likely to want ($HOME plus the XDG user
// dirs). Both parsers below take raw file content and $HOME as plain
// arguments — no environment or filesystem access inside them — so they
// can be unit tested without touching the real machine.

import (
	"context"
	"encoding/xml"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// filesRecentMax caps how many recently-used files TagDefaults offers to
// a sensible number for a runner-bar dropdown.
const filesRecentMax = 15

type xbelDocument struct {
	XMLName   xml.Name       `xml:"xbel"`
	Bookmarks []xbelBookmark `xml:"bookmark"`
}

type xbelBookmark struct {
	Href     string `xml:"href,attr"`
	Modified string `xml:"modified,attr"`
	Visited  string `xml:"visited,attr"`
}

// parseRecentlyUsedXBEL parses the freedesktop recently-used.xbel format
// (GTK, Qt and others all append to the one shared file at
// $XDG_DATA_HOME/recently-used.xbel), returning absolute filesystem paths
// decoded from each entry's `file://` href, newest first. "Newest" is the
// later of an entry's own modified and visited timestamps (either may be
// the more recent depending on what the writing application actually
// updates), parsed as RFC3339 with optional fractional seconds
// (time.RFC3339Nano — a plain time.RFC3339 layout fails to parse the
// fractional-second timestamps GLib itself writes). A non-`file://` href
// or an unparseable href is skipped; a path that appears more than once
// (each application updates its own bookmark element in place in
// practice, but nothing here assumes that) keeps the latest of its own
// occurrences' timestamps. Malformed XML yields nil.
func parseRecentlyUsedXBEL(data []byte) []string {
	var doc xbelDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil
	}

	best := map[string]time.Time{}
	var order []string
	for _, b := range doc.Bookmarks {
		u, err := url.Parse(b.Href)
		if err != nil || u.Scheme != "file" || u.Path == "" {
			continue
		}
		when := laterOf(b.Modified, b.Visited)
		if existing, ok := best[u.Path]; !ok {
			order = append(order, u.Path)
			best[u.Path] = when
		} else if when.After(existing) {
			best[u.Path] = when
		}
	}

	sort.SliceStable(order, func(i, j int) bool { return best[order[i]].After(best[order[j]]) })
	return order
}

// laterOf parses two RFC3339(-Nano) timestamps (either may be empty or
// unparseable — a real recently-used.xbel entry always has both, but
// nothing here assumes it) and returns the later of the two, or the zero
// Time if neither parses.
func laterOf(a, b string) time.Time {
	ta, errA := time.Parse(time.RFC3339Nano, a)
	tb, errB := time.Parse(time.RFC3339Nano, b)
	switch {
	case errA != nil && errB != nil:
		return time.Time{}
	case errA != nil:
		return tb
	case errB != nil:
		return ta
	case tb.After(ta):
		return tb
	default:
		return ta
	}
}

// xdgUserDirKeys/xdgUserDirFallbacks pair up: parseUserDirs's key for
// each standard XDG user directory, and the subdirectory name under
// $HOME to fall back to when user-dirs.dirs does not define it (missing
// entirely, or missing just that one key).
var xdgUserDirKeys = []string{
	"XDG_DESKTOP_DIR", "XDG_DOCUMENTS_DIR", "XDG_DOWNLOADS_DIR",
	"XDG_MUSIC_DIR", "XDG_PICTURES_DIR", "XDG_VIDEOS_DIR",
}
var xdgUserDirFallbacks = []string{"Desktop", "Documents", "Downloads", "Music", "Pictures", "Videos"}

// parseUserDirs parses XDG's user-dirs.dirs shell-variable format
// (`XDG_DOCUMENTS_DIR="$HOME/Documents"`, one assignment per line, `#`
// comments), returning a map from each XDG_*_DIR key to its expanded,
// absolute value with the literal `$HOME` token substituted for home.
// Lines that are not a `XDG_..._DIR=` assignment are ignored, so this
// tolerates the file's own generated header comment unmodified.
func parseUserDirs(data []byte, home string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if !strings.HasPrefix(key, "XDG_") || !strings.HasSuffix(key, "_DIR") {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		value = strings.ReplaceAll(value, "$HOME", home)
		out[key] = value
	}
	return out
}

// commonDirectories lists $HOME followed by each standard XDG user
// directory (from user-dirs.dirs if $XDG_CONFIG_HOME/user-dirs.dirs
// parses, else the plain Desktop/Documents/... fallback under $HOME),
// keeping only directories that actually exist and deduplicating by
// their cleaned absolute path — a directory the user has disabled in
// their user dirs is commonly still written out pointing at bare
// "$HOME/", which must collapse into the single $HOME entry rather than
// appearing twice. Order is always home first, then the six XDG keys in
// xdgUserDirKeys's own fixed order — never a map iteration.
func commonDirectories(home string) []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		clean := filepath.Clean(d)
		if seen[clean] {
			return
		}
		info, err := os.Stat(clean)
		if err != nil || !info.IsDir() {
			return
		}
		seen[clean] = true
		dirs = append(dirs, clean)
	}

	add(home)

	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	var parsed map[string]string
	if data, err := os.ReadFile(filepath.Join(configHome, "user-dirs.dirs")); err == nil {
		parsed = parseUserDirs(data, home)
	}

	for i, key := range xdgUserDirKeys {
		if d, ok := parsed[key]; ok && d != "" {
			add(d)
			continue
		}
		add(filepath.Join(home, xdgUserDirFallbacks[i]))
	}

	return dirs
}

// TagDefaults returns the "file" tag's default list: recently-used files
// (newest first, existing ones only, capped at filesRecentMax) then the
// common directories (see commonDirectories) — "common usage" for the
// "file" tag locked with nothing typed yet. Reuses FilesProvider's own
// result/action shape (openCommand picks the image viewer vs. xdg-open;
// shellQuote makes the path shell-safe).
func (p FilesProvider) TagDefaults(_ context.Context, _ string) []Result {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	var results []Result

	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	if data, err := os.ReadFile(filepath.Join(dataHome, "recently-used.xbel")); err == nil {
		count := 0
		for _, path := range parseRecentlyUsedXBEL(data) {
			if count >= filesRecentMax {
				break
			}
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			results = append(results, Result{
				ID: "file:" + path, Provider: p.Name(),
				Title: filepath.Base(path), Subtitle: path,
				Action: Action{Kind: ActionExec, Data: map[string]string{"command": openCommand(path)}},
			})
			count++
		}
	}

	for _, dir := range commonDirectories(home) {
		title := filepath.Base(dir)
		if dir == filepath.Clean(home) {
			title = "Home"
		}
		results = append(results, Result{
			ID: "file:" + dir, Provider: p.Name(),
			Title: title, Subtitle: dir,
			Action: Action{Kind: ActionExec, Data: map[string]string{"command": openCommand(dir)}},
		})
	}

	return results
}
