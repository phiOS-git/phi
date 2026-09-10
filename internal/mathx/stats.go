package mathx

import (
	"math"
	"sort"
)

// Combinatorics and the three discrete/continuous distributions a
// launcher calculator is realistically asked for (normal, binomial,
// Poisson), plus gcd/lcm/median helpers. Everything is float64 and
// tolerant of non-integer input where that is meaningful (gamma for
// factorial), erroring only where it genuinely cannot answer.

func factorial(x float64) (float64, error) {
	if x < 0 {
		return 0, errf("factorial of a negative number is undefined")
	}
	if x == math.Trunc(x) && x <= 170 {
		r := 1.0
		for i := 2.0; i <= x; i++ {
			r *= i
		}
		return r, nil
	}
	if x == math.Trunc(x) {
		return math.Inf(1), nil // 171! overflows float64
	}
	// Non-integer: the gamma extension, x! = Γ(x+1).
	return math.Gamma(x + 1), nil
}

func combos(n, k float64) (float64, error) {
	if k < 0 || n < 0 {
		return 0, errf("nCr needs non-negative arguments")
	}
	if k > n {
		return 0, nil
	}
	if n == math.Trunc(n) && k == math.Trunc(k) {
		k = math.Min(k, n-k)
		r := 1.0
		for i := 0.0; i < k; i++ {
			r = r * (n - i) / (i + 1)
		}
		return math.Round(r), nil
	}
	ln, _ := math.Lgamma(n + 1)
	lk, _ := math.Lgamma(k + 1)
	lnk, _ := math.Lgamma(n - k + 1)
	return math.Exp(ln - lk - lnk), nil
}

func perms(n, k float64) (float64, error) {
	if k < 0 || n < 0 {
		return 0, errf("nPr needs non-negative arguments")
	}
	if k > n {
		return 0, nil
	}
	r := 1.0
	for i := 0.0; i < k; i++ {
		r *= n - i
	}
	return r, nil
}

func gcd2(a, b float64) float64 {
	a, b = math.Abs(math.Trunc(a)), math.Abs(math.Trunc(b))
	for b != 0 {
		a, b = b, math.Mod(a, b)
	}
	return a
}

func gcdAll(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	g := xs[0]
	for _, x := range xs[1:] {
		g = gcd2(g, x)
	}
	return math.Abs(math.Trunc(g))
}

func lcmAll(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	l := math.Abs(math.Trunc(xs[0]))
	for _, x := range xs[1:] {
		x = math.Abs(math.Trunc(x))
		g := gcd2(l, x)
		if g == 0 {
			return 0
		}
		l = l / g * x
	}
	return l
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// --- distributions -------------------------------------------------------

func normPDF(x, mu, sigma float64) float64 {
	if sigma <= 0 {
		return math.NaN()
	}
	z := (x - mu) / sigma
	return math.Exp(-0.5*z*z) / (sigma * math.Sqrt(2*math.Pi))
}

func normCDF(x, mu, sigma float64) float64 {
	if sigma <= 0 {
		return math.NaN()
	}
	return 0.5 * math.Erfc(-(x-mu)/(sigma*math.Sqrt2))
}

func binomPMF(k, n, p float64) float64 {
	k, n = math.Round(k), math.Round(n)
	if k < 0 || k > n || p < 0 || p > 1 {
		return 0
	}
	c, _ := combos(n, k)
	return c * math.Pow(p, k) * math.Pow(1-p, n-k)
}

func binomCDF(k, n, p float64) float64 {
	k = math.Floor(k)
	s := 0.0
	for i := 0.0; i <= k; i++ {
		s += binomPMF(i, n, p)
	}
	return s
}

func poissonPMF(k, lambda float64) float64 {
	k = math.Round(k)
	if k < 0 || lambda < 0 {
		return 0
	}
	lg, _ := math.Lgamma(k + 1)
	return math.Exp(k*math.Log(lambda) - lambda - lg)
}

func poissonCDF(k, lambda float64) float64 {
	k = math.Floor(k)
	s := 0.0
	for i := 0.0; i <= k; i++ {
		s += poissonPMF(i, lambda)
	}
	return s
}
