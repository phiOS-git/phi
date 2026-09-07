// Package tokens reads phiOS's design tokens (master plan §6.1) and locates
// the phios-dotfiles checkout they live in. It has no knowledge of templates
// or destinations — internal/theme owns those.
package tokens

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Root finds the phios-dotfiles checkout. Every installation procedure in
// docs/ clones it to the same place (mini-procedura-base.md, razer-procedura-
// completata.md, phios-procedura-base-2.md all agree), so that is the
// default; PHI_DOTFILES overrides it for a non-standard layout. phi is
// installed system-wide by pacman (S-11), decoupled from any one checkout,
// so it has to be told — or guess — where the repository is.
func Root() (string, error) {
	if v := os.Getenv("PHI_DOTFILES"); v != "" {
		if info, err := os.Stat(v); err == nil && info.IsDir() {
			return v, nil
		}
		return "", fmt.Errorf("PHI_DOTFILES=%s is not a directory", v)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate the phios-dotfiles checkout: %w", err)
	}
	candidate := filepath.Join(home, "phios-dotfiles")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate, nil
	}
	return "", fmt.Errorf("cannot locate the phios-dotfiles checkout: %s does not exist (set PHI_DOTFILES to override)", candidate)
}

// tokenLine matches a design-token definition: KEY='value', optionally
// followed by whitespace and a trailing '#' comment. The value is captured
// verbatim between the single quotes, so a value that itself starts with '#'
// (PHI_OVERLAY_SCRIM) is never mistaken for a comment — design/tokens.
// common.sh's format contract forbids escaping or embedded quotes, so this
// is the whole grammar.
var tokenLine = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)='([^']*)'`)

// Load reads design/tokens.common.sh and design/tokens.<variant>.sh under
// root and returns every PHI_* token they define. Errors mirror bin/lib/
// tokens.sh's phios_tokens_require, which this replaces.
func Load(root, variant string) (map[string]string, error) {
	common := filepath.Join(root, "design", "tokens.common.sh")
	if _, err := os.Stat(common); err != nil {
		return nil, fmt.Errorf("design/tokens.common.sh is missing; templates cannot be rendered (S-02)")
	}
	variantFile := filepath.Join(root, "design", "tokens."+variant+".sh")
	if _, err := os.Stat(variantFile); err != nil {
		return nil, fmt.Errorf("design/tokens.%s.sh is missing; unknown theme variant: %s", variant, variant)
	}

	out := make(map[string]string)
	for _, f := range []string{common, variantFile} {
		if err := parseInto(f, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func parseInto(path string, out map[string]string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		m := tokenLine.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		out[m[1]] = m[2]
	}
	return scanner.Err()
}
