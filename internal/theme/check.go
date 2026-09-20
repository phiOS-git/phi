package theme

import (
	"phi/internal/tokens"
)

// MinContrast is the WCAG AA floor for normal text, binding on both
// variants.
const MinContrast = 4.5

// checkPairs are the token pairs checked for WCAG AA contrast against bg-0:
// fg-0/1/2 (primary/secondary text) and every Tier 1/2 role (accent, error,
// warn, success, info). fg-3 is excluded by design — it is non-text
// (dividers, disabled marks) and is not held to the 4.5:1 minimum. Labels
// are the design token names.
var checkPairs = []struct{ token, label string }{
	{"PHI_FG_0", "fg-0"}, {"PHI_FG_1", "fg-1"}, {"PHI_FG_2", "fg-2"},
	{"PHI_ACCENT", "accent"}, {"PHI_ERROR", "error"}, {"PHI_WARN", "warn"},
	{"PHI_SUCCESS", "success"}, {"PHI_INFO", "info"},
}

// Variants are the two permanent theme variants: independent, each required
// to meet WCAG AA contrast (4.5:1) on all checked token pairs.
var Variants = []string{"dark", "light"}

// CheckResult is one token-pair measurement.
type CheckResult struct {
	Variant string
	Token   string
	Label   string
	Ratio   float64
	Pass    bool
}

// Check computes the WCAG contrast ratio of every checkPairs token against
// bg-0 on both variants. Results are programmatically verifiable, not
// based on visual inspection.
func Check(root string) ([]CheckResult, error) {
	var out []CheckResult
	for _, variant := range Variants {
		tk, err := tokens.Load(root, variant)
		if err != nil {
			return nil, err
		}
		bg := tk["PHI_BG_0"]
		for _, pair := range checkPairs {
			ratio, err := tokens.Contrast(tk[pair.token], bg)
			if err != nil {
				return nil, err
			}
			out = append(out, CheckResult{
				Variant: variant,
				Token:   pair.token,
				Label:   pair.label,
				Ratio:   ratio,
				Pass:    ratio >= MinContrast,
			})
		}
	}
	return out, nil
}
