package external

import "path/filepath"

// ResolveProfiles reads hosts/<host>.txt exactly as internal/doctor's
// declaredProfiles does: one profile name per line, comments and blank
// lines dropped. A host with no such file declares no profiles, which
// Load then simply unions to nothing — not an error, since doctor's own
// checks treat an undeclared host the same way.
func ResolveProfiles(root, host string) ([]string, error) {
	return readList(filepath.Join(root, "hosts", host+".txt"))
}

// Load unions every profile's external.txt, in the order profiles are
// given. A later profile's entry for a given Name overrides an earlier
// one entirely (not merged field by field) — the same override rule
// bin/phios-install already applies when two profiles ship the same file,
// kept as one idiom rather than introducing a second one here.
func Load(root string, profiles []string) ([]Entry, []Problem, error) {
	byName := make(map[string]Entry)
	var order []string
	var problems []Problem

	for _, profile := range profiles {
		path := filepath.Join(root, "profiles", profile, "external.txt")
		entries, probs, err := parseExternalFile(path, profile)
		if err != nil {
			return nil, nil, err
		}
		problems = append(problems, probs...)
		for _, e := range entries {
			if _, exists := byName[e.Name]; !exists {
				order = append(order, e.Name)
			}
			byName[e.Name] = e
		}
	}

	out := make([]Entry, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out, problems, nil
}
