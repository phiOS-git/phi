package query

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// ApplicationsProvider lists apps by scanning .desktop files (desktop
// entry spec). Re-scans every keystroke (simple, likely fast for hundreds).
type ApplicationsProvider struct{}

func (ApplicationsProvider) Name() string { return "application" }

// Query, called with q == "", returns every application unscored (Score
// left at 0, letting the caller decide the order) — this is what
// query.go's emptyQueryApps and TagDefaults below both build on to list
// every application for an empty or locked-but-untyped "app" query.
func (p ApplicationsProvider) Query(_ context.Context, q string) []Result {
	// Prefix "app <name>": strip "app " and score term only, not full q.
	prefixed := false
	term := q
	if len(q) >= 4 && strings.EqualFold(q[:4], "app ") {
		term = strings.TrimSpace(q[4:])
		if term == "" {
			return nil
		}
		prefixed = true
	}

	var out []Result
	seen := map[string]bool{} // a user override in XDG_DATA_HOME shadows the same id in a system dir
	for _, dir := range desktopEntryDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".desktop") {
				continue
			}
			if seen[e.Name()] {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			entry, ok := parseDesktopEntry(string(data))
			if !ok || entry.noDisplay || !entry.isApplication {
				continue
			}
			score := 0.0
			if prefixed {
				score = matchWeight(entry.name, term)
				if score == 0 {
					continue
				}
			}
			seen[e.Name()] = true
			out = append(out, Result{
				ID: "app:" + e.Name(), Provider: p.Name(),
				Title: entry.name, Subtitle: entry.execClean,
				Score:  score,
				Action: Action{Kind: ActionExec, Data: map[string]string{"command": entry.execClean}},
			})
		}
	}
	return out
}

// TagDefaults returns the "app" tag's default list: literally every
// application, alphabetized (Query(ctx, "") itself returns them in
// directory-scan order, which is arbitrary and a poor fit for a defaults
// list a user is meant to browse) — the "app" tag locked with nothing
// typed yet has nothing more specific to fall back to than that.
func (p ApplicationsProvider) TagDefaults(ctx context.Context, _ string) []Result {
	apps := p.Query(ctx, "")
	sortByFrecencyThenTitle(apps, nil)
	return apps
}

// desktopEntryDirs follows the XDG base directory spec's search order for
// application directories: user overrides first (XDG_DATA_HOME, default
// ~/.local/share), then each of XDG_DATA_DIRS (default
// /usr/local/share:/usr/share) in order, then Flatpak's own export
// directories explicitly.
//
// Flatpak exports live in <data-home>/flatpak/exports/share/applications
// and /var/lib/flatpak/exports/share/applications, neither of which the
// XDG_DATA_DIRS default above covers. They only reach this scan when the
// session environment happens to carry them, but phi query is a fresh
// process per keystroke inheriting whatever the session set — the same gap
// this project always closes by adding the fallback explicitly rather than
// trusting the session to be configured correctly. A dedup pass guards
// against double-scanning a directory a correctly configured session's
// XDG_DATA_DIRS already listed.
func desktopEntryDirs() []string {
	seen := map[string]bool{}
	var dirs []string
	add := func(d string) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		dirs = append(dirs, d)
	}

	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	if dataHome != "" {
		add(filepath.Join(dataHome, "applications"))
	}

	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	for _, d := range strings.Split(dataDirs, ":") {
		if d != "" {
			add(filepath.Join(d, "applications"))
		}
	}

	if dataHome != "" {
		add(filepath.Join(dataHome, "flatpak", "exports", "share", "applications"))
	}
	add("/var/lib/flatpak/exports/share/applications")

	return dirs
}

type desktopEntry struct {
	name          string
	execClean     string
	noDisplay     bool
	isApplication bool
}

// parseDesktopEntry reads the [Desktop Entry] section of a .desktop file's
// raw content. Deliberately not a general INI parser: only the four keys
// this provider needs, first occurrence wins (a real .desktop file may
// have localized "Name[it]=" variants after the bare "Name=" — skipped,
// since the launcher only ever needs the plain English name).
func parseDesktopEntry(content string) (desktopEntry, bool) {
	var e desktopEntry
	inSection := false
	sawType := false
	haveName := false

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inSection = line == "[Desktop Entry]"
			continue
		}
		if !inSection {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "Name":
			if !haveName {
				e.name = value
				haveName = true
			}
		case "Exec":
			e.execClean = cleanExecString(value)
		case "NoDisplay":
			e.noDisplay = strings.EqualFold(value, "true")
		case "Type":
			sawType = true
			e.isApplication = value == "Application"
		}
	}
	if !haveName || e.execClean == "" {
		return desktopEntry{}, false
	}
	if !sawType {
		// Most real-world entries set Type=Application, but the spec's
		// default when absent is implementation-defined; treating "no Type
		// key but a real Name and Exec" as an application is the more
		// useful default for a launcher than silently dropping it.
		e.isApplication = true
	}
	return e, true
}

// cleanExecString strips freedesktop field codes (%f, %F, %u, %U, %i, %c,
// %k, and the deprecated %d/%D/%n/%N/%v/%m) from an Exec= value — this
// provider launches with no file/URL argument to substitute, so every
// field code is simply removed rather than resolved.
func cleanExecString(execValue string) string {
	fields := strings.Fields(execValue)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		switch f {
		case "%f", "%F", "%u", "%U", "%i", "%c", "%k", "%d", "%D", "%n", "%N", "%v", "%m":
			continue
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}
