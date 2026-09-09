package agent

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Path guard-rails, phios-agente-delta.md §3.4. These are a SELECTOR
// guard-rail, never the security boundary — the boundary is the from-empty
// mount namespace (ADR 087). Even a bypassed blocklist leaves A2 with only the
// one directory it was given and A1 with only the project perimeter.
//
// The same rules are mirrored in phios-dotfiles `phi-agent-contain` (bash):
// this file and that script must agree. Keep both in sync.

// alwaysDenyDirs are refused for BOTH the A1 folder-of-interest (read-only) and
// the A2 working directory (read-write): secrets, keyrings, browser profiles,
// the agent's own config, the runtime socket dir.
func alwaysDenyDirs() []string {
	home, _ := os.UserHomeDir()
	rt := os.Getenv("XDG_RUNTIME_DIR")
	d := []string{
		"/etc", "/root", "/proc", "/sys", "/dev", "/boot", "/efi",
	}
	if home != "" {
		for _, rel := range []string{
			".ssh", ".gnupg", ".password-store", ".config/phi-agent",
			".local/share/keyrings", ".local/share/phi-agent",
			".mozilla", ".config/BraveSoftware", ".config/google-chrome",
			".config/chromium", ".thunderbird", ".config/keepassxc",
		} {
			d = append(d, filepath.Join(home, rel))
		}
	}
	if rt != "" {
		d = append(d, rt)
	}
	return d
}

// codeWriteDenySubtrees additionally refuses, for the A2 read-write working
// directory only, the roots that must never be opened for autonomous edits —
// these block the path and everything under it.
func codeWriteDenySubtrees() []string {
	return []string{"/usr", "/var", "/opt", "/srv", "/nix", "/etc"}
}

// codeWriteDenyExact refuses these paths only when they are the target
// exactly (a subdirectory is fine): $HOME itself, the filesystem root.
func codeWriteDenyExact() []string {
	home, _ := os.UserHomeDir()
	d := []string{"/"}
	if home != "" {
		d = append(d, home)
	}
	return d
}

// userBlocklistPath is the settings-editable list.
func userBlocklistPath() (string, error) {
	c, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "phi-agent", "code-blocklist"), nil
}

// readUserBlocklist returns the glob patterns from the user's blocklist file
// (one per line, '#' comments, blank lines ignored). Missing file → no rules.
func readUserBlocklist() ([]string, error) {
	p, err := userBlocklistPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "~") {
			if home, e := os.UserHomeDir(); e == nil {
				line = home + line[1:]
			}
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// isUnderOrEqual reports whether path is dir or a subdirectory of dir.
func isUnderOrEqual(path, dir string) bool {
	path = filepath.Clean(path)
	dir = filepath.Clean(dir)
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

// matchGlob checks a path against a glob that may itself denote a directory
// subtree: "foo/*" and "foo" both match "foo/bar".
func matchGlob(path, pattern string) bool {
	path = filepath.Clean(path)
	pattern = strings.TrimRight(filepath.Clean(pattern), "/*")
	if ok, _ := filepath.Match(pattern, path); ok {
		return true
	}
	return isUnderOrEqual(path, pattern)
}

// resolvePath cleans, absolutises, and resolves symlinks where possible, so a
// deny entry and the candidate are compared in the same form (/etc vs
// /private/etc, $HOME vs its real path, …).
func resolvePath(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = home + p[1:]
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func checkDenied(abs string, subtreeDeny, exactDeny []string) error {
	abs = resolvePath(abs)
	for _, d := range subtreeDeny {
		if isUnderOrEqual(abs, resolvePath(d)) {
			return fmt.Errorf("%s is inside a blocked path (%s)", abs, d)
		}
	}
	for _, d := range exactDeny {
		if abs == resolvePath(d) {
			return fmt.Errorf("%s cannot be opened directly", abs)
		}
	}
	patterns, err := readUserBlocklist()
	if err != nil {
		return err
	}
	for _, pat := range patterns {
		if matchGlob(abs, pat) || matchGlob(abs, resolvePath(pat)) {
			return fmt.Errorf("%s matches a blocklist entry (%s)", abs, pat)
		}
	}
	return nil
}

// ValidateFolderOfInterest checks a path is safe to mount READ-ONLY into A1's
// per-project perimeter (§4.3.1). The path need not exist yet at validation
// time from SaveProjectMeta; when it does, it must be a directory.
func ValidateFolderOfInterest(path string) error {
	abs := path
	if r, err := resolveDir(path); err == nil {
		abs = r
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		abs = filepath.Clean(path)
	}
	return checkDenied(abs, alwaysDenyDirs(), nil)
}

// ValidateCodeDir checks a path is safe to mount READ-WRITE as A2's single
// working directory (§4.4 revised). The path must exist and be a directory.
func ValidateCodeDir(path string) (string, error) {
	abs, err := resolveDir(path)
	if err != nil {
		return "", err
	}
	subtree := append(alwaysDenyDirs(), codeWriteDenySubtrees()...)
	if err := checkDenied(abs, subtree, codeWriteDenyExact()); err != nil {
		return "", err
	}
	return abs, nil
}
