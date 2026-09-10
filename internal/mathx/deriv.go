package mathx

// Symbolic differentiation with respect to one variable. Covers the sum,
// product, quotient, power (constant and variable exponent) and chain
// rules, and the standard function library (trig, inverse trig,
// exp/log, sqrt, hyperbolic, abs). An unknown function differentiates to
// derivative(f(...), var) left in place rather than an error, so a
// partial result is still shown.

// Derivative returns d/dvar of n, simplified.
func Derivative(n Node, variable string) Node {
	return Simplify(diff(n, variable))
}

func diff(n Node, v string) Node {
	switch t := n.(type) {
	case *Num:
		return num(0)

	case *Var:
		if t.Name == v {
			return num(1)
		}
		return num(0) // any other symbol is treated as a constant

	case *Unary:
		switch t.Op {
		case "-":
			return neg(diff(t.X, v))
		case "+":
			return diff(t.X, v)
		case "%":
			return div(diff(t.X, v), num(100))
		}
		// factorial etc. — not differentiable here
		return &Call{Name: "derivative", Args: []Node{clone(n), &Var{Name: v}}}

	case *Binary:
		l, r := t.L, t.R
		dl, dr := diff(l, v), diff(r, v)
		switch t.Op {
		case "+":
			return add(dl, dr)
		case "-":
			return sub(dl, dr)
		case "*":
			// (l*r)' = l'r + lr'
			return add(mul(dl, clone(r)), mul(clone(l), dr))
		case "/":
			// (l/r)' = (l'r - lr') / r^2
			return div(sub(mul(dl, clone(r)), mul(clone(l), dr)), pow(clone(r), num(2)))
		case "^":
			return diffPow(l, r, v)
		case "%":
			// modulo: derivative is 1 wrt the dividend almost everywhere
			return dl
		}

	case *Call:
		return diffCall(t, v)

	case *Compare:
		return &Compare{Op: t.Op, L: diff(t.L, v), R: diff(t.R, v)}
	}
	return num(0)
}

// diffPow handles f^g in three cases: constant exponent (power rule),
// constant base (exponential rule), and the general case via the
// logarithmic-derivative identity.
func diffPow(base, exp Node, v string) Node {
	baseConst := !mentions(base, v)
	expConst := !mentions(exp, v)

	switch {
	case expConst:
		// (f^k)' = k * f^(k-1) * f'
		return mul(mul(clone(exp), pow(clone(base), sub(clone(exp), num(1)))), diff(base, v))
	case baseConst:
		// (a^g)' = a^g * ln(a) * g'
		return mul(mul(pow(clone(base), clone(exp)), call("ln", clone(base))), diff(exp, v))
	default:
		// (f^g)' = f^g * (g' ln f + g f'/f)
		fg := pow(clone(base), clone(exp))
		inner := add(
			mul(diff(exp, v), call("ln", clone(base))),
			mul(clone(exp), div(diff(base, v), clone(base))),
		)
		return mul(fg, inner)
	}
}

// diffCall differentiates a known function via the chain rule: for
// f(u), result is f'(u) * u'.
func diffCall(c *Call, v string) Node {
	if len(c.Args) != 1 {
		// Multi-arg or symbolic verb: leave a derivative() marker.
		return &Call{Name: "derivative", Args: []Node{clone(c), &Var{Name: v}}}
	}
	u := c.Args[0]
	du := diff(u, v)
	var outer Node
	switch c.Name {
	case "sin":
		outer = call("cos", clone(u))
	case "cos":
		outer = neg(call("sin", clone(u)))
	case "tan":
		outer = pow(call("sec", clone(u)), num(2))
	case "cot":
		outer = neg(pow(call("csc", clone(u)), num(2)))
	case "sec":
		outer = mul(call("sec", clone(u)), call("tan", clone(u)))
	case "csc":
		outer = neg(mul(call("csc", clone(u)), call("cot", clone(u))))
	case "asin":
		outer = div(num(1), call("sqrt", sub(num(1), pow(clone(u), num(2)))))
	case "acos":
		outer = neg(div(num(1), call("sqrt", sub(num(1), pow(clone(u), num(2))))))
	case "atan":
		outer = div(num(1), add(num(1), pow(clone(u), num(2))))
	case "sinh":
		outer = call("cosh", clone(u))
	case "cosh":
		outer = call("sinh", clone(u))
	case "tanh":
		outer = sub(num(1), pow(call("tanh", clone(u)), num(2)))
	case "exp":
		outer = call("exp", clone(u))
	case "ln":
		outer = div(num(1), clone(u))
	case "log", "log10":
		outer = div(num(1), mul(clone(u), call("ln", num(10))))
	case "log2":
		outer = div(num(1), mul(clone(u), call("ln", num(2))))
	case "sqrt":
		outer = div(num(1), mul(num(2), call("sqrt", clone(u))))
	case "cbrt":
		outer = div(num(1), mul(num(3), pow(call("cbrt", clone(u)), num(2))))
	case "abs":
		outer = call("sign", clone(u))
	default:
		return &Call{Name: "derivative", Args: []Node{clone(c), &Var{Name: v}}}
	}
	return mul(outer, du)
}
