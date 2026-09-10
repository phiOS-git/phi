package mathx

import "math"

// Simplify applies a bounded set of algebraic identities bottom-up until
// the tree stops changing (or a small iteration cap is hit). It is not a
// computer-algebra normaliser — it exists to keep derivative output and
// step text readable: constant folding, x+0, x*1, x*0, x^1, x^0, 0-x,
// double negation, and a light collection of like terms (x+x -> 2x,
// x*x -> x^2).

func Simplify(n Node) Node {
	prev := ""
	cur := n
	for i := 0; i < 25; i++ {
		cur = simplifyOnce(clone(cur))
		s := cur.String()
		if s == prev {
			break
		}
		prev = s
	}
	return cur
}

func simplifyOnce(n Node) Node {
	switch t := n.(type) {
	case *Num, *Var:
		return n

	case *Call:
		for i, a := range t.Args {
			t.Args[i] = simplifyOnce(a)
		}
		// Fold a call with no free variables (constant args, including
		// named constants like e or pi), except the symbolic verbs.
		if isFoldable(t.Name) && len(freeVars(t)) == 0 {
			if v, err := Eval(t, nil); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
				return &Num{Val: v}
			}
		}
		return t

	case *Unary:
		t.X = simplifyOnce(t.X)
		if t.Op == "-" {
			if v, ok := numVal(t.X); ok {
				return &Num{Val: -v}
			}
			if inner, ok := t.X.(*Unary); ok && inner.Op == "-" && !inner.Post {
				return inner.X // --x -> x
			}
		}
		if t.Op == "%" && t.Post {
			if v, ok := numVal(t.X); ok {
				return &Num{Val: v / 100}
			}
		}
		if v, ok := numVal(t.X); ok && (t.Op == "!") {
			if r, err := factorial(v); err == nil {
				return &Num{Val: r}
			}
		}
		return t

	case *Binary:
		t.L = simplifyOnce(t.L)
		t.R = simplifyOnce(t.R)
		lv, lok := numVal(t.L)
		rv, rok := numVal(t.R)

		// Constant folding.
		if lok && rok {
			if v, err := Eval(&Binary{Op: t.Op, L: t.L, R: t.R}, nil); err == nil {
				if !math.IsNaN(v) && !math.IsInf(v, 0) {
					return &Num{Val: v}
				}
			}
		}

		switch t.Op {
		case "+":
			if lok && lv == 0 {
				return t.R
			}
			if rok && rv == 0 {
				return t.L
			}
			if equal(t.L, t.R) {
				return simplifyOnce(mul(num(2), t.L))
			}
		case "-":
			if rok && rv == 0 {
				return t.L
			}
			if lok && lv == 0 {
				return simplifyOnce(neg(t.R))
			}
			if equal(t.L, t.R) {
				return num(0)
			}
		case "*":
			if (lok && lv == 0) || (rok && rv == 0) {
				return num(0)
			}
			if lok && lv == 1 {
				return t.R
			}
			if rok && rv == 1 {
				return t.L
			}
			if lok && lv == -1 {
				return simplifyOnce(neg(t.R))
			}
			if rok && rv == -1 {
				return simplifyOnce(neg(t.L))
			}
			if equal(t.L, t.R) {
				return pow(t.L, num(2))
			}
		case "/":
			if rok && rv == 1 {
				return t.L
			}
			if lok && lv == 0 {
				return num(0)
			}
			if equal(t.L, t.R) {
				return num(1)
			}
		case "^":
			if rok && rv == 1 {
				return t.L
			}
			if rok && rv == 0 {
				return num(1)
			}
			if lok && lv == 1 {
				return num(1)
			}
			if lok && lv == 0 {
				return num(0)
			}
		}
		return t

	case *Compare:
		t.L = simplifyOnce(t.L)
		t.R = simplifyOnce(t.R)
		return t
	}
	return n
}

// isFoldable excludes the symbolic verbs (derivative, integrate, solve,
// plot, ...) — folding those would run them at simplify time, which is
// never what a simplify pass wants.
func isFoldable(name string) bool {
	switch name {
	case "derivative", "d", "integral", "integrate", "solve", "plot",
		"simplify", "expand", "sumfrom", "prodfrom", "p", "_and":
		return false
	}
	return true
}
