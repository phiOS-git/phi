package query

import "phi/internal/mathx"

// RichResult is the optional structured payload a provider can attach to a
// Result when a single Title/Subtitle line cannot carry the answer — the
// calculator's solving steps, a solver's multiple roots, a converter's
// alternate units, a plot's sampled curve. The shell's Launcher renders it
// as an expanded card below the result row; a provider that sets nothing
// here still works exactly as before (the field is omitempty).
//
// It mirrors mathx.Report deliberately: the calculator is its only
// producer today, and keeping the JSON shape close to the engine's own
// output keeps the mapping trivial and the contract easy to read.
type RichResult struct {
	Kind      string    `json:"kind"`
	Headline  string    `json:"headline"`
	Detail    string    `json:"detail,omitempty"`
	Steps     []string  `json:"steps,omitempty"`
	Solutions []string  `json:"solutions,omitempty"`
	Table     []RichKV  `json:"table,omitempty"`
	Plot      *RichPlot `json:"plot,omitempty"`
	Exact     bool      `json:"exact,omitempty"`
}

// RichKV is one labelled value (an alternate unit, a named quantity).
type RichKV struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// RichPlot is a sampled curve plus its viewport, ready for the shell to
// draw without any further math.
type RichPlot struct {
	Expr   string            `json:"expr"`
	Var    string            `json:"var"`
	Points []mathx.PlotPoint `json:"points"`
	XMin   float64           `json:"xMin"`
	XMax   float64           `json:"xMax"`
	YMin   float64           `json:"yMin"`
	YMax   float64           `json:"yMax"`
	Roots  []float64         `json:"roots,omitempty"`
}

// richFromReport converts a mathx.Report into the launcher payload. It
// returns nil when the report carries nothing a plain Title/Subtitle line
// does not already say (a bare arithmetic value), so trivial answers stay
// lightweight.
func richFromReport(r *mathx.Report) *RichResult {
	if r == nil {
		return nil
	}
	hasExtra := len(r.Steps) > 0 || len(r.Solutions) > 0 || len(r.Table) > 0 || r.Plot != nil || r.Detail != ""
	if !hasExtra {
		return nil
	}
	out := &RichResult{
		Kind:      r.Kind,
		Headline:  r.Headline,
		Detail:    r.Detail,
		Steps:     r.Steps,
		Solutions: r.Solutions,
		Exact:     r.Exact,
	}
	for _, row := range r.Table {
		out.Table = append(out.Table, RichKV{Label: row.Label, Value: row.Value})
	}
	if r.Plot != nil {
		out.Plot = &RichPlot{
			Expr:   r.Plot.Expr,
			Var:    r.Plot.Var,
			Points: r.Plot.Points,
			XMin:   r.Plot.XMin, XMax: r.Plot.XMax,
			YMin: r.Plot.YMin, YMax: r.Plot.YMax,
			Roots: r.Plot.Roots,
		}
	}
	return out
}
