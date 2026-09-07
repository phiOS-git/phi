package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"phi/internal/theme"
	"phi/internal/tokens"
	"phi/internal/view"
)

const themeUsage = `usage: phi theme <verb> [arguments]

Verbs:
  render [--variant NAME] TEMPLATE [DESTINATION]
                    render one template against the design tokens; writes to
                    DESTINATION, or standard output when it is omitted
  set VARIANT [--dry-run]
                    render every design/adapters.txt target for VARIANT,
                    reload the ones that changed, and record VARIANT as the
                    active theme
  preview [--variant NAME]
                    render design/preview.tmpl to standard output
  list              list the themed targets design/adapters.txt declares
  check             report the WCAG contrast ratio of every checked token
                    pair on both variants

--variant defaults to the active variant recorded by the last 'phi theme
set', or dark when none was ever recorded.
`

func runTheme(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, themeUsage)
		return 1
	}

	root, err := tokens.Root()
	if err != nil {
		fmt.Fprintf(stderr, "%s: theme: %v\n", progName, err)
		return 1
	}

	switch args[0] {
	case "render":
		return runThemeRender(args[1:], root, stdout, stderr)
	case "set":
		return runThemeSet(args[1:], root, stdout, stderr)
	case "preview":
		return runThemePreview(args[1:], root, stdout, stderr)
	case "list":
		return runThemeList(root, stdout, stderr)
	case "check":
		return runThemeCheck(root, stdout, stderr)
	case "-h", "--help":
		fmt.Fprint(stdout, themeUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "%s: theme: unknown verb %q\n", progName, args[0])
		fmt.Fprint(stderr, themeUsage)
		return 1
	}
}

// parseVariantFlag pulls a leading/anywhere --variant NAME or --variant=NAME
// out of args, mirroring bin/phios-render's own loop, and returns the
// remaining positional arguments.
func parseVariantFlag(args []string, stderr io.Writer) (variant string, rest []string, ok bool) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--variant":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "%s: theme: --variant needs a name\n", progName)
				return "", nil, false
			}
			variant = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--variant="):
			variant = strings.TrimPrefix(args[i], "--variant=")
		default:
			rest = append(rest, args[i])
		}
	}
	return variant, rest, true
}

func runThemeRender(args []string, root string, stdout, stderr io.Writer) int {
	variant, rest, ok := parseVariantFlag(args, stderr)
	if !ok {
		return 1
	}
	if variant == "" {
		variant = theme.CurrentVariant()
	}
	if len(rest) < 1 || len(rest) > 2 {
		fmt.Fprint(stderr, themeUsage)
		return 1
	}
	src, dest := rest[0], ""
	if len(rest) == 2 {
		dest = rest[1]
	}

	tk, err := tokens.Load(root, variant)
	if err != nil {
		fmt.Fprintf(stderr, "%s: theme: %v\n", progName, err)
		return 1
	}
	data, err := os.ReadFile(src)
	if err != nil {
		fmt.Fprintf(stderr, "%s: theme: no such template: %s\n", progName, src)
		return 1
	}
	rendered := theme.Substitute(tk, data)

	if dest == "" {
		stdout.Write(rendered)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintf(stderr, "%s: theme: %v\n", progName, err)
		return 1
	}
	if err := os.WriteFile(dest, rendered, 0o644); err != nil {
		fmt.Fprintf(stderr, "%s: theme: %v\n", progName, err)
		return 1
	}
	return 0
}

func runThemePreview(args []string, root string, stdout, stderr io.Writer) int {
	variant, rest, ok := parseVariantFlag(args, stderr)
	if !ok {
		return 1
	}
	if variant == "" {
		variant = theme.CurrentVariant()
	}
	if len(rest) != 0 {
		fmt.Fprint(stderr, themeUsage)
		return 1
	}

	tk, err := tokens.Load(root, variant)
	if err != nil {
		fmt.Fprintf(stderr, "%s: theme: %v\n", progName, err)
		return 1
	}
	data, err := os.ReadFile(filepath.Join(root, "design", "preview.tmpl"))
	if err != nil {
		fmt.Fprintf(stderr, "%s: theme: %v\n", progName, err)
		return 1
	}
	stdout.Write(theme.Substitute(tk, data))
	return 0
}

func runThemeSet(args []string, root string, stdout, stderr io.Writer) int {
	dryRun := false
	var variant string
	for _, a := range args {
		if a == "--dry-run" {
			dryRun = true
			continue
		}
		if variant != "" {
			fmt.Fprint(stderr, themeUsage)
			return 1
		}
		variant = a
	}
	if variant == "" {
		fmt.Fprint(stderr, themeUsage)
		return 1
	}
	if _, err := tokens.Load(root, variant); err != nil {
		fmt.Fprintf(stderr, "%s: theme: %v\n", progName, err)
		return 1
	}

	result, err := theme.Set(root, variant, dryRun)
	if err != nil {
		fmt.Fprintf(stderr, "%s: theme: %v\n", progName, err)
		return 1
	}
	fmt.Fprint(stdout, view.ThemeSet(result))

	for _, a := range result.Adapters {
		if a.ReloadOutcome == theme.ReloadFailed {
			return 1
		}
	}
	return 0
}

func runThemeList(root string, stdout, stderr io.Writer) int {
	adapters, err := theme.ParseAdapters(root)
	if err != nil {
		fmt.Fprintf(stderr, "%s: theme: %v\n", progName, err)
		return 1
	}
	fmt.Fprint(stdout, view.ThemeList(adapters))
	return 0
}

func runThemeCheck(root string, stdout, stderr io.Writer) int {
	results, err := theme.Check(root)
	if err != nil {
		fmt.Fprintf(stderr, "%s: theme: %v\n", progName, err)
		return 1
	}
	fmt.Fprint(stdout, view.ThemeCheck(results))

	for _, r := range results {
		if !r.Pass {
			return 1
		}
	}
	return 0
}
