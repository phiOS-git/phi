package mathx

import (
	"math"
	"regexp"
	"strings"
)

// engine.go is the single entry point the launcher's calculator provider
// calls: Evaluate(text) -> *Report. It recognises both symbolic notation
// (derivative(...), integral(...), solve(...), plot(...)) and the natural
// phrasings a person types into a launcher ("derivative of x^2",
// "integrate sin x from 0 to pi", "5 choose 2", "20% of 150"), routes to
// the right pass, and returns a structured Report the CLI renders as text
// and the shell renders as a rich card.

// Row is one labelled value in a Report's Table (a converter's alternate
// units, a solver's multiple roots shown side by side).
type Row struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Report is the structured result of one calculator query.
type Report struct {
	Input     string    `json:"input"`
	Kind      string    `json:"kind"` // value|convert|derivative|integral|solve|plot|simplify|boolean
	Headline  string    `json:"headline"`
	Detail    string    `json:"detail,omitempty"`
	Steps     []string  `json:"steps,omitempty"`
	Solutions []string  `json:"solutions,omitempty"`
	Table     []Row     `json:"table,omitempty"`
	Plot      *PlotData `json:"plot,omitempty"`
	Exact     bool      `json:"exact,omitempty"`
	// CopyText is what the launcher's "copy" action puts on the clipboard
	// — the bare answer, not the decorated headline.
	CopyText string `json:"copyText"`
}

var (
	reDeriv    = regexp.MustCompile(`(?i)^\s*(?:d\s*/\s*d\s*([a-zA-Z])\s*|derivative\s+of\s+|differentiate\s+|deriv\s+of\s+)(.+)$`)
	reDerivAt  = regexp.MustCompile(`(?i)\s+at\s+([a-zA-Z]\w*)\s*=\s*(-?[0-9.]+)\s*$`)
	reIntFrom  = regexp.MustCompile(`(?i)^\s*(?:integrate|integral\s+of|int\s+of|∫)\s+(.+?)\s+(?:d([a-zA-Z])\s+)?from\s+(.+?)\s+to\s+(.+?)\s*$`)
	reIntIndef = regexp.MustCompile(`(?i)^\s*(?:integrate|integral\s+of|int\s+of|antiderivative\s+of|∫)\s+(.+?)\s*(?:d([a-zA-Z]))?\s*$`)
	reSolve    = regexp.MustCompile(`(?i)^\s*solve\s+(?:for\s+([a-zA-Z]\w*)\s+(?:in\s+)?)?(.+)$`)
	rePlot     = regexp.MustCompile(`(?i)^\s*(?:plot|graph|draw)\s+(.+)$`)
	reSimpl    = regexp.MustCompile(`(?i)^\s*(?:simplify|expand)\s+(.+)$`)
	reChoose   = regexp.MustCompile(`(?i)^\s*(-?[0-9.]+)\s+(choose|permute|pick)\s+(-?[0-9.]+)\s*$`)
	rePctOf    = regexp.MustCompile(`(?i)^\s*(.+?)\s*(?:%|\s+percent)\s+of\s+(.+)$`)
	rePctOn    = regexp.MustCompile(`(?i)^\s*(.+?)\s*(?:%|\s+percent)\s+(on|off)\s+(.+)$`)
	reSumFrom  = regexp.MustCompile(`(?i)^\s*(sum|product)\s+of\s+(.+?)\s+for\s+([a-zA-Z])\s*=\s*(.+?)\s+to\s+(.+?)\s*$`)
)

// Evaluate parses and dispatches text. It returns (nil, err) when the
// input is not something the calculator should answer — the provider
// treats that as "no result", not as an error to show.
func Evaluate(text string) (*Report, error) {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return nil, errf("empty")
	}

	// --- natural-language rewrites ------------------------------------

	if m := reChoose.FindStringSubmatch(raw); m != nil {
		fn := "ncr"
		if strings.EqualFold(m[2], "permute") || strings.EqualFold(m[2], "pick") {
			fn = "npr"
		}
		raw = fn + "(" + m[1] + "," + m[3] + ")"
	}
	if m := rePctOf.FindStringSubmatch(raw); m != nil {
		raw = "(" + m[1] + ")/100*(" + m[2] + ")"
	} else if m := rePctOn.FindStringSubmatch(raw); m != nil {
		sign := "+"
		if strings.EqualFold(m[2], "off") {
			sign = "-"
		}
		raw = "(" + m[3] + ")" + sign + "(" + m[3] + ")*(" + m[1] + ")/100"
	}

	if m := reSumFrom.FindStringSubmatch(raw); m != nil {
		verb := "sumfrom"
		if strings.EqualFold(m[1], "product") {
			verb = "prodfrom"
		}
		raw = verb + "(" + m[2] + "," + m[3] + "," + m[4] + "," + m[5] + ")"
	}

	if m := rePlot.FindStringSubmatch(raw); m != nil {
		return doPlot(raw, m[1], "", 0, 0)
	}
	if m := reSimpl.FindStringSubmatch(raw); m != nil {
		return doSimplify(raw, m[1], strings.HasPrefix(strings.ToLower(raw), "expand"))
	}
	if m := reIntFrom.FindStringSubmatch(raw); m != nil {
		return doDefiniteIntegral(raw, m[1], m[2], m[3], m[4])
	}
	if m := reSolve.FindStringSubmatch(raw); m != nil {
		return doSolve(raw, m[2], m[1])
	}
	if m := reDeriv.FindStringSubmatch(raw); m != nil {
		v := m[1]
		body := m[2]
		at := ""
		atExpr := reDerivAt.FindStringSubmatch(body)
		if atExpr != nil {
			body = reDerivAt.ReplaceAllString(body, "")
			v = atExpr[1]
			at = atExpr[2]
		}
		return doDerivative(raw, body, v, at)
	}
	if m := reIntIndef.FindStringSubmatch(raw); m != nil && looksLikeIntegral(raw) {
		return doIndefiniteIntegral(raw, m[1], m[2])
	}

	// --- parse and dispatch on the tree ------------------------------

	n, err := Parse(raw)
	if err != nil {
		return nil, err
	}

	switch t := n.(type) {
	case *Compare:
		return doSolve(raw, raw, "")
	case *Call:
		switch t.Name {
		case "derivative", "d":
			return doDerivativeNode(raw, t)
		case "integral", "integrate":
			return doIntegralNode(raw, t)
		case "solve":
			return doSolveNode(raw, t)
		case "plot":
			return doPlotNode(raw, t)
		case "simplify":
			return doSimplify(raw, t.Args[0].String(), false)
		case "expand":
			return doSimplify(raw, t.Args[0].String(), true)
		case "sumfrom", "prodfrom":
			return doRangeSum(raw, t)
		}
	}

	return evalNumeric(raw, n)
}

func looksLikeIntegral(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "integrate") || strings.HasPrefix(l, "integral") ||
		strings.HasPrefix(l, "int of") || strings.HasPrefix(l, "antiderivative") || strings.HasPrefix(s, "∫")
}

// --- numeric value ------------------------------------------------------

func evalNumeric(raw string, n Node) (*Report, error) {
	if _, ok := n.(*Call); ok {
		if c := n.(*Call); c.Name == "_and" {
			return doSolve(raw, raw, "")
		}
	}
	v, err := Eval(n, nil)
	if err == nil {
		if math.IsNaN(v) {
			return nil, errf("undefined")
		}
		r := &Report{Input: raw, Kind: "value", Headline: formatFloat(v), CopyText: formatFloat(v)}
		// A boolean-ish relation folded to 0/1.
		if _, isCmp := n.(*Compare); isCmp {
			r.Kind = "boolean"
			if v == 1 {
				r.Headline = "true"
			} else {
				r.Headline = "false"
			}
			r.CopyText = r.Headline
		}
		return r, nil
	}

	// Could not evaluate — if there is exactly one free variable and it is
	// not actually a unit name (a bare "5 km" is the converter's job, not
	// a plot of 5·km), the useful answer is a plot of it.
	fv := freeVars(n)
	if len(fv) == 1 && !UnitKnown(fv[0]) {
		return doPlot(raw, n.String(), fv[0], 0, 0)
	}
	return nil, err
}

// --- derivative -------------------------------------------------------

func doDerivative(raw, body, v, at string) (*Report, error) {
	expr, err := Parse(body)
	if err != nil {
		return nil, err
	}
	if v == "" {
		fv := freeVars(expr)
		if len(fv) == 0 {
			return &Report{Input: raw, Kind: "derivative", Headline: "0", CopyText: "0",
				Detail: "the derivative of a constant is 0"}, nil
		}
		v = fv[0]
	}
	d := Derivative(expr, v)
	r := &Report{
		Input: raw, Kind: "derivative", Exact: true,
		Headline: d.String(),
		Detail:   "d/d" + v + " [ " + Simplify(expr).String() + " ]",
		CopyText: d.String(),
		Steps: []string{
			"f(" + v + ") = " + Simplify(expr).String(),
			"Apply the sum, product, quotient and chain rules term by term.",
			"f'(" + v + ") = " + d.String(),
		},
	}
	if at != "" {
		x, perr := parseFloatLoose(at)
		if perr == nil {
			if val, e := Eval(d, Env{v: x}); e == nil {
				r.Solutions = []string{"f'(" + at + ") = " + formatFloat(val)}
				r.CopyText = formatFloat(val)
			}
		}
	}
	r.Plot = Plot(d, v, 0, 0)
	return r, nil
}

func doDerivativeNode(raw string, c *Call) (*Report, error) {
	if len(c.Args) < 1 {
		return nil, errf("derivative(expr, var)")
	}
	v := ""
	if len(c.Args) >= 2 {
		if vv, ok := c.Args[1].(*Var); ok {
			v = vv.Name
		}
	}
	at := ""
	if len(c.Args) == 3 {
		if x, err := Eval(c.Args[2], nil); err == nil {
			at = formatFloat(x)
		}
	}
	return doDerivative(raw, c.Args[0].String(), v, at)
}

// --- integrals -------------------------------------------------------

func doDefiniteIntegral(raw, body, v, aStr, bStr string) (*Report, error) {
	expr, err := Parse(body)
	if err != nil {
		return nil, err
	}
	if v == "" {
		fv := freeVars(expr)
		if len(fv) == 1 {
			v = fv[0]
		} else {
			v = "x"
		}
	}
	aN, err := Parse(aStr)
	if err != nil {
		return nil, err
	}
	bN, err := Parse(bStr)
	if err != nil {
		return nil, err
	}
	a, err := Eval(aN, nil)
	if err != nil {
		return nil, err
	}
	b, err := Eval(bN, nil)
	if err != nil {
		return nil, err
	}
	val, method, err := IntegrateDefinite(expr, v, a, b)
	if err != nil {
		return nil, err
	}
	r := &Report{
		Input: raw, Kind: "integral",
		Headline: formatFloat(val),
		Detail:   "∫ from " + formatFloat(a) + " to " + formatFloat(b) + " of " + Simplify(expr).String() + " d" + v,
		CopyText: formatFloat(val),
		Steps:    []string{"∫_" + formatFloat(a) + "^" + formatFloat(b) + " " + Simplify(expr).String() + " d" + v},
	}
	if anti, ok := IntegrateIndefinite(expr, v); ok {
		fa, _ := Eval(anti, Env{v: a})
		fb, _ := Eval(anti, Env{v: b})
		r.Steps = append(r.Steps,
			"Antiderivative  F("+v+") = "+anti.String(),
			"F("+formatFloat(b)+") − F("+formatFloat(a)+") = "+formatFloat(fb)+" − "+formatFloat(fa)+" = "+formatFloat(fb-fa))
		r.Exact = true
	} else {
		r.Steps = append(r.Steps, "Evaluated by "+method+".")
	}
	r.Plot = Plot(expr, v, a, b)
	return r, nil
}

func doIndefiniteIntegral(raw, body, v string) (*Report, error) {
	expr, err := Parse(body)
	if err != nil {
		return nil, err
	}
	if v == "" {
		fv := freeVars(expr)
		if len(fv) == 1 {
			v = fv[0]
		} else {
			v = "x"
		}
	}
	anti, ok := IntegrateIndefinite(expr, v)
	if !ok {
		return &Report{
			Input: raw, Kind: "integral",
			Headline: "no elementary antiderivative found",
			Detail:   "∫ " + Simplify(expr).String() + " d" + v,
			CopyText: "",
		}, nil
	}
	r := &Report{
		Input: raw, Kind: "integral", Exact: true,
		Headline: AntiderivativeString(anti),
		Detail:   "∫ " + Simplify(expr).String() + " d" + v,
		CopyText: AntiderivativeString(anti),
		Steps: []string{
			"∫ " + Simplify(expr).String() + " d" + v,
			"Integrate term by term / apply the standard forms.",
			"= " + AntiderivativeString(anti),
			"Check: d/d" + v + " [" + anti.String() + "] = " + Derivative(anti, v).String(),
		},
	}
	return r, nil
}

func doIntegralNode(raw string, c *Call) (*Report, error) {
	if len(c.Args) < 2 {
		return nil, errf("integral(expr, var[, a, b])")
	}
	v := ""
	if vv, ok := c.Args[1].(*Var); ok {
		v = vv.Name
	}
	if len(c.Args) == 4 {
		return doDefiniteIntegral(raw, c.Args[0].String(), v, c.Args[2].String(), c.Args[3].String())
	}
	return doIndefiniteIntegral(raw, c.Args[0].String(), v)
}

// --- solve ----------------------------------------------------------

func doSolve(raw, body, v string) (*Report, error) {
	n, err := Parse(body)
	if err != nil {
		return nil, err
	}
	return solveNode(raw, n, v)
}

func doSolveNode(raw string, c *Call) (*Report, error) {
	if len(c.Args) < 1 {
		return nil, errf("solve(equation[, var])")
	}
	v := ""
	if len(c.Args) >= 2 {
		if vv, ok := c.Args[1].(*Var); ok {
			v = vv.Name
		}
	}
	return solveNode(raw, c.Args[0], v)
}

func solveNode(raw string, n Node, v string) (*Report, error) {
	// _and(a<b, b<c) chained inequality: solve each and intersect textually.
	if c, ok := n.(*Call); ok && c.Name == "_and" && len(c.Args) == 2 {
		r1, e1 := Solve(c.Args[0], v)
		r2, e2 := Solve(c.Args[1], v)
		if e1 == nil && e2 == nil {
			return &Report{
				Input: raw, Kind: "solve",
				Headline:  r1.Interval + "  and  " + r2.Interval,
				Detail:    n.String(),
				Solutions: []string{r1.Interval, r2.Interval},
				CopyText:  r1.Interval + " and " + r2.Interval,
			}, nil
		}
	}

	sr, err := Solve(n, v)
	if err != nil {
		return nil, err
	}
	sols := compactStrings(sr.Solutions)
	head := strings.Join(sols, ",  ")
	if len(sols) > 3 {
		head = strings.Join(sols[:3], ",  ") + ",  …"
	}
	if sr.Kind == "inequality" {
		head = sr.Interval
	}
	if sr.Variable != "" && sr.Kind == "equation" && len(sr.Solutions) > 0 && sr.Solutions[0] != "no solution" && sr.Solutions[0] != "all real numbers" {
		head = sr.Variable + " = " + head
	}
	r := &Report{
		Input:     raw,
		Kind:      "solve",
		Headline:  head,
		Detail:    n.String(),
		Steps:     sr.Steps,
		Solutions: compactStrings(sr.Solutions),
		Exact:     sr.Exact,
		CopyText:  strings.Join(compactStrings(sr.Solutions), ", "),
	}
	// Plot the left-minus-right function with the roots marked.
	var f Node
	if cmp, ok := n.(*Compare); ok {
		f = Simplify(sub(clone(cmp.L), clone(cmp.R)))
	} else {
		f = Simplify(clone(n))
	}
	if v == "" {
		v = sr.Variable
	}
	if mentions(f, v) {
		r.Plot = Plot(f, v, 0, 0)
	}
	return r, nil
}

// --- plot / simplify / range-sum -----------------------------------

func doPlot(raw, body, v string, lo, hi float64) (*Report, error) {
	expr, err := Parse(body)
	if err != nil {
		return nil, err
	}
	pd := Plot(expr, v, lo, hi)
	return &Report{
		Input: raw, Kind: "plot",
		Headline: "y = " + pd.Expr,
		Detail:   "plot over " + pd.Var + " ∈ [" + formatFloat(pd.XMin) + ", " + formatFloat(pd.XMax) + "]",
		Plot:     pd,
		CopyText: pd.Expr,
	}, nil
}

func doPlotNode(raw string, c *Call) (*Report, error) {
	if len(c.Args) < 1 {
		return nil, errf("plot(expr[, xmin, xmax])")
	}
	lo, hi := 0.0, 0.0
	if len(c.Args) == 3 {
		lo, _ = Eval(c.Args[1], nil)
		hi, _ = Eval(c.Args[2], nil)
	}
	return doPlot(raw, c.Args[0].String(), "", lo, hi)
}

func doSimplify(raw, body string, expand bool) (*Report, error) {
	expr, err := Parse(body)
	if err != nil {
		return nil, err
	}
	s := Simplify(expr)
	r := &Report{
		Input: raw, Kind: "simplify", Exact: true,
		Headline: s.String(),
		Detail:   "simplify  " + expr.String(),
		CopyText: s.String(),
	}
	if v, e := Eval(s, nil); e == nil {
		r.Detail = expr.String() + "  ="
		r.Headline = formatFloat(v)
		r.CopyText = formatFloat(v)
	}
	return r, nil
}

func doRangeSum(raw string, c *Call) (*Report, error) {
	if len(c.Args) != 4 {
		return nil, errf("sum-of expects expr, var, from, to")
	}
	vv, ok := c.Args[1].(*Var)
	if !ok {
		return nil, errf("second argument must be the index variable")
	}
	lo, err := Eval(c.Args[2], nil)
	if err != nil {
		return nil, err
	}
	hi, err := Eval(c.Args[3], nil)
	if err != nil {
		return nil, err
	}
	product := c.Name == "prodfrom"
	val, err := rangeSum(c.Args[0], vv.Name, math.Round(lo), math.Round(hi), product)
	if err != nil {
		return nil, err
	}
	sym := "Σ"
	if product {
		sym = "Π"
	}
	return &Report{
		Input: raw, Kind: "value", Exact: true,
		Headline: formatFloat(val),
		Detail:   sym + " " + vv.Name + "=" + formatFloat(lo) + "→" + formatFloat(hi) + "  " + c.Args[0].String(),
		CopyText: formatFloat(val),
	}, nil
}

func compactStrings(xs []string) []string {
	var out []string
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			out = append(out, x)
		}
	}
	return out
}
