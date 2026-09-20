package external

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Audit is pure given cfg: every fact it needs (which profiles, which home
// directory, what the machine actually has installed) arrives through
// Config, so the same call produces the same Report regardless of which
// machine or process runs it — the property that makes it callable
// identically from `phi pkg audit` and from internal/doctor.
func Audit(ctx context.Context, cfg Config) (Report, error) {
	enum := cfg.Enum
	if enum == nil {
		enum = systemEnumerator{}
	}

	home := cfg.Home
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Report{}, fmt.Errorf("cannot resolve home directory: %w", err)
		}
		home = h
	}

	entries, problems, err := Load(cfg.Root, cfg.Profiles)
	if err != nil {
		return Report{}, err
	}
	for i := range entries {
		entries[i].Status = "ok"
	}

	report := Report{Entries: entries, Problems: problems}

	driftFindings, statusByName, err := auditDrift(ctx, enum, report.Entries)
	if err != nil {
		return Report{}, err
	}
	report.Findings = append(report.Findings, driftFindings...)
	for i, e := range report.Entries {
		if s, ok := statusByName[e.Name]; ok {
			report.Entries[i].Status = s
		}
	}

	leakFindings, err := auditLeaks(enum, home, report.Entries)
	if err != nil {
		return Report{}, err
	}
	report.Findings = append(report.Findings, leakFindings...)

	report.Findings = append(report.Findings, auditIntegrity(home, report.Entries)...)

	roots, fpFindings, err := auditFingerprints(home)
	if err != nil {
		return Report{}, err
	}
	report.Roots = roots
	report.Findings = append(report.Findings, fpFindings...)

	return report, nil
}

// auditDrift enumerates actual state per manager and diffs it against the
// declarations, in both directions. Each tier is matched against its own
// population by the identifier that population actually exposes: a Flatpak
// app id (extracted from Source, since the declared Name is phiOS's own
// short label, not the app's reverse-DNS id), a "<name>.AppImage" file name,
// or the declared Name itself for a ~/.local/opt tree or a container, which
// are named by phiOS in the first place.
func auditDrift(ctx context.Context, enum Enumerator, entries []Entry) (findings []Finding, status map[string]string, err error) {
	status = map[string]string{}

	// T2 — Flatpak.
	flatpakActual, ferr := enum.Flatpak(ctx)
	if ferr != nil {
		return nil, nil, fmt.Errorf("flatpak: %w", ferr)
	}
	declaredFlatpak := map[string]string{} // app id -> declared Name
	for _, e := range entries {
		if e.Tier == "T2" {
			declaredFlatpak[flatpakAppID(e.Source)] = e.Name
		}
	}
	matchedFlatpak := map[string]bool{}
	for _, id := range flatpakActual {
		if name, ok := declaredFlatpak[id]; ok {
			matchedFlatpak[name] = true
		} else {
			findings = append(findings, Finding{Check: "drift", Kind: "undeclared", Name: id,
				Detail: "Flatpak app installed but not declared in any external.txt"})
		}
	}
	for _, e := range entries {
		if e.Tier != "T2" || matchedFlatpak[e.Name] {
			continue
		}
		status[e.Name] = "missing"
		findings = append(findings, Finding{Check: "drift", Kind: "missing", Name: e.Name,
			Detail: fmt.Sprintf("declared Flatpak app %s not installed", flatpakAppID(e.Source))})
	}

	// T4 — AppImages, matched by "<name>.AppImage" case-insensitively (the
	// filesystem this lands on is not guaranteed case-sensitive).
	appImagesActual, aerr := enum.AppImages()
	if aerr != nil {
		return nil, nil, fmt.Errorf("appimages: %w", aerr)
	}
	declaredAppImages := map[string]string{} // lowercased file name -> declared Name
	for _, e := range entries {
		if e.Tier == "T4" {
			declaredAppImages[strings.ToLower(e.Name+".AppImage")] = e.Name
		}
	}
	matchedAppImages := map[string]bool{}
	for _, f := range appImagesActual {
		if name, ok := declaredAppImages[strings.ToLower(f)]; ok {
			matchedAppImages[name] = true
		} else {
			findings = append(findings, Finding{Check: "drift", Kind: "undeclared", Name: f,
				Detail: "AppImage present in ~/Applications but not declared"})
		}
	}
	for _, e := range entries {
		if e.Tier != "T4" || matchedAppImages[e.Name] {
			continue
		}
		status[e.Name] = "missing"
		findings = append(findings, Finding{Check: "drift", Kind: "missing", Name: e.Name,
			Detail: fmt.Sprintf("declared AppImage %s.AppImage not found in ~/Applications", e.Name)})
	}

	// T3 — bubblewrap-contained trees under ~/.local/opt, named by phiOS.
	optActual, operr := enum.Opt()
	if operr != nil {
		return nil, nil, fmt.Errorf("opt: %w", operr)
	}
	declaredOpt := map[string]bool{}
	for _, e := range entries {
		if e.Tier == "T3" {
			declaredOpt[e.Name] = true
		}
	}
	optSet := toSet(optActual)
	for _, d := range optActual {
		if !declaredOpt[d] {
			findings = append(findings, Finding{Check: "drift", Kind: "undeclared", Name: d,
				Detail: "~/.local/opt/" + d + " present but not declared"})
		}
	}
	for _, e := range entries {
		if e.Tier != "T3" || optSet[e.Name] {
			continue
		}
		status[e.Name] = "missing"
		findings = append(findings, Finding{Check: "drift", Kind: "missing", Name: e.Name,
			Detail: "declared tree ~/.local/opt/" + e.Name + " not found"})
	}

	// TC — rootless containers, named by phiOS.
	containersActual, cerr := enum.Containers(ctx)
	if cerr != nil {
		return nil, nil, fmt.Errorf("containers: %w", cerr)
	}
	declaredContainers := map[string]bool{}
	for _, e := range entries {
		if e.Tier == "TC" {
			declaredContainers[e.Name] = true
		}
	}
	containerSet := toSet(containersActual)
	for _, c := range containersActual {
		if !declaredContainers[c] {
			findings = append(findings, Finding{Check: "drift", Kind: "undeclared", Name: c,
				Detail: "rootless container present but not declared"})
		}
	}
	for _, e := range entries {
		if e.Tier != "TC" || containerSet[e.Name] {
			continue
		}
		status[e.Name] = "missing"
		findings = append(findings, Finding{Check: "drift", Kind: "missing", Name: e.Name,
			Detail: "declared container " + e.Name + " not present"})
	}

	return findings, status, nil
}

func toSet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, s := range items {
		out[s] = true
	}
	return out
}

// flatpakAppID extracts the app id from a Source like
// "flathub:io.github.wivrn.wivrn". A Source with no ':' is used verbatim,
// so a differently-shaped remote reference still gets compared as a whole
// rather than silently matching nothing.
func flatpakAppID(source string) string {
	if _, id, ok := strings.Cut(source, ":"); ok {
		return id
	}
	return source
}

// auditLeaks asserts that the paths a language package manager or a stray
// `make install` could have populated are empty, except ~/.local/bin, which
// may hold exactly the installer's own manifest entries plus one per
// declared external.txt name (any tier may legitimately drop a launcher or
// wrapper script there).
func auditLeaks(enum Enumerator, home string, entries []Entry) ([]Finding, error) {
	var findings []Finding

	type leak struct{ path, label string }
	leaks := []leak{
		{filepath.Join(home, ".npm-global"), "~/.npm-global"},
		{filepath.Join(home, ".cargo", "bin"), "~/.cargo/bin"},
		{filepath.Join(home, "go", "bin"), "~/go/bin"},
		{"/usr/local/bin", "/usr/local/bin"},
		{"/usr/local/lib", "/usr/local/lib"},
		{"/usr/local/share", "/usr/local/share"},
	}
	if np, ok := enum.(npmPrefixer); ok {
		if prefix, found := np.NpmPrefix(); found {
			leaks = append(leaks, leak{prefix, "npm prefix (" + prefix + ")"})
		}
	}

	for _, lk := range leaks {
		names, err := enum.ListDir(lk.path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", lk.path, err)
		}
		if len(names) > 0 {
			findings = append(findings, Finding{Check: "leak", Kind: "leak", Name: lk.label,
				Detail: fmt.Sprintf("not empty (%d): %s", len(names), strings.Join(names, ", "))})
		}
	}

	// ~/.local/lib/python*/site-packages: composed from two ListDir calls
	// rather than a real filesystem glob, so the check stays entirely
	// behind the injected Enumerator.
	libDir := filepath.Join(home, ".local", "lib")
	libEntries, err := enum.ListDir(libDir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", libDir, err)
	}
	for _, e := range libEntries {
		matched, _ := filepath.Match("python*", e)
		if !matched {
			continue
		}
		sitePkgs := filepath.Join(libDir, e, "site-packages")
		names, err := enum.ListDir(sitePkgs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", sitePkgs, err)
		}
		if len(names) > 0 {
			findings = append(findings, Finding{Check: "leak", Kind: "leak", Name: "~/.local/lib/" + e + "/site-packages",
				Detail: fmt.Sprintf("not empty (%d): %s", len(names), strings.Join(names, ", "))})
		}
	}

	// ~/.local/bin: the one leak path with an allowlist.
	localBin := filepath.Join(home, ".local", "bin")
	binEntries, err := enum.ListDir(localBin)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", localBin, err)
	}
	if len(binEntries) > 0 {
		allowed := map[string]bool{}
		manifestTargets, merr := enum.Manifest()
		if merr != nil {
			return nil, fmt.Errorf("manifest: %w", merr)
		}
		for _, t := range manifestTargets {
			t = filepath.ToSlash(t)
			if strings.HasPrefix(t, ".local/bin/") {
				allowed[filepath.Base(t)] = true
			}
		}
		for _, e := range entries {
			allowed[e.Name] = true
		}

		var stray []string
		for _, name := range binEntries {
			if !allowed[name] {
				stray = append(stray, name)
			}
		}
		if len(stray) > 0 {
			findings = append(findings, Finding{Check: "leak", Kind: "leak", Name: "~/.local/bin",
				Detail: fmt.Sprintf("not recorded in the installer manifest or external.txt (%d): %s", len(stray), strings.Join(stray, ", "))})
		}
	}

	return findings, nil
}

// auditIntegrity verifies each entry's recorded sha256 against the artifact
// where one can actually be located, and mutates entries[i].Status in
// place. An entry already "missing" is skipped: there is nothing to hash.
// T2 and TC are left untouched (their manager owns integrity, sha256 is
// always "-"); T3 is always marked "unverified", hex sha256 or not, because
// its installed form is an already-extracted tree with no single artifact
// left to re-hash — the recorded checksum describes the fetched source,
// consumed by extraction, so reporting "ok" here would be a checksum this
// package never actually re-checked.
func auditIntegrity(home string, entries []Entry) []Finding {
	var findings []Finding
	for i := range entries {
		if entries[i].Status == "missing" {
			continue
		}
		switch entries[i].Tier {
		case "T4":
			path := filepath.Join(home, "Applications", entries[i].Name+".AppImage")
			sum, err := sha256File(path)
			if err != nil {
				entries[i].Status = "unverified"
				continue
			}
			if sum != entries[i].SHA256 {
				entries[i].Status = "changed"
				findings = append(findings, Finding{Check: "integrity", Kind: "checksum", Name: entries[i].Name,
					Detail: fmt.Sprintf("recorded sha256 %s does not match the artifact's %s", entries[i].SHA256, sum)})
			}
		case "T3":
			entries[i].Status = "unverified"
		}
	}
	return findings
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// auditFingerprints reads the stored baseline for each of the four closed
// roots (never writes it — only Accept re-baselines) and reports one
// "changed" Finding per root whose current fingerprint differs from it. A
// root never yet baselined is not reported as changed: there is no prior
// observation to differ from.
func auditFingerprints(home string) ([]RootState, []Finding, error) {
	store, err := loadFingerprintStore()
	if err != nil {
		return nil, nil, err
	}

	names := []string{"applications", "opt", "flatpak", "local-bin"}
	roots := make([]RootState, 0, len(names))
	var findings []Finding
	for _, name := range names {
		dir, derr := fingerprintRootDir(home, name)
		if derr != nil {
			return nil, nil, derr
		}
		digest, n, ferr := Fingerprint(dir)
		if ferr != nil {
			return nil, nil, ferr
		}
		prev, seen := store[name]
		changed := seen && prev.Digest != digest
		roots = append(roots, RootState{Root: name, Digest: digest, Entries: n, Changed: changed})
		if changed {
			findings = append(findings, Finding{Check: "integrity", Kind: "changed", Name: name,
				Detail: fmt.Sprintf("fingerprint changed since last accept: %d -> %d entries", prev.Entries, n)})
		}
	}
	return roots, findings, nil
}
