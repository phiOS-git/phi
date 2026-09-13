package query

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// FilesProvider is the file-search provider (S-33 AGENT: "files"), backed
// by `fd` (already in profiles/base/packages.txt, master plan §15 — not a
// new dependency). This is a LIVE search, never a persisted index: I-08's
// constraint on a file index living on the encrypted volume and excluded
// from sync applies to a stored index, which this provider does not build,
// so it does not apply here.
//
// The result count is capped in Go after fd runs, not via an fd flag —
// this project's own discipline elsewhere (S-23's brightness type, C-11)
// is to verify an external tool's exact interface before depending on it,
// and this agent could not confirm the installed fd version's flag name
// for a result cap without guessing; truncating the already-returned
// output needs no such guess.
const filesMaxResults = 8

// phiosImvAppID is passed to imv's own "-i" flag so the window it opens has
// a known, fixed class regardless of imv's undocumented default — matching
// this project's existing "phios-btop" precedent for reliably targeting a
// CLI-adjacent app's window from a Hyprland window rule
// (phios-dotfiles' hyprland.lua.tmpl).
const phiosImvAppID = "phios-imv"

// imageExtensions are the file types opened directly with imv (installed by
// every desktop profile, profiles/desktop/packages.txt) instead of through
// xdg-open. xdg-open depends on a mimeapps.list default association that
// this repository does not ship or manage — nothing here configures one —
// so its actual behaviour is whatever the live machine happens to resolve,
// which is what let it silently misbehave (docs/TODO.md: a terminal window
// flashing open and closing). Naming the real, installed viewer explicitly
// removes that guesswork for the one file type this launcher is asked to
// treat specially.
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".bmp": true, ".webp": true, ".tiff": true, ".tif": true,
}

func isImageFile(path string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(path))]
}

// openCommand is the shell command that opens path: imv, explicitly classed,
// for an image; xdg-open for everything else. Both arguments are quoted —
// see shellQuote.
func openCommand(path string) string {
	if isImageFile(path) {
		return "imv -i " + phiosImvAppID + " " + shellQuote(path)
	}
	return "xdg-open " + shellQuote(path)
}

// shellQuote wraps s as one single-quoted POSIX sh word, escaping any single
// quote it contains. The result of this provider's search is a real
// filesystem path under $HOME, which can contain spaces or shell
// metacharacters — Launcher.qml's "exec" action runs Data["command"]
// through `sh -c`, so the path must never be interpolated unquoted.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type FilesProvider struct{}

func (FilesProvider) Name() string { return "file" }

func (p FilesProvider) Query(ctx context.Context, q string) []Result {
	// Two characters minimum: fd across the whole home directory on every
	// single keystroke of a one-letter query is exactly the kind of
	// per-query cost providerTimeout exists to bound, and a one-character
	// file search is rarely useful even when it completes.
	if len(q) < 2 {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	out, available := runCommand(ctx, "fd", "--type", "f", "--", q, home)
	if !available || out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	var results []Result
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		results = append(results, Result{
			ID: "file:" + line, Provider: p.Name(),
			Title: filepath.Base(line), Subtitle: line, Score: 30,
			Action: Action{Kind: ActionExec, Data: map[string]string{"command": openCommand(line)}},
		})
		if len(results) >= filesMaxResults {
			break
		}
	}
	return results
}
