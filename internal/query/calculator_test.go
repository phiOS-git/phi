package query

import (
	"math"
	"testing"
)

func TestEvalExprArithmetic(t *testing.T) {
	cases := []struct {
		expr string
		want float64
	}{
		{"2 + 2", 4},
		{"2+2*3", 8},    // precedence: * before +
		{"(2+2)*3", 12}, // parentheses override precedence
		{"10 / 4", 2.5},
		{"2 ^ 10", 1024},
		{"2 ^ 3 ^ 2", 512}, // right-associative: 2^(3^2), not (2^3)^2
		{"-3 + 5", 2},
		{"-(3 + 5)", -8},
		{"10 % 3", 1},
		{"3.5 * 2", 7},
		{"  4   +   4  ", 8}, // whitespace-tolerant
	}
	for _, c := range cases {
		got, err := evalExpr(c.expr)
		if err != nil {
			t.Errorf("evalExpr(%q) error: %v", c.expr, err)
			continue
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("evalExpr(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestEvalExprErrors(t *testing.T) {
	cases := []string{"", "2 +", "(2 + 3", "2 / 0", "abc", "2 3"}
	for _, expr := range cases {
		if _, err := evalExpr(expr); err == nil {
			t.Errorf("evalExpr(%q) expected an error, got none", expr)
		}
	}
}

func TestParseConversion(t *testing.T) {
	c, ok := parseConversion("10 km to mi")
	if !ok {
		t.Fatal("expected a match")
	}
	if c.value != 10 || c.fromUnit != "km" || c.toUnit != "mi" {
		t.Errorf("parseConversion = %+v", c)
	}

	if _, ok := parseConversion("2 + 2"); ok {
		t.Error("plain arithmetic must not be read as a conversion")
	}
	if _, ok := parseConversion("10 km toward mi"); ok {
		t.Error("only 'to'/'in' should be accepted as the connector")
	}
	if _, ok := parseConversion("10 banana to mi"); ok {
		t.Error("unknown unit must not match")
	}
}

func TestConvertUnitLength(t *testing.T) {
	got, err := convertUnit(conversion{value: 1, fromUnit: "km", toUnit: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-1000) > 1e-9 {
		t.Errorf("1km in m = %v, want 1000", got)
	}
}

func TestConvertUnitMass(t *testing.T) {
	got, err := convertUnit(conversion{value: 1, fromUnit: "kg", toUnit: "lb"})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-2.2046226218) > 1e-6 {
		t.Errorf("1kg in lb = %v, want ~2.2046", got)
	}
}

func TestConvertTemperature(t *testing.T) {
	cases := []struct {
		v        float64
		from, to string
		want     float64
	}{
		{0, "c", "f", 32},
		{100, "c", "f", 212},
		{32, "f", "c", 0},
		{0, "c", "k", 273.15},
	}
	for _, c := range cases {
		got, err := convertTemperature(c.v, c.from, c.to)
		if err != nil {
			t.Errorf("convertTemperature(%v, %q, %q): %v", c.v, c.from, c.to, err)
			continue
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("convertTemperature(%v, %q, %q) = %v, want %v", c.v, c.from, c.to, got, c.want)
		}
	}
}

func TestConvertUnitRejectsMixedGroups(t *testing.T) {
	if _, err := convertUnit(conversion{value: 1, fromUnit: "kg", toUnit: "m"}); err == nil {
		t.Error("expected an error converting mass to length")
	}
	if _, err := convertUnit(conversion{value: 1, fromUnit: "c", toUnit: "kg"}); err == nil {
		t.Error("expected an error converting temperature to mass")
	}
}

func TestCalculatorProviderQuery(t *testing.T) {
	p := CalculatorProvider{}

	results := p.Query(nil, "2 + 2")
	if len(results) != 1 || results[0].Title != "4" {
		t.Fatalf("Query(2 + 2) = %v, want a single result titled 4", results)
	}
	if results[0].Score != 100 {
		t.Errorf("Score = %v, want 100 (calculator trusts its own confidence)", results[0].Score)
	}

	results = p.Query(nil, "10 km to mi")
	if len(results) != 1 {
		t.Fatalf("Query(10 km to mi) = %v, want one result", results)
	}

	if results := p.Query(nil, "not an expression"); results != nil {
		t.Errorf("Query on non-expression input should return nil, got %v", results)
	}
}
