package view

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"phi/internal/theme"
)

// ThemeCheck renders a `phi theme check` report: every checked pair on both
// variants, in the same plain-text form whether or not stdout is a
// terminal — this is a script-parseable report, not decoration.
func ThemeCheck(results []theme.CheckResult) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)

	variant := ""
	failing := 0
	for _, r := range results {
		if r.Variant != variant {
			if variant != "" {
				fmt.Fprintln(tw)
			}
			variant = r.Variant
			fmt.Fprintf(tw, "%s\n", variant)
		}
		verdict := "ok"
		if !r.Pass {
			verdict = fmt.Sprintf("FAIL (below %.2f:1)", theme.MinContrast)
			failing++
		}
		fmt.Fprintf(tw, "  %s\t%.2f:1\t%s\n", r.Label, r.Ratio, verdict)
	}
	tw.Flush()

	if failing == 0 {
		b.WriteString("\nall pairs clear " + fmt.Sprintf("%.2f:1", theme.MinContrast) + "\n")
	} else {
		b.WriteString(fmt.Sprintf("\n%d pair(s) below %.2f:1\n", failing, theme.MinContrast))
	}
	return b.String()
}

// ThemeList renders `phi theme list`: one row per design/adapters.txt entry.
func ThemeList(adapters []theme.Adapter) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "TEMPLATE\tDESTINATION\tRELOAD\tCLASS\n")
	for _, a := range adapters {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.Template, a.Destination, a.Reload, string(a.Class))
	}
	tw.Flush()
	return b.String()
}

// ThemeSet renders one `phi theme set` run: what changed, what reloaded, and
// the class-C targets that need a restart phi never performs itself.
func ThemeSet(result theme.SetResult) string {
	var b strings.Builder
	label := "variant: " + result.Variant
	if result.DryRun {
		label += " (dry run)"
	}
	b.WriteString(label + "\n\n")

	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	for _, a := range result.Adapters {
		fmt.Fprintf(tw, "  %s\t%s\t[%s]\n", a.State, a.Adapter.Destination, a.Adapter.Class)
	}
	tw.Flush()

	var reloaded, unwired, failed []theme.AdapterResult
	for _, a := range result.Changed() {
		switch a.ReloadOutcome {
		case theme.ReloadRan:
			reloaded = append(reloaded, a)
		case theme.ReloadUnwired:
			unwired = append(unwired, a)
		case theme.ReloadFailed:
			failed = append(failed, a)
		}
	}
	if len(reloaded)+len(unwired)+len(failed) > 0 {
		b.WriteString("\nreload\n")
		for _, a := range reloaded {
			fmt.Fprintf(&b, "  ok       %s\n", a.Adapter.Destination)
		}
		for _, a := range unwired {
			fmt.Fprintf(&b, "  unwired  %s (reload command not filled in yet, design/adapters.txt)\n", a.Adapter.Destination)
		}
		for _, a := range failed {
			fmt.Fprintf(&b, "  FAILED   %s: %v\n", a.Adapter.Destination, a.ReloadError)
		}
	}

	if restart := result.RestartRequired(); len(restart) > 0 {
		b.WriteString("\nrestart required (class C, not restarted automatically)\n")
		for _, a := range restart {
			fmt.Fprintf(&b, "  %s\n", a.Adapter.Destination)
		}
	}

	switch result.PortalPreference {
	case theme.PortalSet:
		b.WriteString("\nportal color-scheme preference: set\n")
	case theme.PortalSetFailed:
		b.WriteString("\nportal color-scheme preference: FAILED (gsettings ran, exited non-zero)\n")
	case theme.PortalNone:
		if !result.DryRun {
			b.WriteString("\nportal color-scheme preference: skipped (gsettings not on PATH)\n")
		}
	}

	return b.String()
}
