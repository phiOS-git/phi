package query

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// CalculatorProvider evaluates arithmetic expressions and simple unit
// conversions entirely locally (S-33 AGENT: "fully local expression
// evaluator with unit conversion"). Two forms:
//
//	a bare expression:  "2 + 2 * 3", "(4 - 1) ^ 2", "-3.5 / 2"
//	a conversion:       "<number> <unit> to|in <unit>", e.g. "10 km to mi"
//
// The unit list is deliberately small — temperature, length and mass, the
// obviously everyday ones no plan document had to name for this to be
// worth building — not an attempt at a general units database, which
// belongs to a real library, not a hand-rolled parser.
type CalculatorProvider struct{}

func (CalculatorProvider) Name() string { return "calculator" }

func (p CalculatorProvider) Query(_ context.Context, q string) []Result {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}

	if conv, ok := parseConversion(q); ok {
		out, err := convertUnit(conv)
		if err != nil {
			return nil
		}
		text := formatNumber(out)
		return []Result{{
			ID: "calc:" + q, Provider: p.Name(),
			Title: text + " " + conv.toUnit, Subtitle: q, Score: 100,
			Action: Action{Kind: ActionCopyText, Data: map[string]string{"text": text}},
		}}
	}

	val, err := evalExpr(q)
	if err != nil {
		return nil
	}
	text := formatNumber(val)
	return []Result{{
		ID: "calc:" + q, Provider: p.Name(),
		Title: text, Subtitle: q, Score: 100,
		Action: Action{Kind: ActionCopyText, Data: map[string]string{"text": text}},
	}}
}

func formatNumber(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'g', 10, 64)
}

// --- Arithmetic: a small recursive-descent parser --------------------------
//
// expr   := term (('+' | '-') term)*
// term   := unary (('*' | '/' | '%') unary)*
// unary  := '-' unary | power
// power  := atom ('^' unary)*        (right-associative)
// atom   := number | '(' expr ')'

type exprParser struct {
	src []rune
	pos int
}

func evalExpr(s string) (float64, error) {
	p := &exprParser{src: []rune(s)}
	v, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if p.pos != len(p.src) {
		return 0, fmt.Errorf("unexpected input at %d", p.pos)
	}
	return v, nil
}

func (p *exprParser) skipSpace() {
	for p.pos < len(p.src) && unicode.IsSpace(p.src[p.pos]) {
		p.pos++
	}
}

func (p *exprParser) peek() rune {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *exprParser) parseExpr() (float64, error) {
	v, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		switch p.peek() {
		case '+':
			p.pos++
			rhs, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			v += rhs
		case '-':
			p.pos++
			rhs, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			v -= rhs
		default:
			return v, nil
		}
	}
}

func (p *exprParser) parseTerm() (float64, error) {
	v, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	for {
		switch p.peek() {
		case '*':
			p.pos++
			rhs, err := p.parseUnary()
			if err != nil {
				return 0, err
			}
			v *= rhs
		case '/':
			p.pos++
			rhs, err := p.parseUnary()
			if err != nil {
				return 0, err
			}
			if rhs == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			v /= rhs
		case '%':
			p.pos++
			rhs, err := p.parseUnary()
			if err != nil {
				return 0, err
			}
			if rhs == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			v = math.Mod(v, rhs)
		default:
			return v, nil
		}
	}
}

func (p *exprParser) parseUnary() (float64, error) {
	if p.peek() == '-' {
		p.pos++
		v, err := p.parseUnary()
		return -v, err
	}
	if p.peek() == '+' {
		p.pos++
		return p.parseUnary()
	}
	return p.parsePower()
}

func (p *exprParser) parsePower() (float64, error) {
	base, err := p.parseAtom()
	if err != nil {
		return 0, err
	}
	if p.peek() == '^' {
		p.pos++
		exp, err := p.parseUnary() // right-associative
		if err != nil {
			return 0, err
		}
		return math.Pow(base, exp), nil
	}
	return base, nil
}

func (p *exprParser) parseAtom() (float64, error) {
	if p.peek() == '(' {
		p.pos++
		v, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		if p.peek() != ')' {
			return 0, fmt.Errorf("expected ')'")
		}
		p.pos++
		return v, nil
	}
	return p.parseNumber()
}

func (p *exprParser) parseNumber() (float64, error) {
	p.skipSpace()
	start := p.pos
	for p.pos < len(p.src) && (unicode.IsDigit(p.src[p.pos]) || p.src[p.pos] == '.') {
		p.pos++
	}
	if p.pos == start {
		return 0, fmt.Errorf("expected a number at %d", p.pos)
	}
	return strconv.ParseFloat(string(p.src[start:p.pos]), 64)
}

// --- Unit conversion ---------------------------------------------------

type conversion struct {
	value            float64
	fromUnit, toUnit string
}

// parseConversion matches "<number> <unit> (to|in) <unit>". Not a match
// (ok == false) for anything else, including a bare number with no unit —
// that falls through to evalExpr instead.
func parseConversion(s string) (conversion, bool) {
	fields := strings.Fields(s)
	if len(fields) != 4 {
		return conversion{}, false
	}
	if fields[2] != "to" && fields[2] != "in" {
		return conversion{}, false
	}
	val, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return conversion{}, false
	}
	from := strings.ToLower(fields[1])
	to := strings.ToLower(fields[3])
	if !unitKnown(from) || !unitKnown(to) {
		return conversion{}, false
	}
	return conversion{value: val, fromUnit: from, toUnit: to}, true
}

// toBaseSI: multiply a unit's value by this to reach the group's base
// (metres for length, kilograms for mass). Temperature is handled
// separately — it is not a linear scale through zero.
var lengthToMetres = map[string]float64{
	"mm": 0.001, "cm": 0.01, "m": 1, "km": 1000,
	"in": 0.0254, "ft": 0.3048, "mi": 1609.344,
}
var massToKg = map[string]float64{
	"g": 0.001, "kg": 1, "lb": 0.45359237, "oz": 0.028349523125,
}
var temperatureUnits = map[string]bool{"c": true, "f": true, "k": true}

func unitKnown(u string) bool {
	if _, ok := lengthToMetres[u]; ok {
		return true
	}
	if _, ok := massToKg[u]; ok {
		return true
	}
	return temperatureUnits[u]
}

func convertUnit(c conversion) (float64, error) {
	if temperatureUnits[c.fromUnit] || temperatureUnits[c.toUnit] {
		if !temperatureUnits[c.fromUnit] || !temperatureUnits[c.toUnit] {
			return 0, fmt.Errorf("cannot mix temperature with %s/%s", c.fromUnit, c.toUnit)
		}
		return convertTemperature(c.value, c.fromUnit, c.toUnit)
	}
	if from, ok := lengthToMetres[c.fromUnit]; ok {
		to, ok := lengthToMetres[c.toUnit]
		if !ok {
			return 0, fmt.Errorf("cannot convert length to %s", c.toUnit)
		}
		return c.value * from / to, nil
	}
	if from, ok := massToKg[c.fromUnit]; ok {
		to, ok := massToKg[c.toUnit]
		if !ok {
			return 0, fmt.Errorf("cannot convert mass to %s", c.toUnit)
		}
		return c.value * from / to, nil
	}
	return 0, fmt.Errorf("unknown unit %s", c.fromUnit)
}

func convertTemperature(v float64, from, to string) (float64, error) {
	var celsius float64
	switch from {
	case "c":
		celsius = v
	case "f":
		celsius = (v - 32) * 5 / 9
	case "k":
		celsius = v - 273.15
	default:
		return 0, fmt.Errorf("unknown temperature unit %s", from)
	}
	switch to {
	case "c":
		return celsius, nil
	case "f":
		return celsius*9/5 + 32, nil
	case "k":
		return celsius + 273.15, nil
	default:
		return 0, fmt.Errorf("unknown temperature unit %s", to)
	}
}
