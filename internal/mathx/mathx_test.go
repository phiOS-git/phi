package mathx

import (
	"math"
	"strings"
	"testing"
)

func evalNum(t *testing.T, expr string) float64 {
	t.Helper()
	n, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	v, err := Eval(n, nil)
	if err != nil {
		t.Fatalf("Eval(%q): %v", expr, err)
	}
	return v
}

func TestArithmetic(t *testing.T) {
	cases := map[string]float64{
		"2 + 2":         4,
		"2+2*3":         8,
		"(2+2)*3":       12,
		"10 / 4":        2.5,
		"2 ^ 10":        1024,
		"2 ^ 3 ^ 2":     512,
		"-3 + 5":        2,
		"-(3 + 5)":      -8,
		"10 % 3":        1, // modulo, operand follows
		"10 mod 3":      1,
		"3.5 * 2":       7,
		"  4   +   4  ": 8,
		"2x where x=3":  0, // parse error path checked elsewhere; skip
		"1e3 + 1":       1001,
		"1_000 + 1":     1001,
		"1,000 + 1":     1001,
		"2×3":           6,
		"7 ÷ 2":         3.5,
		"|-5| + 1":      6,
		"sqrt(16)":      4,
		"sqrt 16":       4,
		"2(3+4)":        14,
		"3!":            6,
		"5!":            120,
		"nCr(5,2)":      10,
		"2 pi":          2 * math.Pi,
		"50% * 200":     100,
		"200 + 10%":     200.1, // 200 + (10/100)
	}
	for expr, want := range cases {
		if strings.Contains(expr, "where") {
			continue
		}
		got := evalNum(t, expr)
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("%q = %v, want %v", expr, got, want)
		}
	}
}

func TestEvaluateValue(t *testing.T) {
	r, err := Evaluate("2 + 2 * 3")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "value" || r.Headline != "8" {
		t.Errorf("got %+v", r)
	}
}

func TestPercentPhrases(t *testing.T) {
	cases := map[string]float64{
		"20% of 150":       30,
		"10% off 200":      180,
		"5% on 200":        210,
		"15 percent of 60": 9,
	}
	for expr, want := range cases {
		r, err := Evaluate(expr)
		if err != nil {
			t.Fatalf("%q: %v", expr, err)
		}
		got, _ := parseFloatLoose(r.Headline)
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("%q = %v, want %v", expr, r.Headline, want)
		}
	}
}

func TestDerivative(t *testing.T) {
	cases := map[string]string{
		"x^2":    "2 * x",
		"x^3":    "3 * x^2",
		"sin(x)": "cos(x)",
		"2x + 1": "2",
		"e^x":    "e^x",
		"1/x":    "-1 / x^2",
	}
	for expr, want := range cases {
		r, err := Evaluate("derivative of " + expr)
		if err != nil {
			t.Fatalf("%q: %v", expr, err)
		}
		if r.Headline != want {
			t.Errorf("d/dx %q = %q, want %q", expr, r.Headline, want)
		}
	}
}

func TestDerivativeNumericCheck(t *testing.T) {
	// d/dx tan(x) at x=1 should match a finite difference.
	r, err := Evaluate("d/dx tan(x) at x=1")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Solutions) == 0 {
		t.Fatalf("no evaluated solution: %+v", r)
	}
	got, _ := parseFloatLoose(strings.TrimPrefix(r.Solutions[0], "f'(1) = "))
	want := (math.Tan(1+1e-6) - math.Tan(1-1e-6)) / 2e-6
	if math.Abs(got-want) > 1e-3 {
		t.Errorf("tan'(1) = %v, want ~%v", got, want)
	}
}

func TestDefiniteIntegral(t *testing.T) {
	cases := []struct {
		expr string
		want float64
	}{
		{"integrate x^2 from 0 to 3", 9},
		{"integrate sin(x) from 0 to pi", 2},
		{"integral of 2x from 0 to 5", 25},
		{"integrate 1/x from 1 to e", 1},
	}
	for _, c := range cases {
		r, err := Evaluate(c.expr)
		if err != nil {
			t.Fatalf("%q: %v", c.expr, err)
		}
		got, _ := parseFloatLoose(r.Headline)
		if math.Abs(got-c.want) > 1e-6 {
			t.Errorf("%q = %v, want %v", c.expr, r.Headline, c.want)
		}
	}
}

func TestIndefiniteIntegral(t *testing.T) {
	r, err := Evaluate("integral of x^2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Headline, "x^3") || !strings.Contains(r.Headline, "C") {
		t.Errorf("∫x^2 dx = %q", r.Headline)
	}
}

func TestSolveLinear(t *testing.T) {
	r, err := Evaluate("solve 2x + 6 = 0")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Solutions) == 0 || r.Solutions[0] != "-3" {
		t.Errorf("2x+6=0 -> %+v", r.Solutions)
	}
	if len(r.Steps) == 0 {
		t.Error("expected worked steps for a linear equation")
	}
}

func TestSolveQuadratic(t *testing.T) {
	r, err := Evaluate("x^2 + x - 6 = 0")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range r.Solutions {
		got[s] = true
	}
	if !got["2"] || !got["-3"] {
		t.Errorf("x^2+x-6=0 -> %v, want {2, -3}", r.Solutions)
	}
}

func TestSolveComplexRoots(t *testing.T) {
	r, err := Evaluate("solve x^2 + 1 = 0")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(r.Solutions, " ")
	if !strings.Contains(joined, "i") {
		t.Errorf("x^2+1=0 -> %v, want complex", r.Solutions)
	}
}

func TestSolveInequality(t *testing.T) {
	r, err := Evaluate("x^2 - 4 < 0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Headline, "-2") || !strings.Contains(r.Headline, "2") {
		t.Errorf("x^2-4<0 -> %q, want -2 < x < 2", r.Headline)
	}
}

func TestPlot(t *testing.T) {
	r, err := Evaluate("plot sin(x)")
	if err != nil {
		t.Fatal(err)
	}
	if r.Plot == nil || len(r.Plot.Points) < 100 {
		t.Fatalf("plot sin(x): %+v", r.Plot)
	}
	if r.Plot.YMin > -0.9 || r.Plot.YMax < 0.9 {
		t.Errorf("sin plot y-range too tight: %v..%v", r.Plot.YMin, r.Plot.YMax)
	}
}

func TestConversionBasics(t *testing.T) {
	cases := []struct {
		q    string
		want float64
		unit string
	}{
		{"100 km to m", 100000, "m"},
		{"100km to m", 100000, "m"},
		{"1 mile in feet", 5280, "ft"},
		{"-40 C to F", -40, "°F"},
		{"0 C to F", 32, "°F"},
		{"1 cup in ml", 236.5882365, "mL"},
		{"2 GiB to MB", 2147.483648, "MB"},
		{"1e6 bytes to MB", 1, "MB"},
		{"180 deg to rad", math.Pi, "rad"},
		{"1 day in seconds", 86400, "s"},
	}
	for _, c := range cases {
		r, ok := ParseConversion(c.q)
		if !ok {
			t.Errorf("%q: not recognised as a conversion", c.q)
			continue
		}
		got, _ := parseFloatLoose(r.CopyText)
		if math.Abs(got-c.want) > 1e-6*math.Max(1, math.Abs(c.want)) {
			t.Errorf("%q = %v, want %v %s", c.q, r.CopyText, c.want, c.unit)
		}
		if !strings.Contains(r.Headline, c.unit) {
			t.Errorf("%q headline %q missing unit %q", c.q, r.Headline, c.unit)
		}
	}
}

func TestConversionRejects(t *testing.T) {
	for _, q := range []string{"2 + 2", "banana to apple", "hello world", "10 to 20"} {
		if _, ok := ParseConversion(q); ok {
			t.Errorf("%q should not be a conversion", q)
		}
	}
}

func TestConversionDimensionMismatch(t *testing.T) {
	r, ok := ParseConversion("5 kg to m")
	if !ok {
		t.Fatal("expected a (failing) conversion report")
	}
	if !strings.Contains(strings.ToLower(r.Headline), "cannot convert") {
		t.Errorf("kg to m -> %q", r.Headline)
	}
}

func TestNoInfiniteRootDump(t *testing.T) {
	r, err := Evaluate("solve sin(x) = 0.5")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Solutions) > 12 {
		t.Errorf("sin(x)=0.5 returned %d solutions, want a bounded set", len(r.Solutions))
	}
	// 0.523598... (pi/6) must be among them.
	found := false
	for _, s := range r.Solutions {
		if v, e := parseFloatLoose(s); e == nil && math.Abs(v-math.Pi/6) < 1e-4 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected pi/6 among %v", r.Solutions)
	}
}

func TestFunctionLibrary(t *testing.T) {
	cases := map[string]float64{
		"log(1000)":         3,
		"log(8, 2)":         3,
		"ln(e^2)":           2,
		"log2(1024)":        10,
		"hypot(3,4)":        5,
		"gcd(12,18)":        6,
		"lcm(4,6)":          12,
		"floor(3.7)":        3,
		"ceil(3.2)":         4,
		"round(3.14159, 2)": 3.14,
		"abs(-7)":           7,
		"max(1,9,4)":        9,
		"min(5,2,8)":        2,
		"clamp(15, 0, 10)":  10,
	}
	for expr, want := range cases {
		got := evalNum(t, expr)
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("%q = %v, want %v", expr, got, want)
		}
	}
}
