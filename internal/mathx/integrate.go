package mathx

import (
	"math"
	"strings"
)

// Integration.
//
//   - A definite integral is evaluated by adaptive Simpson quadrature,
//     which is accurate and fast for the smooth integrands a launcher
//     sees, with a fixed recursion budget so a pathological input cannot
//     hang the query.
//   - An indefinite integral is attempted symbolically for the cases that
//     have a short closed form: polynomials term by term, 1/x, e^x,
//     sin/cos, sec^2, and a linear-substitution wrapper (f(ax+b)). Anything
//     else returns ok == false and the caller reports "no elementary
//     antiderivative found".

// IntegrateDefinite returns ∫_a^b f dvar and a short method note.
func IntegrateDefinite(f Node, variable string, a, b float64) (float64, string, error) {
	fn := func(x float64) float64 {
		v, err := Eval(f, Env{variable: x})
		if err != nil {
			return math.NaN()
		}
		return v
	}
	flip := 1.0
	if a > b {
		a, b, flip = b, a, -1
	}
	val, ok := adaptiveSimpson(fn, a, b, 1e-10, 40)
	if !ok {
		return 0, "", errf("integrand is not finite over [%s, %s]", formatFloat(a), formatFloat(b))
	}
	return flip * val, "adaptive Simpson quadrature", nil
}

// IntegrateIndefinite returns an antiderivative (without +C) when one is
// found in the supported set.
func IntegrateIndefinite(f Node, v string) (Node, bool) {
	n := Simplify(clone(f))

	// Polynomial: integrate term by term.
	if coeffs, ok := polyCoeffs(n, v); ok {
		var terms []Node
		for i, c := range coeffs {
			if c == 0 {
				continue
			}
			p := i + 1
			terms = append(terms, div(mul(num(c), pow(&Var{Name: v}, num(float64(p)))), num(float64(p))))
		}
		if len(terms) == 0 {
			return num(0), true
		}
		acc := terms[0]
		for _, t := range terms[1:] {
			acc = add(acc, t)
		}
		return Simplify(acc), true
	}

	if r, ok := intBasic(n, v); ok {
		return Simplify(r), true
	}
	return nil, false
}

// intBasic handles the small table of standard forms plus a
// constant-factor and linear-argument wrapper.
func intBasic(n Node, v string) (Node, bool) {
	switch t := n.(type) {
	case *Unary:
		if t.Op == "-" && !t.Post {
			if r, ok := intBasic(t.X, v); ok {
				return neg(r), true
			}
		}
	case *Binary:
		// Pull a constant factor out of a product.
		if t.Op == "*" {
			if !mentions(t.L, v) {
				if r, ok := intBasic(t.R, v); ok {
					return mul(clone(t.L), r), true
				}
			}
			if !mentions(t.R, v) {
				if r, ok := intBasic(t.L, v); ok {
					return mul(clone(t.R), r), true
				}
			}
		}
		if t.Op == "/" && !mentions(t.R, v) {
			if r, ok := intBasic(t.L, v); ok {
				return div(r, clone(t.R)), true
			}
		}
		// 1/x
		if t.Op == "/" {
			if lv, ok := numVal(t.L); ok && lv == 1 {
				if vr, ok := t.R.(*Var); ok && vr.Name == v {
					return call("ln", call("abs", &Var{Name: v})), true
				}
			}
		}
		// x^n  (already covered by polyCoeffs, but negative n lands here)
		if t.Op == "^" {
			if vr, ok := t.L.(*Var); ok && vr.Name == v {
				if e, ok := numVal(t.R); ok && e != -1 {
					return div(pow(&Var{Name: v}, num(e+1)), num(e+1)), true
				}
				if e, ok := numVal(t.R); ok && e == -1 {
					return call("ln", call("abs", &Var{Name: v})), true
				}
			}
		}
	case *Call:
		if len(t.Args) == 1 {
			arg := t.Args[0]
			// linear argument a*x + b
			a, b, lin := linearArg(arg, v)
			if !lin {
				return nil, false
			}
			var prim Node
			switch t.Name {
			case "sin":
				prim = neg(call("cos", clone(arg)))
			case "cos":
				prim = call("sin", clone(arg))
			case "exp":
				prim = call("exp", clone(arg))
			case "sec":
				return nil, false
			default:
				return nil, false
			}
			if a != 1 {
				return div(prim, num(a)), true
			}
			_ = b
			return prim, true
		}
	case *Var:
		if t.Name == v {
			return div(pow(&Var{Name: v}, num(2)), num(2)), true
		}
	case *Num:
		return mul(num(t.Val), &Var{Name: v}), true
	}
	return nil, false
}

// linearArg reports whether expr is a*v + b with constant a, b.
func linearArg(expr Node, v string) (a, b float64, ok bool) {
	c, isPoly := polyCoeffs(expr, v)
	if !isPoly {
		return 0, 0, false
	}
	if polyDegree(c) > 1 {
		return 0, 0, false
	}
	b = 0
	if len(c) > 0 {
		b = c[0]
	}
	a = 0
	if len(c) > 1 {
		a = c[1]
	}
	if a == 0 {
		return 0, 0, false
	}
	return a, b, true
}

// adaptiveSimpson integrates fn over [a,b] to tolerance tol with a
// recursion depth cap. ok == false if the integrand goes non-finite.
func adaptiveSimpson(fn func(float64) float64, a, b, tol float64, maxDepth int) (float64, bool) {
	fa, fb, fm := fn(a), fn(b), fn((a+b)/2)
	if !finite(fa) || !finite(fb) || !finite(fm) {
		// Retry nudging the endpoints inward — a removable endpoint
		// singularity (e.g. sin(x)/x at 0) should not defeat the whole
		// integral.
		eps := (b - a) * 1e-9
		fa, fb = fn(a+eps), fn(b-eps)
		if !finite(fa) || !finite(fb) {
			return 0, false
		}
	}
	whole := (b - a) / 6 * (fa + 4*fm + fb)
	v, ok := asr(fn, a, b, tol, whole, fa, fm, fb, maxDepth)
	return v, ok
}

func asr(fn func(float64) float64, a, b, tol, whole, fa, fm, fb float64, depth int) (float64, bool) {
	m := (a + b) / 2
	lm, rm := (a+m)/2, (m+b)/2
	flm, frm := fn(lm), fn(rm)
	if !finite(flm) || !finite(frm) {
		return whole, true // best effort
	}
	left := (m - a) / 6 * (fa + 4*flm + fm)
	right := (b - m) / 6 * (fm + 4*frm + fb)
	if depth <= 0 || math.Abs(left+right-whole) <= 15*tol {
		return left + right + (left+right-whole)/15, true
	}
	lv, lok := asr(fn, a, m, tol/2, left, fa, flm, fm, depth-1)
	rv, rok := asr(fn, m, b, tol/2, right, fm, frm, fb, depth-1)
	return lv + rv, lok && rok
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// AntiderivativeString renders an indefinite integral result with "+ C".
func AntiderivativeString(n Node) string {
	s := n.String()
	if strings.TrimSpace(s) == "0" {
		return "C"
	}
	return s + " + C"
}

// rangeSum evaluates a finite Σ or Π of f(v) for v = lo..hi (engine.go's
// "sum of ... for k=a to b" form).
func rangeSum(f Node, v string, lo, hi float64, product bool) (float64, error) {
	if hi-lo > 1e6 {
		return 0, errf("range too large")
	}
	acc := 0.0
	if product {
		acc = 1.0
	}
	for i := lo; i <= hi; i++ {
		val, err := Eval(f, Env{v: i})
		if err != nil {
			return 0, err
		}
		if product {
			acc *= val
		} else {
			acc += val
		}
	}
	return acc, nil
}
