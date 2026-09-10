package mathx

import (
	"math"
	"strings"
)

// Numeric evaluation of a Node tree against an environment of variable
// bindings. Constants (pi, e, ...) and the standard function library live
// here. Everything is float64; complex results surface only from the
// quadratic solver, which formats them itself.

var constants = map[string]float64{
	"pi":  math.Pi,
	"tau": 2 * math.Pi,
	"e":   math.E,
	"phi": math.Phi,
	"inf": math.Inf(1),
	"nan": math.NaN(),
}

// Env is a set of variable bindings for one evaluation.
type Env map[string]float64

func (e Env) lookup(name string) (float64, bool) {
	if e != nil {
		if v, ok := e[name]; ok {
			return v, true
		}
	}
	v, ok := constants[name]
	return v, ok
}

// Eval numerically evaluates n. Unknown identifiers are an error unless
// they are in env.
func Eval(n Node, env Env) (float64, error) {
	switch t := n.(type) {
	case *Num:
		return t.Val, nil

	case *Var:
		if v, ok := env.lookup(t.Name); ok {
			return v, nil
		}
		return 0, errf("unknown symbol %q", t.Name)

	case *Unary:
		x, err := Eval(t.X, env)
		if err != nil {
			return 0, err
		}
		switch t.Op {
		case "-":
			return -x, nil
		case "+":
			return x, nil
		case "!":
			return factorial(x)
		case "%":
			return x / 100, nil
		}
		return 0, errf("bad unary %q", t.Op)

	case *Binary:
		l, err := Eval(t.L, env)
		if err != nil {
			return 0, err
		}
		r, err := Eval(t.R, env)
		if err != nil {
			return 0, err
		}
		switch t.Op {
		case "+":
			return l + r, nil
		case "-":
			return l - r, nil
		case "*":
			return l * r, nil
		case "/":
			if r == 0 {
				return 0, errf("division by zero")
			}
			return l / r, nil
		case "%":
			if r == 0 {
				return 0, errf("division by zero")
			}
			return math.Mod(l, r), nil
		case "^":
			return powFloat(l, r)
		}
		return 0, errf("bad operator %q", t.Op)

	case *Compare:
		l, err := Eval(t.L, env)
		if err != nil {
			return 0, err
		}
		r, err := Eval(t.R, env)
		if err != nil {
			return 0, err
		}
		if boolCompare(t.Op, l, r) {
			return 1, nil
		}
		return 0, nil

	case *Call:
		return evalCall(t, env)
	}
	return 0, errf("cannot evaluate %T", n)
}

func boolCompare(op string, l, r float64) bool {
	switch op {
	case "=":
		return l == r
	case "!=":
		return l != r
	case "<":
		return l < r
	case ">":
		return l > r
	case "<=":
		return l <= r
	case ">=":
		return l >= r
	}
	return false
}

func powFloat(base, exp float64) (float64, error) {
	if base < 0 && exp != math.Trunc(exp) {
		return 0, errf("negative base to a fractional power is complex")
	}
	return math.Pow(base, exp), nil
}

// funcArity maps every supported function to its accepted argument counts.
// -1 means variadic (min 1).
var funcArity = map[string][]int{
	"sin": {1}, "cos": {1}, "tan": {1}, "cot": {1}, "sec": {1}, "csc": {1},
	"asin": {1}, "acos": {1}, "atan": {1}, "atan2": {2},
	"sinh": {1}, "cosh": {1}, "tanh": {1}, "asinh": {1}, "acosh": {1}, "atanh": {1},
	"exp": {1}, "expm1": {1}, "ln": {1}, "log": {1, 2}, "log2": {1}, "log10": {1}, "lg": {1},
	"sqrt": {1}, "cbrt": {1}, "root": {2}, "abs": {1}, "sign": {1}, "sgn": {1},
	"floor": {1}, "ceil": {1}, "round": {1, 2}, "trunc": {1}, "frac": {1},
	"min": {-1}, "max": {-1}, "sum": {-1}, "avg": {-1}, "mean": {-1},
	"median": {-1}, "product": {-1}, "hypot": {2},
	"mod": {2}, "gcd": {-1}, "lcm": {-1},
	"factorial": {1}, "gamma": {1}, "fact": {1},
	"ncr": {2}, "npr": {2}, "choose": {2}, "comb": {2}, "perm": {2}, "binom": {2},
	"deg": {1}, "rad": {1}, "todeg": {1}, "torad": {1},
	"clamp": {3}, "lerp": {3},
	"re": {1}, "im": {1},
	"_and": {2},
	// distributions
	"npdf": {1, 3}, "ncdf": {1, 3}, "normpdf": {1, 3}, "normcdf": {1, 3},
	"binompmf": {3}, "binomcdf": {3}, "poissonpmf": {2}, "poissoncdf": {2},
	"erf": {1}, "erfc": {1},
	// declared for the parser/deriv/solve layers; not numerically special
	"derivative": {2, 3}, "integral": {3, 4}, "integrate": {3, 4},
	"solve": {1, 2}, "plot": {1, 3}, "simplify": {1}, "expand": {1}, "d": {2, 3},
	"P": {1}, "sumfrom": {4}, "prodfrom": {4},
}

// IsFunc reports whether name is a known function (used by the parser to
// allow "sqrt 2" without parentheses).
func IsFunc(name string) bool {
	_, ok := funcArity[strings.ToLower(name)]
	return ok
}

func evalCall(c *Call, env Env) (float64, error) {
	name := strings.ToLower(c.Name)
	arities, known := funcArity[name]
	if !known {
		return 0, errf("unknown function %q", c.Name)
	}
	if !arityOK(arities, len(c.Args)) {
		return 0, errf("%s: wrong number of arguments", name)
	}
	args := make([]float64, len(c.Args))
	for i, a := range c.Args {
		v, err := Eval(a, env)
		if err != nil {
			return 0, err
		}
		args[i] = v
	}
	x := 0.0
	if len(args) > 0 {
		x = args[0]
	}

	switch name {
	case "sin":
		return math.Sin(x), nil
	case "cos":
		return math.Cos(x), nil
	case "tan":
		return math.Tan(x), nil
	case "cot":
		return 1 / math.Tan(x), nil
	case "sec":
		return 1 / math.Cos(x), nil
	case "csc":
		return 1 / math.Sin(x), nil
	case "asin":
		return math.Asin(x), nil
	case "acos":
		return math.Acos(x), nil
	case "atan":
		return math.Atan(x), nil
	case "atan2":
		return math.Atan2(args[0], args[1]), nil
	case "sinh":
		return math.Sinh(x), nil
	case "cosh":
		return math.Cosh(x), nil
	case "tanh":
		return math.Tanh(x), nil
	case "asinh":
		return math.Asinh(x), nil
	case "acosh":
		return math.Acosh(x), nil
	case "atanh":
		return math.Atanh(x), nil
	case "exp":
		return math.Exp(x), nil
	case "expm1":
		return math.Expm1(x), nil
	case "ln":
		return math.Log(x), nil
	case "lg", "log2":
		return math.Log2(x), nil
	case "log10":
		return math.Log10(x), nil
	case "log":
		if len(args) == 2 {
			// log(x, b) = log base b of x
			return math.Log(args[0]) / math.Log(args[1]), nil
		}
		return math.Log10(x), nil // "log" with one arg is base 10, calculator convention
	case "sqrt":
		if x < 0 {
			return 0, errf("sqrt of a negative number is complex")
		}
		return math.Sqrt(x), nil
	case "cbrt":
		return math.Cbrt(x), nil
	case "root":
		return math.Pow(args[1], 1/args[0]), nil
	case "abs":
		return math.Abs(x), nil
	case "sign", "sgn":
		return sign(x), nil
	case "floor":
		return math.Floor(x), nil
	case "ceil":
		return math.Ceil(x), nil
	case "trunc":
		return math.Trunc(x), nil
	case "frac":
		return x - math.Trunc(x), nil
	case "round":
		if len(args) == 2 {
			p := math.Pow(10, args[1])
			return math.Round(x*p) / p, nil
		}
		return math.Round(x), nil
	case "min":
		return reduce(args, math.Inf(1), math.Min), nil
	case "max":
		return reduce(args, math.Inf(-1), math.Max), nil
	case "sum":
		s := 0.0
		for _, a := range args {
			s += a
		}
		return s, nil
	case "product":
		s := 1.0
		for _, a := range args {
			s *= a
		}
		return s, nil
	case "avg", "mean":
		s := 0.0
		for _, a := range args {
			s += a
		}
		return s / float64(len(args)), nil
	case "median":
		return median(args), nil
	case "hypot":
		return math.Hypot(args[0], args[1]), nil
	case "mod":
		return math.Mod(args[0], args[1]), nil
	case "gcd":
		return gcdAll(args), nil
	case "lcm":
		return lcmAll(args), nil
	case "factorial", "fact":
		return factorial(x)
	case "gamma":
		return math.Gamma(x), nil
	case "ncr", "choose", "comb", "binom":
		return combos(args[0], args[1])
	case "npr", "perm":
		return perms(args[0], args[1])
	case "deg", "todeg":
		return x * 180 / math.Pi, nil
	case "rad", "torad":
		return x * math.Pi / 180, nil
	case "clamp":
		return math.Max(args[1], math.Min(args[0], args[2])), nil
	case "lerp":
		return args[0] + (args[1]-args[0])*args[2], nil
	case "re":
		return x, nil
	case "im":
		return 0, nil
	case "erf":
		return math.Erf(x), nil
	case "erfc":
		return math.Erfc(x), nil
	case "_and":
		if args[0] != 0 && args[1] != 0 {
			return 1, nil
		}
		return 0, nil
	case "npdf", "normpdf":
		mu, sigma := 0.0, 1.0
		if len(args) == 3 {
			mu, sigma = args[1], args[2]
		}
		return normPDF(x, mu, sigma), nil
	case "ncdf", "normcdf":
		mu, sigma := 0.0, 1.0
		if len(args) == 3 {
			mu, sigma = args[1], args[2]
		}
		return normCDF(x, mu, sigma), nil
	case "binompmf":
		return binomPMF(args[0], args[1], args[2]), nil
	case "binomcdf":
		return binomCDF(args[0], args[1], args[2]), nil
	case "poissonpmf":
		return poissonPMF(args[0], args[1]), nil
	case "poissoncdf":
		return poissonCDF(args[0], args[1]), nil
	}
	return 0, errf("%s: not available in numeric context", name)
}

func arityOK(arities []int, n int) bool {
	for _, a := range arities {
		if a == -1 {
			return n >= 1
		}
		if a == n {
			return true
		}
	}
	return false
}

func reduce(xs []float64, init float64, f func(a, b float64) float64) float64 {
	acc := init
	for _, x := range xs {
		acc = f(acc, x)
	}
	return acc
}

func sign(x float64) float64 {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	default:
		return 0
	}
}
