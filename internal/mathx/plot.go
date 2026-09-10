package mathx

import "math"

// Plot samples one function of one variable into a point series the shell
// can draw. It picks a domain when the caller does not give one, samples
// densely, splits the series at poles/large jumps so the renderer does not
// draw a vertical line through an asymptote, and reports a y-range clipped
// to the bulk of the data (so a single spike near a pole does not flatten
// the whole curve).

// PlotPoint is one sample. Break == true marks a gap the renderer should
// not connect across.
type PlotPoint struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Break bool    `json:"break,omitempty"`
}

// PlotData is a full plottable result.
type PlotData struct {
	Expr   string      `json:"expr"`
	Var    string      `json:"var"`
	Points []PlotPoint `json:"points"`
	XMin   float64     `json:"xMin"`
	XMax   float64     `json:"xMax"`
	YMin   float64     `json:"yMin"`
	YMax   float64     `json:"yMax"`
	Roots  []float64   `json:"roots,omitempty"`
}

// Plot builds a PlotData for f over [xmin, xmax]. If xmin == xmax the
// domain defaults to [-10, 10].
func Plot(f Node, variable string, xmin, xmax float64) *PlotData {
	if variable == "" {
		vs := freeVars(f)
		if len(vs) == 1 {
			variable = vs[0]
		} else {
			variable = "x"
		}
	}
	if xmin == xmax {
		xmin, xmax = -10, 10
	}
	if xmin > xmax {
		xmin, xmax = xmax, xmin
	}

	// 240 samples is smooth for the small chart the launcher draws and
	// keeps the JSON `phi query` emits on every keystroke reasonable.
	const n = 240
	step := (xmax - xmin) / float64(n)
	pts := make([]PlotPoint, 0, n+1)
	var ys []float64
	prevY := math.NaN()
	prevOK := false

	for i := 0; i <= n; i++ {
		x := xmin + float64(i)*step
		y, err := Eval(f, Env{variable: x})
		ok := err == nil && !math.IsNaN(y) && !math.IsInf(y, 0)
		p := PlotPoint{X: x, Y: y}
		if !ok {
			p.Y = 0
			p.Break = true
			prevOK = false
			pts = append(pts, p)
			continue
		}
		// Detect a jump far larger than the local trend -> treat as a
		// break (pole).
		if prevOK && math.Abs(y-prevY) > 1e3*(1+math.Abs(prevY)) {
			p.Break = true
		}
		ys = append(ys, y)
		prevY, prevOK = y, true
		pts = append(pts, p)
	}

	ymin, ymax := robustRange(ys)
	return &PlotData{
		Expr:   f.String(),
		Var:    variable,
		Points: pts,
		XMin:   xmin, XMax: xmax,
		YMin: ymin, YMax: ymax,
		Roots: rootsInRange(f, variable, xmin, xmax),
	}
}

// robustRange returns a y-range covering roughly the 2nd–98th percentile
// of the samples, padded, so an asymptote spike does not dominate.
func robustRange(ys []float64) (float64, float64) {
	if len(ys) == 0 {
		return -1, 1
	}
	s := append([]float64(nil), ys...)
	sortFloats(s)
	lo := s[int(0.02*float64(len(s)))]
	hi := s[min(len(s)-1, int(0.98*float64(len(s))))]
	if lo == hi {
		lo -= 1
		hi += 1
	}
	pad := (hi - lo) * 0.1
	lo, hi = lo-pad, hi+pad
	// Always show the x-axis if it is close.
	if lo > 0 && lo < hi*0.5 {
		lo = 0
	}
	if hi < 0 && hi > lo*0.5 {
		hi = 0
	}
	return lo, hi
}

func rootsInRange(f Node, v string, lo, hi float64) []float64 {
	fn := func(x float64) float64 {
		y, err := Eval(f, Env{v: x})
		if err != nil {
			return math.NaN()
		}
		return y
	}
	var roots []float64
	step := (hi - lo) / 400
	prev := fn(lo)
	for x := lo + step; x <= hi; x += step {
		cur := fn(x)
		if finite(prev) && finite(cur) && prev*cur < 0 {
			r := bisect(fn, x-step, x)
			roots = append(roots, r)
		}
		prev = cur
	}
	return roots
}

func sortFloats(s []float64) {
	// insertion sort — plot slices are small (<= 481) and this avoids a
	// sort import churn; std sort is used elsewhere.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
