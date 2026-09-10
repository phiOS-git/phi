package mathx

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Equation and inequality solving for a single unknown.
//
//   - Polynomials up to degree 2 are solved exactly (linear isolation and
//     the quadratic formula), with worked steps and complex roots where
//     they occur.
//   - Higher-degree polynomials and any transcendental equation are
//     solved numerically: a broad scan brackets every sign change, then
//     bisection + a Newton polish locates each root; duplicates are
//     merged.
//   - Inequalities are reduced to f(x) <relation> 0, the real roots of f
//     become the breakpoints, and the sign of f is sampled on each open
//     interval to assemble the solution set in interval notation.
//
// It does not do systems of equations, symbolic transcendental inversion,
// or parametric solutions.

// SolveResult is the outcome of solving one relation.
type SolveResult struct {
	Variable  string
	Kind      string   // "equation" | "inequality" | "identity" | "contradiction"
	Exact     bool     // true when solved by formula rather than numerically
	Solutions []string // rendered, e.g. "2", "-1/2", "1.4142", "1 + 2i"
	Interval  string   // for inequalities, e.g. "x < -2 or x > 2"
	Steps     []string
}

// Solve solves a relation (Compare) or, for a bare expression, "expr = 0".
func Solve(n Node, variable string) (*SolveResult, error) {
	if variable == "" {
		vs := freeVars(n)
		if len(vs) != 1 {
			return nil, errf("specify the variable to solve for")
		}
		variable = vs[0]
	}

	op := "="
	var lhs, rhs Node
	if c, ok := n.(*Compare); ok {
		op = c.Op
		lhs, rhs = c.L, c.R
	} else {
		lhs, rhs = n, num(0)
	}

	// f(x) = lhs - rhs, so we solve f(x) <op> 0.
	f := Simplify(sub(clone(lhs), clone(rhs)))
	if mentions(f, variable) == false {
		// No unknown left: the relation is constant.
		v, err := Eval(f, nil)
		if err == nil {
			res := &SolveResult{Variable: variable}
			if boolCompare(op, v, 0) {
				res.Kind = "identity"
				res.Solutions = []string{"all real numbers"}
			} else {
				res.Kind = "contradiction"
				res.Solutions = []string{"no solution"}
			}
			return res, nil
		}
	}

	if op == "=" {
		return solveEquation(f, lhs, rhs, variable)
	}
	return solveInequality(f, op, lhs, rhs, variable)
}

func solveEquation(f, lhs, rhs Node, v string) (*SolveResult, error) {
	res := &SolveResult{Variable: v, Kind: "equation"}

	if coeffs, ok := polyCoeffs(f, v); ok {
		deg := polyDegree(coeffs)
		switch deg {
		case 0:
			if coeffs[0] == 0 {
				res.Kind = "identity"
				res.Solutions = []string{"all real numbers"}
			} else {
				res.Kind = "contradiction"
				res.Solutions = []string{"no solution"}
			}
			return res, nil
		case 1:
			return solveLinear(coeffs, lhs, rhs, v), nil
		case 2:
			return solveQuadratic(coeffs, lhs, rhs, v), nil
		}
		// Degree 3-4: try rational/real roots numerically but mark exact
		// where a clean root is found.
		roots := numericRoots(func(x float64) float64 { return polyEval(coeffs, x) }, polyDeriv(coeffs))
		res.Steps = append(res.Steps,
			fmt.Sprintf("Polynomial of degree %d: %s = 0", deg, polyString(coeffs, v)),
			"Solved numerically (no general radical formula is applied above degree 2).")
		res.Solutions = renderRoots(roots)
		return res, nil
	}

	// Transcendental / non-polynomial: numeric only.
	fn := func(x float64) float64 {
		val, err := Eval(f, Env{v: x})
		if err != nil {
			return math.NaN()
		}
		return val
	}
	roots, truncated := numericRootsPeriodic(fn)
	res.Steps = append(res.Steps,
		"Rearranged to  "+Simplify(f).String()+" = 0",
		"No closed form applies; solved numerically near the origin.")
	if len(roots) == 0 {
		res.Solutions = []string{"no real solution found"}
		return res, nil
	}
	res.Solutions = renderRoots(roots)
	if truncated {
		res.Solutions = append(res.Solutions, "… (more solutions further out)")
		res.Steps = append(res.Steps, "This equation has further solutions outside the window shown.")
	}
	return res, nil
}

func solveLinear(c []float64, lhs, rhs Node, v string) *SolveResult {
	// c[0] + c[1] x = 0  ->  x = -c[0]/c[1]
	a, b := c[1], c[0]
	root := -b / a
	steps := []string{
		fmt.Sprintf("%s = %s", lhs.String(), rhs.String()),
		fmt.Sprintf("Collect terms:  %s = 0", polyString(c, v)),
		fmt.Sprintf("%s%s = %s", coef(a), v, formatFloat(-b)),
		fmt.Sprintf("%s = %s ÷ %s = %s", v, formatFloat(-b), formatFloat(a), formatFloat(root)),
	}
	return &SolveResult{
		Variable: v, Kind: "equation", Exact: true,
		Solutions: []string{formatFloat(root), rationalHint(root)},
		Steps:     steps,
	}
}

func solveQuadratic(c []float64, lhs, rhs Node, v string) *SolveResult {
	a, b, cc := c[2], c[1], c[0]
	disc := b*b - 4*a*cc
	steps := []string{
		fmt.Sprintf("%s = %s", lhs.String(), rhs.String()),
		fmt.Sprintf("Standard form:  %s = 0", polyString([]float64{cc, b, a}, v)),
		fmt.Sprintf("a = %s,  b = %s,  c = %s", formatFloat(a), formatFloat(b), formatFloat(cc)),
		fmt.Sprintf("Discriminant  Δ = b² − 4ac = %s", formatFloat(disc)),
		fmt.Sprintf("%s = (−b ± √Δ) / 2a", v),
	}
	res := &SolveResult{Variable: v, Kind: "equation", Exact: true, Steps: steps}
	switch {
	case disc > 0:
		s := math.Sqrt(disc)
		r1, r2 := (-b+s)/(2*a), (-b-s)/(2*a)
		res.Steps = append(res.Steps, fmt.Sprintf("√Δ = %s", formatFloat(s)))
		res.Solutions = []string{formatFloat(r1), formatFloat(r2)}
	case disc == 0:
		r := -b / (2 * a)
		res.Steps = append(res.Steps, "Δ = 0 → one repeated root")
		res.Solutions = []string{formatFloat(r)}
	default:
		re := -b / (2 * a)
		im := math.Sqrt(-disc) / (2 * a)
		res.Steps = append(res.Steps, fmt.Sprintf("Δ < 0 → complex conjugate roots, √Δ = %si", formatFloat(math.Sqrt(-disc))))
		res.Solutions = []string{
			complexString(re, im),
			complexString(re, -im),
		}
	}
	return res
}

func solveInequality(f Node, op string, lhs, rhs Node, v string) (*SolveResult, error) {
	res := &SolveResult{Variable: v, Kind: "inequality"}
	fn := func(x float64) float64 {
		val, err := Eval(f, Env{v: x})
		if err != nil {
			return math.NaN()
		}
		return val
	}

	var breaks []float64
	if coeffs, ok := polyCoeffs(f, v); ok {
		breaks = numericRoots(func(x float64) float64 { return polyEval(coeffs, x) }, polyDeriv(coeffs))
		res.Steps = append(res.Steps, fmt.Sprintf("Reduce to  %s %s 0", polyString(coeffs, v), op))
	} else {
		breaks, _ = numericRootsPeriodic(fn)
		res.Steps = append(res.Steps, "Reduce to  "+Simplify(f).String()+" "+op+" 0")
	}
	sort.Float64s(breaks)
	breaks = dedupeSorted(breaks, 1e-7)

	res.Steps = append(res.Steps, "Boundary points: "+joinFloats(breaks))

	// Sample the sign of f on each open interval delimited by the breaks.
	pts := samplePoints(breaks)
	var segments []string
	for i, p := range pts {
		val := fn(p.probe)
		ok := boolCompare(op, val, 0)
		if ok {
			segments = append(segments, intervalLabel(p, v, strings.Contains(op, "=")))
		}
		_ = i
	}
	// Include the boundary points themselves for <= / >=.
	if strings.Contains(op, "=") {
		for _, b := range breaks {
			if boolCompare(op, fn(b), 0) {
				segments = append(segments, fmt.Sprintf("%s = %s", v, formatFloat(b)))
			}
		}
	}

	if len(segments) == 0 {
		res.Interval = "no solution"
		res.Solutions = []string{"no solution"}
	} else {
		res.Interval = strings.Join(dedupeStrings(segments), "  or  ")
		res.Solutions = []string{res.Interval}
	}
	res.Steps = append(res.Steps, "Sign chart → "+res.Interval)
	return res, nil
}

// --- polynomial helpers -------------------------------------------------

// polyCoeffs returns the coefficient slice (index i is the x^i term) if n
// is a polynomial in v with constant coefficients, else ok == false.
func polyCoeffs(n Node, v string) (coeffs []float64, ok bool) {
	switch t := n.(type) {
	case *Num:
		return []float64{t.Val}, true
	case *Var:
		if t.Name == v {
			return []float64{0, 1}, true
		}
		if val, isC := constants[t.Name]; isC {
			return []float64{val}, true
		}
		return nil, false
	case *Unary:
		x, ok := polyCoeffs(t.X, v)
		if !ok {
			return nil, false
		}
		switch {
		case t.Op == "-" && !t.Post:
			return scalePoly(x, -1), true
		case t.Op == "+" && !t.Post:
			return x, true
		case t.Op == "%" && t.Post:
			return scalePoly(x, 0.01), true
		}
		return nil, false
	case *Binary:
		switch t.Op {
		case "+", "-":
			l, lok := polyCoeffs(t.L, v)
			r, rok := polyCoeffs(t.R, v)
			if !lok || !rok {
				return nil, false
			}
			if t.Op == "-" {
				r = scalePoly(r, -1)
			}
			return addPoly(l, r), true
		case "*":
			l, lok := polyCoeffs(t.L, v)
			r, rok := polyCoeffs(t.R, v)
			if !lok || !rok {
				return nil, false
			}
			return mulPoly(l, r), true
		case "/":
			if mentions(t.R, v) {
				return nil, false
			}
			d, err := Eval(t.R, nil)
			if err != nil || d == 0 {
				return nil, false
			}
			l, lok := polyCoeffs(t.L, v)
			if !lok {
				return nil, false
			}
			return scalePoly(l, 1/d), true
		case "^":
			e, err := Eval(t.R, nil)
			if err != nil || e < 0 || e != math.Trunc(e) || e > 12 {
				return nil, false
			}
			base, ok := polyCoeffs(t.L, v)
			if !ok {
				return nil, false
			}
			result := []float64{1}
			for i := 0; i < int(e); i++ {
				result = mulPoly(result, base)
			}
			return result, true
		}
		return nil, false
	case *Call:
		if mentions(t, v) {
			return nil, false
		}
		val, err := Eval(t, nil)
		if err != nil {
			return nil, false
		}
		return []float64{val}, true
	}
	return nil, false
}

func scalePoly(c []float64, k float64) []float64 {
	out := make([]float64, len(c))
	for i, x := range c {
		out[i] = x * k
	}
	return out
}

func addPoly(a, b []float64) []float64 {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	out := make([]float64, n)
	for i := range out {
		if i < len(a) {
			out[i] += a[i]
		}
		if i < len(b) {
			out[i] += b[i]
		}
	}
	return out
}

func mulPoly(a, b []float64) []float64 {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	out := make([]float64, len(a)+len(b)-1)
	for i, av := range a {
		for j, bv := range b {
			out[i+j] += av * bv
		}
	}
	return out
}

func polyDegree(c []float64) int {
	for i := len(c) - 1; i > 0; i-- {
		if math.Abs(c[i]) > 1e-12 {
			return i
		}
	}
	return 0
}

func polyEval(c []float64, x float64) float64 {
	v := 0.0
	for i := len(c) - 1; i >= 0; i-- {
		v = v*x + c[i]
	}
	return v
}

func polyDeriv(c []float64) func(float64) float64 {
	if len(c) < 2 {
		return func(float64) float64 { return 0 }
	}
	d := make([]float64, len(c)-1)
	for i := 1; i < len(c); i++ {
		d[i-1] = c[i] * float64(i)
	}
	return func(x float64) float64 { return polyEval(d, x) }
}

// polyString renders a coefficient slice as "x^2 + 3x - 4", hiding unit
// coefficients and folding the sign of each term into the joiner.
func polyString(c []float64, v string) string {
	var b strings.Builder
	first := true
	for i := len(c) - 1; i >= 0; i-- {
		if math.Abs(c[i]) < 1e-12 {
			continue
		}
		coeff := c[i]
		switch {
		case first:
			if coeff < 0 {
				b.WriteString("−")
			}
			first = false
		case coeff < 0:
			b.WriteString(" − ")
		default:
			b.WriteString(" + ")
		}
		mag := math.Abs(coeff)
		body := ""
		switch i {
		case 0:
			body = formatFloat(mag)
		case 1:
			if mag != 1 {
				body = formatFloat(mag)
			}
			body += v
		default:
			if mag != 1 {
				body = formatFloat(mag)
			}
			body += fmt.Sprintf("%s^%d", v, i)
		}
		b.WriteString(body)
	}
	if first {
		return "0"
	}
	return b.String()
}

// coef prints a coefficient that directly precedes a variable, dropping a
// unit magnitude ("x" not "1x", "−x" not "−1x").
func coef(c float64) string {
	switch {
	case c == 1:
		return ""
	case c == -1:
		return "−"
	default:
		return formatFloat(c)
	}
}

// --- numeric root finding ---------------------------------------------

// numericRoots scans a wide domain for a polynomial-like function: roots
// are few, so a broad sweep is safe.
func numericRoots(f func(float64) float64, deriv func(float64) float64) []float64 {
	return numericRootsBounded(f, deriv, -1000, 1000, 0.02, 32)
}

// numericRootsPeriodic is for transcendental equations, which may have
// infinitely many roots: it scans only a window around the origin and
// caps the count, so "solve sin(x) = 0.5" returns a handful of nearby
// solutions rather than thousands.
func numericRootsPeriodic(f func(float64) float64) ([]float64, bool) {
	r := numericRootsBounded(f, nil, -8*math.Pi, 8*math.Pi, math.Pi/2000, 12)
	// Sort by proximity to zero so the ones shown are the "principal" set.
	sort.Slice(r, func(i, j int) bool { return math.Abs(r[i]) < math.Abs(r[j]) })
	truncated := len(r) >= 12
	if len(r) > 8 {
		r = r[:8]
	}
	sort.Float64s(r)
	return r, truncated
}

func numericRootsBounded(f func(float64) float64, deriv func(float64) float64, lo, hi, step float64, cap int) []float64 {
	var roots []float64
	add := func(r float64) {
		if math.IsNaN(r) || math.IsInf(r, 0) {
			return
		}
		if math.Abs(f(r)) > 1e-6 {
			return
		}
		for _, e := range roots {
			if math.Abs(e-r) < 1e-6 {
				return
			}
		}
		roots = append(roots, r)
	}

	prev := f(lo)
	for x := lo + step; x <= hi && len(roots) < cap; x += step {
		cur := f(x)
		switch {
		case prev == 0:
			add(x - step)
		case cur == 0:
			add(x)
		case prev*cur < 0:
			add(bisect(f, x-step, x))
		}
		prev = cur
	}

	// Newton polish from a few seeds to catch tangent roots the sign scan
	// misses (e.g. (x-3)^2 = 0).
	span := hi - lo
	for _, frac := range []float64{0.02, 0.1, 0.25, 0.4, 0.5, 0.6, 0.75, 0.9, 0.98} {
		if len(roots) >= cap {
			break
		}
		if r, ok := newton(f, deriv, lo+span*frac); ok && r >= lo && r <= hi {
			add(r)
		}
	}

	sort.Float64s(roots)
	return roots
}

func bisect(f func(float64) float64, lo, hi float64) float64 {
	flo := f(lo)
	for i := 0; i < 200; i++ {
		mid := (lo + hi) / 2
		fm := f(mid)
		if fm == 0 || (hi-lo) < 1e-13 {
			return mid
		}
		if (flo < 0) != (fm < 0) {
			hi = mid
		} else {
			lo, flo = mid, fm
		}
	}
	return (lo + hi) / 2
}

func newton(f func(float64) float64, deriv func(float64) float64, x float64) (float64, bool) {
	for i := 0; i < 100; i++ {
		fx := f(x)
		if math.Abs(fx) < 1e-12 {
			return x, true
		}
		var d float64
		if deriv != nil {
			d = deriv(x)
		} else {
			h := 1e-6 * (math.Abs(x) + 1e-6)
			d = (f(x+h) - f(x-h)) / (2 * h)
		}
		if d == 0 || math.IsNaN(d) {
			return 0, false
		}
		nx := x - fx/d
		if math.IsNaN(nx) || math.IsInf(nx, 0) {
			return 0, false
		}
		if math.Abs(nx-x) < 1e-13 {
			x = nx
			break
		}
		x = nx
	}
	if math.Abs(f(x)) < 1e-7 {
		return x, true
	}
	return 0, false
}

// --- rendering helpers ------------------------------------------------

func renderRoots(roots []float64) []string {
	if len(roots) == 0 {
		return []string{"no real solution found"}
	}
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if math.Abs(r) < 1e-10 {
			r = 0
		}
		out = append(out, formatFloat(r))
	}
	return out
}

func complexString(re, im float64) string {
	if math.Abs(im) < 1e-12 {
		return formatFloat(re)
	}
	sign := "+"
	if im < 0 {
		sign = "−"
	}
	reStr := formatFloat(re)
	imStr := formatFloat(math.Abs(im))
	if imStr == "1" {
		imStr = ""
	}
	if math.Abs(re) < 1e-12 {
		if sign == "−" {
			return "−" + imStr + "i"
		}
		return imStr + "i"
	}
	return fmt.Sprintf("%s %s %si", reStr, sign, imStr)
}

// rationalHint offers a tidy fraction for a root that is obviously one
// (denominators up to 64), else returns "" so the caller can drop it.
func rationalHint(x float64) string {
	if x == math.Trunc(x) {
		return ""
	}
	for den := 2.0; den <= 64; den++ {
		num := x * den
		if math.Abs(num-math.Round(num)) < 1e-9 {
			return fmt.Sprintf("%s/%s", formatFloat(math.Round(num)), formatFloat(den))
		}
	}
	return ""
}

type interval struct {
	lo, hi       float64
	loInf, hiInf bool
	probe        float64
}

func samplePoints(breaks []float64) []interval {
	if len(breaks) == 0 {
		return []interval{{loInf: true, hiInf: true, probe: 0}}
	}
	var out []interval
	out = append(out, interval{loInf: true, hi: breaks[0], probe: breaks[0] - 1})
	for i := 0; i+1 < len(breaks); i++ {
		mid := (breaks[i] + breaks[i+1]) / 2
		out = append(out, interval{lo: breaks[i], hi: breaks[i+1], probe: mid})
	}
	out = append(out, interval{lo: breaks[len(breaks)-1], hiInf: true, probe: breaks[len(breaks)-1] + 1})
	return out
}

func intervalLabel(iv interval, v string, closed bool) string {
	lt, gt := "<", ">"
	if closed {
		lt, gt = "≤", "≥"
	}
	switch {
	case iv.loInf && iv.hiInf:
		return "all real " + v
	case iv.loInf:
		return fmt.Sprintf("%s %s %s", v, lt, formatFloat(iv.hi))
	case iv.hiInf:
		return fmt.Sprintf("%s %s %s", v, gt, formatFloat(iv.lo))
	default:
		return fmt.Sprintf("%s %s %s %s %s", formatFloat(iv.lo), lt, v, lt, formatFloat(iv.hi))
	}
}

func dedupeSorted(xs []float64, eps float64) []float64 {
	var out []float64
	for _, x := range xs {
		if len(out) == 0 || math.Abs(out[len(out)-1]-x) > eps {
			out = append(out, x)
		}
	}
	return out
}

func dedupeStrings(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func joinFloats(xs []float64) string {
	if len(xs) == 0 {
		return "none"
	}
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = formatFloat(x)
	}
	return strings.Join(parts, ", ")
}
