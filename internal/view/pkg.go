package view

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"phi/internal/external"
	"phi/internal/pkg"
)

// PkgList renders `phi pkg list`: one row per category (T0, AUR, T4,
// phi-packages), with package names under each.
func PkgList(entries []pkg.Entry) string {
	return pkgReport(entries, false)
}

// PkgCheck renders `phi pkg check`: the same four categories, each entry
// annotated with its available update if pacman -Qu reported one.
func PkgCheck(result pkg.CheckResult) string {
	var b strings.Builder
	if result.Stale {
		b.WriteString("(pacman -Qu unavailable or failed — update column may be stale or empty)\n\n")
	}
	b.WriteString(pkgReport(result.Entries, true))
	return b.String()
}

// PkgState renders `phi pkg state` — one component per line, aligned.
func PkgState(components []pkg.Component) string {
	if len(components) == 0 {
		return "no component versions could be read\n"
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 2, 4, 2, ' ', 0)
	for _, c := range components {
		fmt.Fprintf(tw, "%s\t%s\t(%s)\n", c.Name, c.Version, c.Source)
	}
	tw.Flush()
	return b.String()
}

// PkgManager renders `phi pkg list --manager NAME`.
func PkgManager(l pkg.ManagerListing) string {
	var b strings.Builder
	if !l.Implemented {
		fmt.Fprintf(&b, "%s: %s\n", l.Manager, l.Note)
		return b.String()
	}
	fmt.Fprintf(&b, "%s (%d)\n", l.Manager, len(l.Entries))
	if l.Note != "" {
		fmt.Fprintf(&b, "  %s\n", l.Note)
	}
	if len(l.Entries) == 0 {
		b.WriteString("  none\n")
		return b.String()
	}
	tw := tabwriter.NewWriter(&b, 2, 4, 2, ' ', 0)
	for _, e := range l.Entries {
		if e.Version != "" {
			fmt.Fprintf(tw, "  %s\t%s\n", e.Name, e.Version)
		} else {
			fmt.Fprintf(tw, "  %s\n", e.Name)
		}
	}
	tw.Flush()
	return b.String()
}

// PkgAudit renders `phi pkg audit`: parse problems first (a malformed
// declaration is never silently folded into "no findings"), then every
// declared entry with its audited status, the four fingerprint roots, and
// finally the findings that drove those statuses.
func PkgAudit(report external.Report) string {
	var b strings.Builder

	if len(report.Problems) > 0 {
		fmt.Fprintf(&b, "parse problems (%d):\n", len(report.Problems))
		for _, p := range report.Problems {
			fmt.Fprintf(&b, "  %s:%d: %s\n", p.File, p.Line, p.Detail)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "declared (%d):\n", len(report.Entries))
	if len(report.Entries) == 0 {
		b.WriteString("  none\n")
	} else {
		tw := tabwriter.NewWriter(&b, 2, 4, 2, ' ', 0)
		for _, e := range report.Entries {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", e.Name, e.Tier, e.Status, e.Profile)
		}
		tw.Flush()
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "fingerprints (%d roots):\n", len(report.Roots))
	for _, r := range report.Roots {
		state := "unchanged"
		if r.Changed {
			state = "CHANGED"
		}
		fmt.Fprintf(&b, "  %-12s %-9s %4d entries  %s\n", r.Root, state, r.Entries, r.Digest)
	}
	b.WriteString("\n")

	if len(report.Findings) == 0 {
		b.WriteString("no findings\n")
		return b.String()
	}
	fmt.Fprintf(&b, "findings (%d):\n", len(report.Findings))
	for _, f := range report.Findings {
		fmt.Fprintf(&b, "  [%s/%s] %s: %s\n", f.Check, f.Kind, f.Name, f.Detail)
	}
	return b.String()
}

func pkgReport(entries []pkg.Entry, showUpdates bool) string {
	var b strings.Builder
	categories := []pkg.Category{pkg.CategoryT0, pkg.CategoryAUR, pkg.CategoryT4, pkg.CategoryPhiPackages}

	for _, cat := range categories {
		var rows []pkg.Entry
		for _, e := range entries {
			if e.Category == cat {
				rows = append(rows, e)
			}
		}

		fmt.Fprintf(&b, "%s (%d)\n", cat, len(rows))

		if cat == pkg.CategoryT4 {
			b.WriteString("  cannot be enumerated: T4 packages are installed without pacman ever\n")
			b.WriteString("  recording them, so no pacman query can see them. Policy forbids this tier\n")
			b.WriteString("  entirely for now, so this row's true state is \"should be none\", not\n")
			b.WriteString("  \"confirmed none\".\n\n")
			continue
		}

		if cat == pkg.CategoryAUR && len(rows) == 0 {
			b.WriteString("  empty — no AUR packages, exactly as expected\n\n")
			continue
		}
		if cat == pkg.CategoryAUR && len(rows) > 0 {
			b.WriteString("  POLICY VIOLATION: AUR packages are forbidden, and pacman cannot tell\n")
			b.WriteString("  these apart from a manually pacman -U'd build either — both land here.\n")
		}

		tw := tabwriter.NewWriter(&b, 2, 4, 2, ' ', 0)
		for _, e := range rows {
			if showUpdates && e.Update != "" {
				fmt.Fprintf(tw, "  %s\t%s\t-> %s\n", e.Name, e.Version, e.Update)
			} else {
				fmt.Fprintf(tw, "  %s\t%s\n", e.Name, e.Version)
			}
		}
		tw.Flush()
		b.WriteString("\n")
	}

	return b.String()
}
