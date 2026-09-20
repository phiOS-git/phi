package query

import (
	"context"
	"strings"

	"phi/internal/mathx"
)

// CalculatorProvider answers arithmetic, math and conversions locally via
// internal/mathx. Tries conversion first, then calculation. Runs async to
// avoid blocking providerTimeout.
type CalculatorProvider struct{}

func (CalculatorProvider) Name() string { return "calculator" }

func (p CalculatorProvider) Query(ctx context.Context, q string) []Result {
	q = strings.TrimSpace(q)
	if q == "" || len(q) > 512 {
		return nil
	}
	// runner-bar prefix feature: "math <expr>" is an
	// explicit request to evaluate <expr>. "convert <expr>" needs no
	// stripping here — mathx.ParseConversion already strips its own
	// leading "convert " (reConvertLead, internal/mathx/convert.go). The
	// q[5:] slice below is never empty when this matches: q was already
	// trimmed above, so it cannot itself end in the space "math "'s last
	// character requires, meaning at least one more character always
	// follows it.
	if len(q) >= 5 && strings.EqualFold(q[:5], "math ") {
		q = strings.TrimSpace(q[5:])
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
