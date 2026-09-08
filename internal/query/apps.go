package query

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// ApplicationsProvider lists installed applications by scanning .desktop
// files (freedesktop Desktop Entry spec) — the same source Quickshell's
// own DesktopEntries type reads on the shell side, duplicated here rather
// than reached through a running shell (query.go's own note explains why:
// phi stays self-contained and testable without one).
//
// Cold start (query.go's own constraint): re-scanning every .desktop file
// on every keystroke is the simplest correct thing, and for the low
// hundreds of entries a real system has is plausibly fast enough — this is
// genuinely unverified without real hardware, flagged here rather than
// guessed at with an unbuilt cache this step has no way to validate either.
type ApplicationsProvider struct{}

func (ApplicationsProvider) Name() string { return "application" }

func (p ApplicationsProvider) Query(_ context.Context, q string) []Result {
	if q == "" {
		return nil
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
			seen[e.Name()] = true
			out = append(out, Result{
				ID: "app:" + e.Name(), Provider: p.Name(),
				Title: entry.name, Subtitle: entry.execClean,
				Action: Action{Kind: ActionExec, Data: map[string]string{"command": entry.execClean}},
			})
		}
	}
	return out
}

// desktopEntryDirs follows the XDG base directory spec's search order for
// application directories: user overrides first (XDG_DATA_HOME, default
// ~/.local/share), then each of XDG_DATA_DIRS (default
// /usr/local/share:/usr/share) in order.
func desktopEntryDirs() []string {
	var dirs []string

	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	if dataHome != "" {
		dirs = append(dirs, filepath.Join(dataHome, "applications"))
	}

	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	for _, d := range strings.Split(dataDirs, ":") {
		if d != "" {
			dirs = append(dirs, filepath.Join(d, "applications"))
		}
	}
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
// since this project's own language rule (CLAUDE.md) keeps everything
// this agent writes in English and there is no requirement here to
// localize the launcher).
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
