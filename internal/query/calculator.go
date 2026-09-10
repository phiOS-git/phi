package query

import (
	"context"
	"strings"

	"phi/internal/mathx"
)

// CalculatorProvider answers arithmetic, symbolic math and unit
// conversions entirely locally (S-33 AGENT: "fully local expression
// evaluator with unit conversion"). The engine is internal/mathx; this
// file is the thin launcher adapter — it decides conversion-vs-calculation,
// bounds the work by the query context, and maps a mathx.Report onto a
// Result plus its optional RichResult card.
//
// Two shapes, tried in order:
//
//	a unit conversion:  "100km to m", "-40 C to F", "2 GiB to MB", "5 km"
//	everything else:     "2+2*3", "sqrt(2)", "solve x^2-4=0",
//	                     "d/dx sin(x)", "integrate x^2 from 0 to 3",
//	                     "plot sin(x)", "40% of 250", "5 choose 2"
//
// Cold start (phi/CLAUDE.md): this runs on every keystroke. The engine's
// passes are all finite, but a pathological expression could still take
// longer than providerTimeout, so the actual evaluation runs in a
// goroutine and is abandoned if ctx fires first — the query never blocks
// on it.
type CalculatorProvider struct{}

func (CalculatorProvider) Name() string { return "calculator" }

func (p CalculatorProvider) Query(ctx context.Context, q string) []Result {
	q = strings.TrimSpace(q)
	if q == "" || len(q) > 512 {
		return nil
	}

	type outcome struct {
		results []Result
	}
	ch := make(chan outcome, 1)
	go func() {
		ch <- outcome{results: p.evaluate(q)}
	}()

	select {
	case <-ctx.Done():
		return nil
	case o := <-ch:
		return o.results
	}
}

func (p CalculatorProvider) evaluate(q string) []Result {
	// Conversion first — "100 km to m" must never be read as arithmetic.
	if rep, ok := mathx.ParseConversion(q); ok {
		return []Result{p.result(q, rep, "conv")}
	}

	rep, err := mathx.Evaluate(q)
	if err != nil || rep == nil {
		return nil
	}
	// A currency conversion (three-letter codes) is CurrencyProvider's
	// job, not ours — if mathx somehow produced a value for it, drop it so
	// the two providers do not both answer.
	if rep.Kind == "value" && looksLikeCurrencyQuery(q) {
		return nil
	}
	return []Result{p.result(q, rep, "calc")}
}

func (p CalculatorProvider) result(q string, rep *mathx.Report, idPrefix string) Result {
	title := rep.Headline
	subtitle := rep.Detail
	if subtitle == "" {
		subtitle = q
	}
	copyText := rep.CopyText
	if copyText == "" {
		copyText = rep.Headline
	}
	return Result{
		ID:       idPrefix + ":" + q,
		Provider: p.Name(),
		Title:    title,
		Subtitle: subtitle,
		Score:    100, // the calculator trusts its own confidence (rank.go)
		Action:   Action{Kind: ActionCopyText, Data: map[string]string{"text": copyText}},
		Rich:     richFromReport(rep),
	}
}

// looksLikeCurrencyQuery is a cheap guard so the calculator and
// CurrencyProvider never both answer "100 usd to eur". It only has to
// catch the shape CurrencyProvider itself accepts.
func looksLikeCurrencyQuery(q string) bool {
	fields := strings.Fields(strings.ToLower(q))
	if len(fields) != 4 {
		return false
	}
	if fields[2] != "to" && fields[2] != "in" {
		return false
	}
	return len(fields[1]) == 3 && len(fields[3]) == 3 &&
		isAlpha(fields[1]) && isAlpha(fields[3])
}

func isAlpha(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}
