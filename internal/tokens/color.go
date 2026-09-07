package tokens

import (
	"fmt"
	"math"
	"strconv"
)

// Contrast returns the WCAG 2.x contrast ratio between two #rrggbb (or
// #rrggbbaa, alpha ignored) colour tokens. This is the exact method tokens.
// dark.sh and tokens.light.sh cite in their own provenance comments ("Ratios
// are WCAG 2.x against bg-0") — not an OKLab proxy: §6.2's 4.5:1 floor is a
// WCAG AA figure and only means what it says under WCAG's own formula.
func Contrast(a, b string) (float64, error) {
	la, err := relativeLuminance(a)
	if err != nil {
		return 0, err
	}
	lb, err := relativeLuminance(b)
	if err != nil {
		return 0, err
	}
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05), nil
}

func relativeLuminance(hex string) (float64, error) {
	r, g, b, err := parseHex(hex)
	if err != nil {
		return 0, err
	}
	return 0.2126*linearize(r) + 0.7152*linearize(g) + 0.0722*linearize(b), nil
}

func linearize(c uint8) float64 {
	cs := float64(c) / 255
	if cs <= 0.03928 {
		return cs / 12.92
	}
	return math.Pow((cs+0.055)/1.055, 2.4)
}

func parseHex(hex string) (r, g, b uint8, err error) {
	if len(hex) != 7 && len(hex) != 9 {
		return 0, 0, 0, fmt.Errorf("not a #rrggbb colour: %q", hex)
	}
	if hex[0] != '#' {
		return 0, 0, 0, fmt.Errorf("not a #rrggbb colour: %q", hex)
	}
	var v [3]uint8
	for i := 0; i < 3; i++ {
		n, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("not a #rrggbb colour: %q", hex)
		}
		v[i] = uint8(n)
	}
	return v[0], v[1], v[2], nil
}
