package mathx

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Error is the one error type the whole package returns, so a caller can
// always render a position if it has one.
type Error struct {
	Msg string
	Pos int
}

func (e *Error) Error() string { return e.Msg }

func errf(format string, a ...any) error { return &Error{Msg: fmt.Sprintf(format, a...)} }

// parseFloatLoose accepts what strconv.ParseFloat does plus a bare
// leading dot (".5").
func parseFloatLoose(s string) (float64, error) {
	if strings.HasPrefix(s, ".") {
		s = "0" + s
	}
	return strconv.ParseFloat(s, 64)
}

// Node is one node of the expression tree. The concrete types are a small
// closed set; every pass in this package type-switches over them.
type Node interface {
	String() string
	isNode()
}

type (
	// Num is a literal.
	Num struct{ Val float64 }
	// Var is a name that is looked up in the environment at eval time
	// (a constant like pi, or a solve/plot/derivative variable).
	Var struct{ Name string }
	// Call is a function application: name(args...).
	Call struct {
		Name string
		Args []Node
	}
	// Unary is a prefix "-" / "+" or a postfix "!" (factorial) / "%"
	// (percent, meaning x/100). Postfix is recorded with Post = true.
	Unary struct {
		Op   string
		X    Node
		Post bool
	}
	// Binary is an arithmetic operator: + - * / % ^ .
	Binary struct {
		Op   string
		L, R Node
	}
	// Compare is a relation: = < > <= >= != . Its presence at the root
	// of a parse turns "evaluate" into "solve".
	Compare struct {
		Op   string
		L, R Node
	}
)

func (*Num) isNode()     {}
func (*Var) isNode()     {}
func (*Call) isNode()    {}
func (*Unary) isNode()   {}
func (*Binary) isNode()  {}
func (*Compare) isNode() {}

func (n *Num) String() string {
	return formatFloat(n.Val)
}
func (n *Var) String() string { return n.Name }
func (n *Call) String() string {
	parts := make([]string, len(n.Args))
	for i, a := range n.Args {
		parts[i] = a.String()
	}
	return n.Name + "(" + strings.Join(parts, ", ") + ")"
}
func (n *Unary) String() string {
	if n.Post {
		return wrap(n.X, 70) + n.Op
	}
	return n.Op + wrap(n.X, 70)
}
func (n *Binary) String() string {
	p := opPrec(n.Op)
	l := wrap(n.L, p)
	r := wrapRight(n.R, p, n.Op)
	sep := " " + n.Op + " "
	if n.Op == "^" {
		sep = "^"
	}
	return l + sep + r
}
func (n *Compare) String() string {
	return n.L.String() + " " + n.Op + " " + n.R.String()
}

// --- precedence-aware pretty printing -------------------------------------

func opPrec(op string) int {
	switch op {
	case "=", "<", ">", "<=", ">=", "!=":
		return 5
	case "+", "-":
		return 10
	case "*", "/", "%":
		return 20
	case "^":
		return 30
	}
	return 0
}

func nodePrec(n Node) int {
	switch t := n.(type) {
	case *Binary:
		return opPrec(t.Op)
	case *Compare:
		return opPrec(t.Op)
	case *Unary:
		if t.Post {
			return 70
		}
		return 25
	case *Num:
		if t.Val < 0 {
			return 25
		}
		return 100
	}
	return 100
}

func wrap(n Node, parentPrec int) string {
	if nodePrec(n) < parentPrec {
		return "(" + n.String() + ")"
	}
	return n.String()
}

// wrapRight adds a parenthesis when a right operand of the same precedence
// must not re-associate ("a - (b - c)", "a / (b * c)", "a ^ (b ^ c)" is
// fine since ^ is right-assoc so no parens needed there).
func wrapRight(n Node, parentPrec int, parentOp string) string {
	np := nodePrec(n)
	if np < parentPrec || (np == parentPrec && (parentOp == "-" || parentOp == "/" || parentOp == "%")) {
		return "(" + n.String() + ")"
	}
	return n.String()
}

// FormatNumber renders a float the way the launcher shows numbers —
// exported for other providers (currency) that need the same formatting.
func FormatNumber(v float64) string { return formatFloat(v) }

// formatFloat renders a number the way the launcher should show it: a
// plain integer when it is one, a trimmed decimal otherwise, and
// scientific notation only for genuinely large or small magnitudes.
func formatFloat(v float64) string {
	if math.IsInf(v, 1) {
		return "∞"
	}
	if math.IsInf(v, -1) {
		return "-∞"
	}
	if math.IsNaN(v) {
		return "undefined"
	}
	if v == 0 {
		return "0"
	}
	abs := math.Abs(v)
	if v == math.Trunc(v) && abs < 1e15 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	if abs >= 1e-4 && abs < 1e12 {
		s := strconv.FormatFloat(v, 'f', 10, 64)
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
		// Guard against "-0" style artefacts.
		if s == "-0" {
			return "0"
		}
		return s
	}
	return strconv.FormatFloat(v, 'g', 8, 64)
}

// --- structural helpers used by simplify / deriv / solve -----------------

func numVal(n Node) (float64, bool) {
	if num, ok := n.(*Num); ok {
		return num.Val, true
	}
	return 0, false
}

// clone deep-copies a subtree so a pass can rewrite freely.
func clone(n Node) Node {
	switch t := n.(type) {
	case *Num:
		return &Num{Val: t.Val}
	case *Var:
		return &Var{Name: t.Name}
	case *Call:
		args := make([]Node, len(t.Args))
		for i, a := range t.Args {
			args[i] = clone(a)
		}
		return &Call{Name: t.Name, Args: args}
	case *Unary:
		return &Unary{Op: t.Op, X: clone(t.X), Post: t.Post}
	case *Binary:
		return &Binary{Op: t.Op, L: clone(t.L), R: clone(t.R)}
	case *Compare:
		return &Compare{Op: t.Op, L: clone(t.L), R: clone(t.R)}
	}
	return n
}

// equal reports structural equality (after both sides are simplified by
// the caller when that matters).
func equal(a, b Node) bool {
	switch x := a.(type) {
	case *Num:
		y, ok := b.(*Num)
		return ok && x.Val == y.Val
	case *Var:
		y, ok := b.(*Var)
		return ok && x.Name == y.Name
	case *Call:
		y, ok := b.(*Call)
		if !ok || x.Name != y.Name || len(x.Args) != len(y.Args) {
			return false
		}
		for i := range x.Args {
			if !equal(x.Args[i], y.Args[i]) {
				return false
			}
		}
		return true
	case *Unary:
		y, ok := b.(*Unary)
		return ok && x.Op == y.Op && x.Post == y.Post && equal(x.X, y.X)
	case *Binary:
		y, ok := b.(*Binary)
		return ok && x.Op == y.Op && equal(x.L, y.L) && equal(x.R, y.R)
	case *Compare:
		y, ok := b.(*Compare)
		return ok && x.Op == y.Op && equal(x.L, y.L) && equal(x.R, y.R)
	}
	return false
}

// mentions reports whether name appears anywhere in the subtree.
func mentions(n Node, name string) bool {
	found := false
	walk(n, func(x Node) {
		if v, ok := x.(*Var); ok && v.Name == name {
			found = true
		}
	})
	return found
}

// freeVars collects every distinct Var name in the subtree, excluding the
// ones that are really constants (pi, e, ...) or function names.
func freeVars(n Node) []string {
	seen := map[string]bool{}
	var out []string
	walk(n, func(x Node) {
		if v, ok := x.(*Var); ok {
			if _, isConst := constants[v.Name]; isConst {
				return
			}
			if !seen[v.Name] {
				seen[v.Name] = true
				out = append(out, v.Name)
			}
		}
	})
	return out
}

func walk(n Node, fn func(Node)) {
	if n == nil {
		return
	}
	fn(n)
	switch t := n.(type) {
	case *Call:
		for _, a := range t.Args {
			walk(a, fn)
		}
	case *Unary:
		walk(t.X, fn)
	case *Binary:
		walk(t.L, fn)
		walk(t.R, fn)
	case *Compare:
		walk(t.L, fn)
		walk(t.R, fn)
	}
}

// num / neg / add / mul / pow are tiny constructors that keep the rewrite
// passes readable.
func num(v float64) Node            { return &Num{Val: v} }
func add(a, b Node) Node            { return &Binary{Op: "+", L: a, R: b} }
func sub(a, b Node) Node            { return &Binary{Op: "-", L: a, R: b} }
func mul(a, b Node) Node            { return &Binary{Op: "*", L: a, R: b} }
func div(a, b Node) Node            { return &Binary{Op: "/", L: a, R: b} }
func pow(a, b Node) Node            { return &Binary{Op: "^", L: a, R: b} }
func neg(a Node) Node               { return &Unary{Op: "-", X: a} }
func call(n string, a ...Node) Node { return &Call{Name: n, Args: a} }
