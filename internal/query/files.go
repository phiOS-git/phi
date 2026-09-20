package query

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// FilesProvider: file search backed by `fd` (live search, capped in Go).
const filesMaxResults = 8

// imageExtensions: files opened in phi-shell's native floating window
// (not via xdg-open, which has mimeapps.list uncertainties).
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".bmp": true, ".webp": true, ".tiff": true, ".tif": true,
}

func isImageFile(path string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(path))]
}

// openCommand is the shell command that opens path: phi-shell's own IPC
// target for an image (the same `qs -p ~/.config/quickshell/phi ipc call
// <target> <fn> ...` shape this codebase already uses elsewhere, e.g.
// internal/query/timer.go's "timer add"/"timer addAlarm" calls); xdg-open
// for everything else. Both arguments are quoted — see shellQuote.
func openCommand(path string) string {
	if isImageFile(path) {
		return "qs -p ~/.config/quickshell/phi ipc call image open " + shellQuote(path)
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
	// runner-bar prefix feature: "file <name>" searches for
	// exactly <name> — without this, fd would search for a file literally
	// named "file <name>", almost never a real match.
	if len(q) >= 5 && strings.EqualFold(q[:5], "file ") {
		q = strings.TrimSpace(q[5:])
	}
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
