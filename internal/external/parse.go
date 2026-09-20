package external

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// namePattern is deliberately strict: name becomes a generated filename
// (<name>.AppImage) and a ~/.local/bin entry, so an unvalidated value read
// from repository content would be a path-traversal vector, not just an
// untidy one.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// hex64 matches a lowercase sha256 hex digest, the only checksum shape this
// format accepts (matching phios_sha256's sha256sum output, lowercased).
var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validTiers is the set external.txt may declare. T0 and T1 are handled as
// a specific, named error rather than falling into the generic "unknown
// tier" case, because the mistake of writing one there is exactly the
// mistake this format exists to catch: those two tiers are declared in
// packages.txt, where pacman already tracks, installs and removes them.
var validTiers = map[string]bool{"TC": true, "T2": true, "T3": true, "T4": true}

// readList reads a phios-dotfiles list file: one entry per line, truncated
// at the first '#' and trimmed. Mirrors bin/lib/common.sh's phios_read_list
// (and internal/doctor's own private copy of the same logic) byte for byte:
// '#' truncates from its first occurrence anywhere on the line, then both
// ends are trimmed, and a missing file is an empty list, not an error.
func readList(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

// parseExternalFile reads one profile's external.txt, applying the same
// comment/whitespace rule as readList line by line (kept separate from
// readList because a Problem needs the original line number, which a flat
// []string of already-cleaned lines has thrown away). A missing file is an
// empty declaration, not an error: most profiles declare no external
// software at all.
func parseExternalFile(path, profile string) (entries []Entry, problems []Problem, err error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	for i, raw := range strings.Split(string(data), "\n") {
		line := raw
		if idx := strings.IndexByte(line, '#'); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		entry, verr := parseEntry(line)
		if verr != nil {
			problems = append(problems, Problem{File: path, Line: i + 1, Detail: verr.Error()})
			continue
		}
		entry.Profile = profile
		entries = append(entries, entry)
	}
	return entries, problems, nil
}

// parseEntry validates one already comment-stripped, trimmed line against
// the frozen "name | tier | source | ref | sha256 | reason" format.
func parseEntry(line string) (Entry, error) {
	fields := strings.Split(line, "|")
	if len(fields) != 6 {
		return Entry{}, fmt.Errorf("expected 6 '|'-separated fields (name | tier | source | ref | sha256 | reason), got %d", len(fields))
	}
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	name, tier, source, ref, sha, reason := fields[0], fields[1], fields[2], fields[3], fields[4], fields[5]

	if !namePattern.MatchString(name) {
		return Entry{}, fmt.Errorf("invalid name %q: must match %s", name, namePattern.String())
	}

	if tier == "T0" || tier == "T1" {
		return Entry{}, fmt.Errorf("tier %s belongs in packages.txt, not external.txt: a T0/T1 package is already tracked by pacman", tier)
	}
	if !validTiers[tier] {
		return Entry{}, fmt.Errorf("invalid tier %q: must be one of TC, T2, T3, T4", tier)
	}

	if source == "" {
		return Entry{}, fmt.Errorf("source must not be empty")
	}

	if ref == "" {
		return Entry{}, fmt.Errorf("ref must not be empty")
	}
	if ref == "latest" {
		return Entry{}, fmt.Errorf(`ref must not be "latest": a declaration is pinned to an exact version, never a moving tag`)
	}
	if tier == "TC" && !strings.HasPrefix(ref, "sha256:") {
		return Entry{}, fmt.Errorf("tier TC must pin ref by image digest (sha256:...), got %q", ref)
	}

	if err := validateSHA256(tier, sha); err != nil {
		return Entry{}, err
	}

	if reason == "" {
		return Entry{}, fmt.Errorf("reason must not be empty")
	}

	return Entry{Name: name, Tier: tier, Source: source, Ref: ref, SHA256: sha, Reason: reason}, nil
}

// validateSHA256 enforces the per-tier checksum shape: mandatory for T4
// (the tier with no manager-provided integrity at all), optional for T3
// (contained, but the fetched artifact can still be pinned), and forbidden
// for T2 and TC — Flatpak and a container digest already carry their own
// integrity, so a second checksum here would just be an unverifiable claim.
func validateSHA256(tier, sha string) error {
	switch tier {
	case "T4":
		if !hex64.MatchString(sha) {
			return fmt.Errorf("tier T4 requires a 64-character lowercase hex sha256, got %q", sha)
		}
	case "T3":
		if sha != "-" && !hex64.MatchString(sha) {
			return fmt.Errorf("tier T3 sha256 must be \"-\" or a 64-character lowercase hex sha256, got %q", sha)
		}
	case "T2", "TC":
		if sha != "-" {
			return fmt.Errorf("tier %s sha256 must be \"-\" (the manager provides its own integrity), got %q", tier, sha)
		}
	}
	return nil
}
