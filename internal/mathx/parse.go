package mathx

import "strings"

// A precedence-climbing parser producing the Node tree. It supports:
//
//   - the arithmetic operators + - * / ^ and modulo (% or "mod")
//   - implicit multiplication: 2x, 2(3), (a)(b), 3 sin(x)  (same
//     precedence as an explicit *)
//   - prefix +/- and postfix ! (factorial) and % (percent -> x/100)
//   - function calls f(a, b, ...) and |x| for absolute value
//   - one relation (= < > <= >=) turning the parse into an equation or
//     inequality; a chained relation a < x < b becomes _and(a<x, x<b)
//
// Parse is the only export. Everything downstream (eval, deriv, simplify,
// solve, plot) consumes Node.

// Parse turns source text into a Node.
func Parse(input string) (Node, error) {
	toks, err := lex(input)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.parseRelation()
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tEOF {
		return nil, &Error{Msg: "unexpected " + describe(p.cur()), Pos: p.cur().pos}
	}
	return n, nil
}

type parser struct {
	toks      []token
	pos       int
	pipeDepth int // open '|' absolute-value bars, so a closing '|' is not read as a new one
}

func (p *parser) cur() token { return p.toks[p.pos] }
func (p *parser) peek() token {
	if p.pos+1 < len(p.toks) {
		return p.toks[p.pos+1]
	}
	return p.toks[len(p.toks)-1]
}
func (p *parser) advance() token { t := p.toks[p.pos]; p.pos++; return t }

func describe(t token) string {
	if t.kind == tEOF {
		return "end of input"
	}
	if t.text == "" {
		return "token"
	}
	return "'" + t.text + "'"
}

// parseRelation handles the optional comparison at the top level.
func (p *parser) parseRelation() (Node, error) {
	left, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if !isRelOp(p.cur()) {
		return left, nil
	}
	op := p.advance().text
	mid, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	first := &Compare{Op: op, L: left, R: mid}
	if !isRelOp(p.cur()) {
		return first, nil
	}
	// Chained: a op mid op2 right  ->  _and(a op mid, mid op2 right)
	op2 := p.advance().text
	right, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	second := &Compare{Op: op2, L: clone(mid), R: right}
	return &Call{Name: "_and", Args: []Node{first, second}}, nil
}

func isRelOp(t token) bool {
	if t.kind != tOp {
		return false
	}
	switch t.text {
	case "=", "<", ">", "<=", ">=", "!=":
		return true
	}
	return false
}

// binPrec is the binding power of an infix arithmetic operator, or -1 if
// the token is not one.
func binPrec(t token, prev Node) int {
	if t.kind == tIdent && strings.EqualFold(t.text, "mod") {
		return 20
	}
	if t.kind == tIdent && strings.EqualFold(t.text, "to") {
		return -1 // handled by the caller (conversion grammar), never here
	}
	if t.kind != tOp {
		return -1
	}
	switch t.text {
	case "+", "-":
		return 10
	case "*", "/":
		return 20
	case "%":
		// modulo only when an operand follows; otherwise it is postfix
		// percent and belongs to parsePostfix.
		return 20
	case "^":
		return 30
	}
	return -1
}

func (p *parser) parseExpr(minPrec int) (Node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()

		// A closing absolute-value bar ends this expression; it must not
		// be read as the start of a new |...| via implicit multiplication.
		if t.kind == tPipe && p.pipeDepth > 0 {
			return left, nil
		}

		// Implicit multiplication: a primary immediately followed by
		// something that starts another primary.
		if startsPrimary(t) && 20 >= minPrec {
			right, err := p.parseExprRHS(20 + 1)
			if err != nil {
				return nil, err
			}
			left = &Binary{Op: "*", L: left, R: right}
			continue
		}

		prec := binPrec(t, left)
		if prec < 0 || prec < minPrec {
			return left, nil
		}
		// Percent: a lone "%" with no operand after it is postfix, not
		// modulo — but parsePostfix already consumed those, so if we see
		// "%" here with no primary following, stop.
		if t.text == "%" && !startsPrimary(p.peek()) && p.peek().kind != tOp {
			return left, nil
		}
		op := t.text
		if t.kind == tIdent {
			op = "%" // "mod"
		}
		p.advance()
		nextMin := prec + 1
		if op == "^" {
			nextMin = prec // right-associative
		}
		right, err := p.parseExprRHS(nextMin)
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: op, L: left, R: right}
	}
}

// parseExprRHS is parseExpr for a right-hand operand; split out only so the
// implicit-multiplication branch can recurse without re-reading a unary.
func (p *parser) parseExprRHS(minPrec int) (Node, error) {
	return p.parseExpr(minPrec)
}

func startsPrimary(t token) bool {
	switch t.kind {
	case tNum, tLParen, tPipe:
		return true
	case tIdent:
		// "mod" / "to" / "and" / "or" are operators, not primaries.
		switch strings.ToLower(t.text) {
		case "mod", "to", "in", "and", "or", "as", "into":
			return false
		}
		return true
	}
	return false
}

func (p *parser) parseUnary() (Node, error) {
	t := p.cur()
	if t.kind == tOp && (t.text == "-" || t.text == "+") {
		p.advance()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if t.text == "-" {
			return &Unary{Op: "-", X: x}, nil
		}
		return x, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (Node, error) {
	n, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		if t.kind == tOp && t.text == "!" {
			p.advance()
			n = &Unary{Op: "!", X: n, Post: true}
			continue
		}
		if t.kind == tOp && t.text == "%" && !startsPrimary(p.peek()) {
			// postfix percent: only when nothing that could be an operand
			// follows (otherwise it is the modulo operator).
			p.advance()
			n = &Unary{Op: "%", X: n, Post: true}
			continue
		}
		// x^2 attaches here so that -x^2 parses as -(x^2).
		if t.kind == tOp && t.text == "^" {
			p.advance()
			exp, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			n = &Binary{Op: "^", L: n, R: exp}
			continue
		}
		return n, nil
	}
}

func (p *parser) parsePrimary() (Node, error) {
	t := p.cur()
	switch t.kind {
	case tNum:
		p.advance()
		return &Num{Val: t.num}, nil

	case tLParen:
		p.advance()
		inner, err := p.parseRelation()
		if err != nil {
			return nil, err
		}
		if p.cur().kind != tRParen {
			return nil, &Error{Msg: "missing ')'", Pos: p.cur().pos}
		}
		p.advance()
		return inner, nil

	case tPipe:
		p.advance()
		p.pipeDepth++
		inner, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if p.cur().kind != tPipe {
			return nil, &Error{Msg: "missing '|'", Pos: p.cur().pos}
		}
		p.pipeDepth--
		p.advance()
		return &Call{Name: "abs", Args: []Node{inner}}, nil

	case tIdent:
		name := t.text
		p.advance()
		// A bare known function name followed by an operand: "sqrt 2",
		// "sin pi", "ln e" — one implicit argument, no parentheses.
		if p.cur().kind != tLParen && IsFunc(name) && startsPrimary(p.cur()) &&
			!(p.cur().kind == tPipe && p.pipeDepth > 0) {
			arg, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			return &Call{Name: strings.ToLower(name), Args: []Node{arg}}, nil
		}
		if p.cur().kind == tLParen {
			p.advance()
			var args []Node
			if p.cur().kind != tRParen {
				for {
					a, err := p.parseRelation()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.cur().kind == tComma {
						p.advance()
						continue
					}
					break
				}
			}
			if p.cur().kind != tRParen {
				return nil, &Error{Msg: "missing ')' in call to " + name, Pos: p.cur().pos}
			}
			p.advance()
			return &Call{Name: strings.ToLower(name), Args: args}, nil
		}
		return &Var{Name: canonVarName(name)}, nil
	}
	return nil, &Error{Msg: "expected a value, got " + describe(t), Pos: t.pos}
}

// canonVarName folds the handful of spelled-out constant names so eval and
// deriv see a single spelling.
func canonVarName(s string) string {
	switch strings.ToLower(s) {
	case "pi":
		return "pi"
	case "tau":
		return "tau"
	case "e":
		return "e"
	case "phi":
		return "phi"
	case "inf", "infinity":
		return "inf"
	case "nan", "undefined":
		return "nan"
	}
	return s
}
