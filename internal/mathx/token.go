package mathx

import (
	"strings"
	"unicode"
)

// The lexer for the calculator. It is intentionally forgiving about
// notation the launcher's users actually type: Unicode operators
// (× ÷ − √ π), superscripts (x² → x^2), "**" for power, "|x|" for absolute
// value, thousands separators (1,000,000), underscores in numbers
// (1_000), and a leading "." on a fraction (.5). Everything unusual is
// folded to a canonical ASCII form here so the parser sees a small,
// regular token stream.

type tokKind int

const (
	tEOF tokKind = iota
	tNum
	tIdent
	tOp // + - * / % ^ ! = < > <= >= != , unary handled by the parser
	tLParen
	tRParen
	tComma
	tPipe // | ... | absolute value
)

type token struct {
	kind tokKind
	text string
	num  float64
	pos  int
}

// preprocess folds Unicode and shorthand notation to ASCII before
// tokenizing.
func preprocess(s string) string {
	repl := strings.NewReplacer(
		"×", "*", "·", "*", "∙", "*", "⋅", "*",
		"÷", "/", "∕", "/", "⁄", "/",
		"−", "-", "–", "-", "—", "-",
		"√", " sqrt ", "∛", " cbrt ",
		"π", " pi ", "τ", " tau ", "∞", " inf ", "ϕ", " phi ", "φ", " phi ",
		"≤", "<=", "≥", ">=", "≠", "!=", "⩽", "<=", "⩾", ">=",
		"**", "^",
		"⁰", "^0", "¹", "^1", "²", "^2", "³", "^3", "⁴", "^4",
		"⁵", "^5", "⁶", "^6", "⁷", "^7", "⁸", "^8", "⁹", "^9",
		"→", " to ", "➜", " to ", "⇒", " to ",
	)
	return repl.Replace(s)
}

// stripThousands removes a comma or space that sits strictly between
// digits and is followed by exactly three more digits — "1,000,000" and
// "1 000 000" become "1000000" — without touching a comma used as an
// argument separator ("max(1, 2)").
func stripThousands(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i := 0; i < len(r); i++ {
		c := r[i]
		if (c == ',' || c == ' ') && i > 0 && unicode.IsDigit(r[i-1]) {
			if i+3 < len(r)+1 && i+1 < len(r) && unicode.IsDigit(r[i+1]) {
				// need at least 3 digits following, and not a 4th
				j := i + 1
				count := 0
				for j < len(r) && unicode.IsDigit(r[j]) {
					count++
					j++
				}
				if count == 3 && (j >= len(r) || !unicode.IsDigit(r[j])) {
					continue // drop the separator
				}
			}
		}
		b.WriteRune(c)
	}
	return b.String()
}

type lexer struct {
	src []rune
	pos int
}

func lex(input string) ([]token, error) {
	input = stripThousands(preprocess(input))
	l := &lexer{src: []rune(input)}
	var toks []token
	for {
		t, err := l.next()
		if err != nil {
			return nil, err
		}
		toks = append(toks, t)
		if t.kind == tEOF {
			return toks, nil
		}
	}
}

func (l *lexer) next() (token, error) {
	for l.pos < len(l.src) && unicode.IsSpace(l.src[l.pos]) {
		l.pos++
	}
	if l.pos >= len(l.src) {
		return token{kind: tEOF, pos: l.pos}, nil
	}
	start := l.pos
	c := l.src[l.pos]

	switch {
	case c == '(' || c == '[' || c == '{':
		l.pos++
		return token{kind: tLParen, text: "(", pos: start}, nil
	case c == ')' || c == ']' || c == '}':
		l.pos++
		return token{kind: tRParen, text: ")", pos: start}, nil
	case c == ',' || c == ';':
		l.pos++
		return token{kind: tComma, text: ",", pos: start}, nil
	case c == '|':
		l.pos++
		return token{kind: tPipe, text: "|", pos: start}, nil
	case c == '.' || unicode.IsDigit(c):
		return l.number()
	case isIdentStart(c):
		for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
			l.pos++
		}
		return token{kind: tIdent, text: string(l.src[start:l.pos]), pos: start}, nil
	}

	// Operators, including the two-character comparisons.
	two := ""
	if l.pos+1 < len(l.src) {
		two = string(l.src[l.pos : l.pos+2])
	}
	switch two {
	case "<=", ">=", "==", "!=":
		l.pos += 2
		op := two
		if op == "==" {
			op = "="
		}
		return token{kind: tOp, text: op, pos: start}, nil
	}
	switch c {
	case '+', '-', '*', '/', '%', '^', '!', '=', '<', '>':
		l.pos++
		return token{kind: tOp, text: string(c), pos: start}, nil
	}
	return token{}, &Error{Msg: "unexpected character " + string(c), Pos: start}
}

func (l *lexer) number() (token, error) {
	start := l.pos
	seenDot := false
	seenExp := false
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case unicode.IsDigit(c):
			l.pos++
		case c == '_':
			l.pos++ // digit grouping, dropped below
		case c == '.' && !seenDot && !seenExp:
			seenDot = true
			l.pos++
		case (c == 'e' || c == 'E') && !seenExp && l.pos > start:
			// only an exponent if followed by a digit or a signed digit
			if l.pos+1 < len(l.src) && (unicode.IsDigit(l.src[l.pos+1]) ||
				((l.src[l.pos+1] == '+' || l.src[l.pos+1] == '-') && l.pos+2 < len(l.src) && unicode.IsDigit(l.src[l.pos+2]))) {
				seenExp = true
				l.pos++
				if l.src[l.pos] == '+' || l.src[l.pos] == '-' {
					l.pos++
				}
			} else {
				goto done
			}
		default:
			goto done
		}
	}
done:
	raw := strings.ReplaceAll(string(l.src[start:l.pos]), "_", "")
	v, err := parseFloatLoose(raw)
	if err != nil {
		return token{}, &Error{Msg: "bad number " + raw, Pos: start}
	}
	return token{kind: tNum, text: raw, num: v, pos: start}, nil
}

func isIdentStart(c rune) bool {
	return unicode.IsLetter(c) || c == '_' || (c >= 0x0391 && c <= 0x03C9) // Greek block
}
func isIdentPart(c rune) bool {
	return isIdentStart(c) || unicode.IsDigit(c)
}
