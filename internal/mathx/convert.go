package mathx

import (
	"regexp"
	"strings"
)

// convert.go turns a free-text unit-conversion query into a Report. It is
// deliberately separate from Evaluate: the launcher's converter provider
// tries this first, and only if it does not match does the text go to the
// calculator.
//
// Accepted shapes (case-insensitive, connector is to|in|into|as|->|→):
//
//	100 km to m          100km to m           100 kilometres in miles
//	5ft to cm            -40 C to F           1e6 bytes to MB
//	km to mi             (implicit value 1)
//	20 c in f            (the "in" connector, disambiguated from inches)
//
// It does NOT handle currency (a separate provider) or compound units
// ("5 ft 3 in") — those return ok == false.

var (
	reConnector   = regexp.MustCompile(`(?i)\s+(?:to|into|as)\s+|\s*(?:->|=>|→)\s*`)
	reLeftSide    = regexp.MustCompile(`^\s*([+-]?(?:[0-9][0-9,_ ]*)?(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)\s*(.+?)\s*$`)
	reConvertLead = regexp.MustCompile(`(?i)^\s*(?:convert|how\s+(?:much|many)\s+(?:is\s+)?)\s+`)
	reBareQty     = regexp.MustCompile(`^\s*([+-]?[0-9][0-9,_ ]*(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)\s*([a-zA-Zµμ°"'/²³^]+[a-zA-Z0-9µμ°"'/²³^ ]*?)\s*$`)
)

// ParseConversion returns a Report for a unit conversion, or ok == false
// when text is not one.
func ParseConversion(text string) (*Report, bool) {
	raw := strings.TrimSpace(text)
	raw = reConvertLead.ReplaceAllString(raw, "")
	if raw == "" {
		return nil, false
	}

	left, right, ok := splitConnector(raw)
	if !ok {
		// No connector: a bare "5 km" / "72 °F" is still worth answering —
		// show it across the other common units of its dimension. Requires
		// an explicit leading number so a plain "km" or a stray word does
		// not trigger it.
		if rep, ok := bareQuantity(text, raw); ok {
			return rep, true
		}
		return nil, false
	}

	value, fromUnit, ok := splitQuantity(left)
	if !ok {
		return nil, false
	}
	toUnit := strings.TrimSpace(right)
	if toUnit == "" {
		return nil, false
	}

	fromDef, ok1 := lookupUnit(fromUnit)
	toDef, ok2 := lookupUnit(toUnit)
	if !ok1 || !ok2 {
		return nil, false
	}
	if fromDef.dim != toDef.dim {
		// A real unit pair but incompatible dimensions: still worth a
		// clear message rather than silence.
		return &Report{
			Input:    text,
			Kind:     "convert",
			Headline: "cannot convert " + fromDef.canon + " to " + toDef.canon,
			Detail:   string(fromDef.dim) + " → " + string(toDef.dim),
			CopyText: "",
		}, true
	}

	out, dim, fromCanon, toCanon, err := Convert(value, fromUnit, toUnit)
	if err != nil {
		return nil, false
	}

	headline := formatFloat(out) + " " + toCanon
	r := &Report{
		Input:    text,
		Kind:     "convert",
		Headline: headline,
		Detail:   formatFloat(value) + " " + fromCanon + "  =  " + formatFloat(out) + " " + toCanon,
		CopyText: formatFloat(out),
		Exact:    true,
	}

	// A small table of the same quantity in other common units.
	for _, u := range CommonTargets(dim, fromUnit, toUnit) {
		if v, _, _, uc, e := Convert(value, fromUnit, u); e == nil {
			r.Table = append(r.Table, Row{Label: uc, Value: formatFloat(v)})
		}
	}
	return r, true
}

// bareQuantity answers a "<number> <unit>" query with no target by
// expanding it into the dimension's common units.
func bareQuantity(orig, raw string) (*Report, bool) {
	m := reBareQty.FindStringSubmatch(raw)
	if m == nil {
		return nil, false
	}
	numStr := strings.Map(func(r rune) rune {
		if r == ',' || r == '_' || r == ' ' {
			return -1
		}
		return r
	}, m[1])
	value, err := parseFloatLoose(numStr)
	if err != nil {
		return nil, false
	}
	unit := strings.TrimSpace(m[2])
	def, ok := lookupUnit(unit)
	if !ok {
		return nil, false
	}
	targets := CommonTargets(def.dim, unit)
	if len(targets) == 0 {
		return nil, false
	}
	r := &Report{
		Input:    orig,
		Kind:     "convert",
		Headline: formatFloat(value) + " " + def.canon,
		Detail:   string(def.dim),
		CopyText: formatFloat(value) + " " + def.canon,
		Exact:    true,
	}
	for _, u := range targets {
		if v, _, _, uc, e := Convert(value, unit, u); e == nil {
			r.Table = append(r.Table, Row{Label: uc, Value: formatFloat(v)})
		}
	}
	return r, true
}

// splitConnector splits on the first connector keyword. "to/into/as/->"
// are tried first; a bare "in" is accepted only when both sides then
// resolve to real units (so "5 in to cm" keeps "in" as inches).
func splitConnector(s string) (left, right string, ok bool) {
	if loc := reConnector.FindStringIndex(s); loc != nil {
		return s[:loc[0]], s[loc[1]:], true
	}
	// Try every " in " split, left to right, and accept the first where
	// both halves look like unit expressions.
	lower := strings.ToLower(s)
	for i := 0; i+4 <= len(lower); i++ {
		if lower[i:i+4] == " in " {
			l, r := s[:i], s[i+4:]
			if _, u, okq := splitQuantity(l); okq {
				if _, ok1 := lookupUnit(u); ok1 {
					if _, ok2 := lookupUnit(strings.TrimSpace(r)); ok2 {
						return l, r, true
					}
				}
			}
		}
	}
	return "", "", false
}

// splitQuantity separates a leading number (possibly glued to the unit,
// possibly absent → 1) from the unit text.
func splitQuantity(s string) (value float64, unit string, ok bool) {
	m := reLeftSide.FindStringSubmatch(s)
	if m == nil {
		return 0, "", false
	}
	numStr := strings.Map(func(r rune) rune {
		if r == ',' || r == '_' || r == ' ' {
			return -1
		}
		return r
	}, m[1])
	unit = strings.TrimSpace(m[2])
	if unit == "" {
		return 0, "", false
	}
	if numStr == "" || numStr == "+" || numStr == "-" {
		return 1, unit, true
	}
	v, err := parseFloatLoose(numStr)
	if err != nil {
		return 0, "", false
	}
	return v, unit, true
}
