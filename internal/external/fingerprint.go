package external

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// validRoots is the closed set of fingerprintable populations — the same
// closed-key-set discipline internal/state's Keys uses, so the TSV store's
// key column is well defined and a typo in a root name can never silently
// create a new row instead of failing.
var validRoots = map[string]bool{
	"applications": true, // ~/Applications — T4 AppImages
	"opt":          true, // ~/.local/opt — T3 unpacked trees
	"flatpak":      true, // the Flatpak export tree — T2 apps
	"local-bin":    true, // ~/.local/bin — the one leak path with an allowlist
}

// Fingerprint hashes (relative path, size, mode, mtime) for every entry
// under dir into one stable digest. It deliberately hashes metadata, not
// file content: reading every byte under, say, an AppImage collection on
// every doctor run would make this check itself the expensive part of
// doctor. A missing dir fingerprints as ("", 0, nil), not an error — an
// empty or not-yet-created root (no AppImages installed yet) is a normal,
// common state, not a fault.
func Fingerprint(dir string) (digest string, n int, err error) {
	info, statErr := os.Stat(dir)
	if os.IsNotExist(statErr) {
		return "", 0, nil
	}
	if statErr != nil {
		return "", 0, statErr
	}
	if !info.IsDir() {
		return "", 0, fmt.Errorf("%s is not a directory", dir)
	}

	type stamp struct {
		rel   string
		size  int64
		mode  os.FileMode
		mtime int64
	}
	var stamps []stamp

	walkErr := filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		stamps = append(stamps, stamp{rel: rel, size: fi.Size(), mode: fi.Mode(), mtime: fi.ModTime().UnixNano()})
		return nil
	})
	if walkErr != nil {
		return "", 0, walkErr
	}

	sort.Slice(stamps, func(i, j int) bool { return stamps[i].rel < stamps[j].rel })

	h := sha256.New()
	for _, s := range stamps {
		fmt.Fprintf(h, "%s\t%d\t%s\t%d\n", s.rel, s.size, s.mode, s.mtime)
	}
	return hex.EncodeToString(h.Sum(nil)), len(stamps), nil
}

// fingerprintRootDir resolves one of the four closed root names to the
// real directory it fingerprints, given a home directory. Shared by Audit
// (which is given home through Config, to stay pure) and Accept (which
// resolves the real os.UserHomeDir() itself, the one place in this package
// where re-baselining is allowed to touch the live system).
func fingerprintRootDir(home, name string) (string, error) {
	switch name {
	case "applications":
		return filepath.Join(home, "Applications"), nil
	case "opt":
		return filepath.Join(home, ".local", "opt"), nil
	case "flatpak":
		// Not the whole ~/.local/share/flatpak tree: that churns on every
		// runtime update and per-app cache write, which would make the
		// fingerprint noise rather than signal (the plan's own risk #6).
		// The exports directory changes exactly when the declared app
		// population changes, which is the thing worth catching here.
		return filepath.Join(home, ".local", "share", "flatpak", "exports", "share", "applications"), nil
	case "local-bin":
		return filepath.Join(home, ".local", "bin"), nil
	default:
		return "", fmt.Errorf("unknown fingerprint root %q (must be one of applications, opt, flatpak, local-bin)", name)
	}
}

// fingerprintStorePath is $XDG_STATE_HOME/phios/external-fingerprints — not
// under phi's own $XDG_STATE_HOME/phi, and not a phi state.Keys entry:
// state's key set is closed and flat, one value per key, which is the
// wrong shape for a per-root record with three columns.
func fingerprintStorePath() (string, error) {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "phios", "external-fingerprints"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "phios", "external-fingerprints"), nil
}

func loadFingerprintStore() (map[string]RootState, error) {
	path, err := fingerprintStorePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]RootState{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]RootState{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			continue
		}
		n, _ := strconv.Atoi(fields[2])
		out[fields[0]] = RootState{Root: fields[0], Digest: fields[1], Entries: n}
	}
	return out, nil
}

// writeFingerprintStore writes the TSV to a .new temporary and renames it
// over the target, so a run interrupted mid-write leaves the previous store
// intact rather than a truncated one — the same atomicity bin/lib/
// manifest.sh's phios_manifest_write uses for the installer's own state file.
func writeFingerprintStore(store map[string]RootState) error {
	path, err := fingerprintStorePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	names := make([]string, 0, len(store))
	for k := range store {
		names = append(names, k)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("# phios external-fingerprints — machine state, not configuration.\n")
	b.WriteString("# root\tdigest\tentries\n")
	for _, k := range names {
		rs := store[k]
		fmt.Fprintf(&b, "%s\t%s\t%d\n", rs.Root, rs.Digest, rs.Entries)
	}

	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Accept re-baselines one root after a legitimate change (a WiVRn update,
// a new AppImage), recording its current fingerprint as the new baseline
// so the next Audit stops reporting it as changed.
func Accept(rootName string) error {
	if !validRoots[rootName] {
		return fmt.Errorf("unknown fingerprint root %q (must be one of applications, opt, flatpak, local-bin)", rootName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir, err := fingerprintRootDir(home, rootName)
	if err != nil {
		return err
	}
	digest, n, err := Fingerprint(dir)
	if err != nil {
		return err
	}
	store, err := loadFingerprintStore()
	if err != nil {
		return err
	}
	store[rootName] = RootState{Root: rootName, Digest: digest, Entries: n}
	return writeFingerprintStore(store)
}
