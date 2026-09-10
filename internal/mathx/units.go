package mathx

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Unit conversion for the launcher's converter (phi query). The old
// hand-rolled table in internal/query/calculator.go covered seven lengths,
// four masses and three temperatures and demanded exactly
// "<number> <unit> to <unit>" — four whitespace-separated tokens, so
// "100km to m" did not parse. This package replaces it with a real, if
// still finite, registry: every SI base and the common derived quantities,
// each metric unit auto-expanded across the full SI prefix range, a broad
// alias list (symbols, spelled-out names, plurals, "sq m" / "m2" / "m^2"),
// and a tolerant "<number><unit> (to|in|as|into|->) <unit>" grammar that
// also accepts a glued number, an implicit value of 1, and a bare
// "<unit> to <unit>".
//
// It is deliberately NOT a general dimensional-analysis engine: no
// arbitrary compound units ("kg*m/s^2" as text), no offset+scale mixing
// beyond temperature. Those belong to a dedicated library; this is the
// everyday set a launcher is asked for.

// Dimension is the physical quantity a unit measures. Conversion is only
// defined between units of the same Dimension.
type Dimension string

const (
	DimLength      Dimension = "length"
	DimMass        Dimension = "mass"
	DimTime        Dimension = "time"
	DimTemperature Dimension = "temperature"
	DimArea        Dimension = "area"
	DimVolume      Dimension = "volume"
	DimSpeed       Dimension = "speed"
	DimAcceler     Dimension = "acceleration"
	DimForce       Dimension = "force"
	DimPressure    Dimension = "pressure"
	DimEnergy      Dimension = "energy"
	DimPower       Dimension = "power"
	DimFrequency   Dimension = "frequency"
	DimAngle       Dimension = "angle"
	DimData        Dimension = "data"
	DimDataRate    Dimension = "data rate"
	DimCurrent     Dimension = "electric current"
	DimVoltage     Dimension = "voltage"
	DimResistance  Dimension = "resistance"
	DimCharge      Dimension = "electric charge"
	DimIlluminance Dimension = "illuminance"
	DimFuel        Dimension = "fuel economy"
)

// unitDef converts to and from the dimension's base unit with the linear
// map base = value*factor + offset. offset is zero for every dimension
// except temperature, which is the one everyday scale that does not pass
// through a common zero.
type unitDef struct {
	dim    Dimension
	factor float64
	offset float64
	canon  string // the name printed back to the user
}

// registry is the fully expanded lookup table: every alias, and every
// SI-prefixed form of every prefixable unit, mapped to its unitDef. Built
// once, lazily, on first use — a launcher process is short-lived but a
// single query can touch it several times.
var registry map[string]unitDef

// siPrefixes is the full modern set (BIPM, 2022 revision included). value
// is the power of ten the prefix multiplies by.
var siPrefixes = []struct {
	sym, name string
	value     float64
}{
	{"Q", "quetta", 1e30}, {"R", "ronna", 1e27}, {"Y", "yotta", 1e24},
	{"Z", "zetta", 1e21}, {"E", "exa", 1e18}, {"P", "peta", 1e15},
	{"T", "tera", 1e12}, {"G", "giga", 1e9}, {"M", "mega", 1e6},
	{"k", "kilo", 1e3}, {"h", "hecto", 1e2}, {"da", "deka", 1e1},
	{"d", "deci", 1e-1}, {"c", "centi", 1e-2}, {"m", "milli", 1e-3},
	{"u", "micro", 1e-6}, {"µ", "micro", 1e-6}, {"μ", "micro", 1e-6},
	{"n", "nano", 1e-9}, {"p", "pico", 1e-12}, {"f", "femto", 1e-15},
	{"a", "atto", 1e-18}, {"z", "zepto", 1e-21}, {"y", "yocto", 1e-24},
	{"r", "ronto", 1e-27}, {"q", "quecto", 1e-30},
}

// binPrefixes are the IEC binary prefixes, applied only to the data units.
var binPrefixes = []struct {
	sym, name string
	value     float64
}{
	{"Ki", "kibi", 1 << 10}, {"Mi", "mebi", 1 << 20}, {"Gi", "gibi", 1 << 30},
	{"Ti", "tebi", 1 << 40}, {"Pi", "pebi", 1 << 50}, {"Ei", "exbi", 1 << 60},
}

// prefixable is one metric unit that accepts the full SI prefix range. The
// aliases here are the UNPREFIXED forms; a prefixed alias is generated for
// every entry (km, kilometre, kilometer, ...).
type prefixable struct {
	dim     Dimension
	factor  float64 // unprefixed unit -> base
	canon   string  // unprefixed canonical name
	symbols []string
	names   []string // spelled out, singular; plural "s" is added automatically
	binary  bool     // also expand with binary prefixes (data units)
}

var prefixables = []prefixable{
	{dim: DimLength, factor: 1, canon: "m", symbols: []string{"m"}, names: []string{"meter", "metre"}},
	{dim: DimMass, factor: 1e-3, canon: "g", symbols: []string{"g"}, names: []string{"gram", "gramme"}},
	{dim: DimTime, factor: 1, canon: "s", symbols: []string{"s", "sec"}, names: []string{"second"}},
	{dim: DimVolume, factor: 1e-3, canon: "L", symbols: []string{"l", "L"}, names: []string{"liter", "litre"}},
	{dim: DimForce, factor: 1, canon: "N", symbols: []string{"N"}, names: []string{"newton"}},
	{dim: DimPressure, factor: 1, canon: "Pa", symbols: []string{"Pa"}, names: []string{"pascal"}},
	{dim: DimEnergy, factor: 1, canon: "J", symbols: []string{"J"}, names: []string{"joule"}},
	{dim: DimPower, factor: 1, canon: "W", symbols: []string{"W"}, names: []string{"watt"}},
	{dim: DimFrequency, factor: 1, canon: "Hz", symbols: []string{"Hz"}, names: []string{"hertz"}},
	{dim: DimCurrent, factor: 1, canon: "A", symbols: []string{"A"}, names: []string{"amp", "ampere", "ampère"}},
	{dim: DimVoltage, factor: 1, canon: "V", symbols: []string{"V"}, names: []string{"volt"}},
	{dim: DimResistance, factor: 1, canon: "Ω", symbols: []string{"ohm", "Ω"}, names: []string{"ohm"}},
	{dim: DimCharge, factor: 1, canon: "C", symbols: []string{"C"}, names: []string{"coulomb"}},
	{dim: DimIlluminance, factor: 1, canon: "lx", symbols: []string{"lx"}, names: []string{"lux"}},
	{dim: DimData, factor: 1, canon: "bit", symbols: []string{"b", "bit"}, names: []string{"bit"}, binary: true},
	{dim: DimData, factor: 8, canon: "byte", symbols: []string{"B", "byte"}, names: []string{"byte", "octet"}, binary: true},
	{dim: DimEnergy, factor: 1, canon: "Wh", symbols: []string{"Wh"}, names: []string{"watt-hour", "watthour"}}, // Wh, kWh, MWh via prefixes on the whole token
}

// fixedUnit is a non-metric unit (or a metric one that takes no prefix)
// listed explicitly with its factor to the dimension's base.
type fixedUnit struct {
	dim     Dimension
	factor  float64
	canon   string
	aliases []string
}

var fixedUnits = []fixedUnit{
	// length — imperial / US / typographic / astronomical
	{DimLength, 0.0254, "in", []string{"in", "inch", "inches", "\"", "″"}},
	{DimLength, 0.3048, "ft", []string{"ft", "foot", "feet", "'", "′"}},
	{DimLength, 0.9144, "yd", []string{"yd", "yard", "yards"}},
	{DimLength, 1609.344, "mi", []string{"mi", "mile", "miles"}},
	{DimLength, 1852, "nmi", []string{"nmi", "nauticalmile", "nauticalmiles", "NM"}},
	{DimLength, 0.01, "cm", nil}, // ensure common ones exist even if prefix parse is bypassed
	{DimLength, 0.001, "mm", nil},
	{DimLength, 1000, "km", nil},
	{DimLength, 1e-6, "µm", []string{"micron", "microns"}},
	{DimLength, 1e-10, "Å", []string{"angstrom", "angstroms", "ang"}},
	{DimLength, 149597870700, "AU", []string{"au", "astronomicalunit"}},
	{DimLength, 9.4607304725808e15, "ly", []string{"ly", "lightyear", "lightyears"}},
	{DimLength, 3.0856775814913673e16, "pc", []string{"pc", "parsec", "parsecs"}},
	{DimLength, 0.01905, "hand", []string{"hand", "hands"}},
	{DimLength, 201.168, "furlong", []string{"furlong", "furlongs"}},

	// mass
	{DimMass, 1, "kg", nil},
	{DimMass, 1e-3, "g", nil},
	{DimMass, 1e-6, "mg", nil},
	{DimMass, 0.45359237, "lb", []string{"lb", "lbs", "pound", "pounds", "#"}},
	{DimMass, 0.028349523125, "oz", []string{"oz", "ounce", "ounces"}},
	{DimMass, 0.0002834952, "dram", []string{"dram", "drams", "dr"}},
	{DimMass, 6.35029318, "st", []string{"st", "stone", "stones"}},
	{DimMass, 1000, "t", []string{"t", "tonne", "tonnes", "metricton", "ton"}},
	{DimMass, 907.18474, "uston", []string{"uston", "shortton"}},
	{DimMass, 1016.0469088, "ukton", []string{"ukton", "longton", "imperialton"}},
	{DimMass, 0.0000647989, "gr", []string{"grain", "grains"}},
	{DimMass, 0.0311034768, "ozt", []string{"ozt", "troyounce", "troyounces"}},
	{DimMass, 0.2e-3, "ct", []string{"ct", "carat", "carats"}},
	{DimMass, 1.66053906660e-27, "Da", []string{"da", "dalton", "daltons", "amu", "u"}},

	// time
	{DimTime, 1, "s", nil},
	{DimTime, 1e-3, "ms", nil},
	{DimTime, 1e-6, "µs", nil},
	{DimTime, 1e-9, "ns", nil},
	{DimTime, 60, "min", []string{"min", "mins", "minute", "minutes"}},
	{DimTime, 3600, "h", []string{"h", "hr", "hrs", "hour", "hours"}},
	{DimTime, 86400, "day", []string{"d", "day", "days"}},
	{DimTime, 604800, "week", []string{"wk", "week", "weeks"}},
	{DimTime, 2629800, "month", []string{"mo", "month", "months"}}, // average Gregorian month
	{DimTime, 31557600, "year", []string{"y", "yr", "yrs", "year", "years"}},
	{DimTime, 315576000, "decade", []string{"decade", "decades"}},
	{DimTime, 3155760000, "century", []string{"century", "centuries"}},

	// temperature — factor+offset, base is kelvin
	{DimTemperature, 1, "K", []string{"k", "kelvin", "kelvins"}},

	// area
	{DimArea, 1, "m²", []string{"m2", "m^2", "sqm", "sqmeter", "sqmetre", "squaremeter", "squaremetre"}},
	{DimArea, 1e-4, "cm²", []string{"cm2", "cm^2", "sqcm"}},
	{DimArea, 1e6, "km²", []string{"km2", "km^2", "sqkm"}},
	{DimArea, 0.09290304, "ft²", []string{"ft2", "ft^2", "sqft", "squarefoot", "squarefeet"}},
	{DimArea, 0.00064516, "in²", []string{"in2", "in^2", "sqin", "squareinch", "squareinches"}},
	{DimArea, 0.83612736, "yd²", []string{"yd2", "yd^2", "sqyd"}},
	{DimArea, 2589988.110336, "mi²", []string{"mi2", "mi^2", "sqmi", "squaremile", "squaremiles"}},
	{DimArea, 4046.8564224, "acre", []string{"acre", "acres"}},
	{DimArea, 10000, "ha", []string{"ha", "hectare", "hectares"}},
	{DimArea, 100, "are", []string{"are", "ares"}},

	// volume
	{DimVolume, 1, "m³", []string{"m3", "m^3", "cubicmeter", "cubicmetre", "cbm", "kL", "kiloliter", "kilolitre"}},
	{DimVolume, 1e-3, "L", nil},
	{DimVolume, 1e-6, "mL", []string{"cc", "cm3", "cm^3", "cubiccentimeter"}},
	{DimVolume, 0.028316846592, "ft³", []string{"ft3", "ft^3", "cuft", "cubicfoot", "cubicfeet"}},
	{DimVolume, 1.6387064e-5, "in³", []string{"in3", "in^3", "cuin", "cubicinch", "cubicinches"}},
	{DimVolume, 0.764554857984, "yd³", []string{"yd3", "yd^3", "cuyd", "cubicyard"}},
	{DimVolume, 0.003785411784, "gal", []string{"gal", "gallon", "gallons", "usgal"}},
	{DimVolume, 0.00454609, "ukgal", []string{"ukgal", "imperialgallon", "impgal"}},
	{DimVolume, 0.000946352946, "qt", []string{"qt", "quart", "quarts"}},
	{DimVolume, 0.000473176473, "pt", []string{"pt", "pint", "pints"}},
	{DimVolume, 0.0002365882365, "cup", []string{"cup", "cups"}},
	{DimVolume, 2.95735295625e-5, "floz", []string{"floz", "fluidounce", "fluidounces", "usfloz"}},
	{DimVolume, 1.478676478125e-5, "tbsp", []string{"tbsp", "tablespoon", "tablespoons"}},
	{DimVolume, 4.92892159375e-6, "tsp", []string{"tsp", "teaspoon", "teaspoons"}},
	{DimVolume, 0.158987294928, "bbl", []string{"bbl", "barrel", "barrels", "oilbarrel"}},

	// speed
	{DimSpeed, 1, "m/s", []string{"mps", "ms-1", "meterspersecond", "metrespersecond"}},
	{DimSpeed, 1.0 / 3.6, "km/h", []string{"kmh", "kph", "kmph", "kilometersperhour", "kilometresperhour"}},
	{DimSpeed, 0.44704, "mph", []string{"mph", "mi/h", "milesperhour"}},
	{DimSpeed, 0.514444444444, "kn", []string{"kn", "kt", "knot", "knots"}},
	{DimSpeed, 0.3048, "ft/s", []string{"fps", "ft/s", "feetpersecond"}},
	{DimSpeed, 340.29, "mach", []string{"mach"}}, // at sea level, ISA
	{DimSpeed, 299792458, "c", []string{"lightspeed", "speedoflight"}},

	// acceleration
	{DimAcceler, 1, "m/s²", []string{"m/s2", "m/s^2", "mps2"}},
	{DimAcceler, 9.80665, "g0", []string{"gee", "g-force", "gforce", "standardgravity"}},

	// force
	{DimForce, 1, "N", nil},
	{DimForce, 4.4482216152605, "lbf", []string{"lbf", "poundforce", "poundsforce"}},
	{DimForce, 1e-5, "dyn", []string{"dyn", "dyne", "dynes"}},
	{DimForce, 9.80665, "kgf", []string{"kgf", "kilogramforce", "kp", "kilopond"}},

	// pressure
	{DimPressure, 1, "Pa", nil},
	{DimPressure, 1000, "kPa", nil},
	{DimPressure, 100, "hPa", nil},
	{DimPressure, 1e6, "MPa", nil},
	{DimPressure, 100000, "bar", []string{"bar", "bars"}},
	{DimPressure, 100, "mbar", []string{"mbar", "millibar", "millibars"}},
	{DimPressure, 101325, "atm", []string{"atm", "atmosphere", "atmospheres"}},
	{DimPressure, 6894.757293168, "psi", []string{"psi", "poundspersquareinch"}},
	{DimPressure, 133.322387415, "mmHg", []string{"mmhg", "torr", "torrs"}},
	{DimPressure, 3386.389, "inHg", []string{"inhg", "inchesofmercury"}},

	// energy
	{DimEnergy, 1, "J", nil},
	{DimEnergy, 1000, "kJ", nil},
	{DimEnergy, 1e6, "MJ", nil},
	{DimEnergy, 4.184, "cal", []string{"cal", "calorie", "calories", "smallcalorie"}},
	{DimEnergy, 4184, "kcal", []string{"kcal", "kilocalorie", "kilocalories", "Cal", "foodcalorie", "foodcalories"}},
	{DimEnergy, 3600, "Wh", nil},
	{DimEnergy, 3.6e6, "kWh", []string{"kwh", "kilowatthour", "kilowatthours"}},
	{DimEnergy, 3.6e9, "MWh", []string{"mwh", "megawatthour"}},
	{DimEnergy, 1055.05585262, "BTU", []string{"btu", "britishthermalunit"}},
	{DimEnergy, 1.602176634e-19, "eV", []string{"ev", "electronvolt", "electronvolts"}},
	{DimEnergy, 1e-7, "erg", []string{"erg", "ergs"}},
	{DimEnergy, 4.184e9, "tonTNT", []string{"tontnt", "tonsoftnt"}},

	// power
	{DimPower, 1, "W", nil},
	{DimPower, 1000, "kW", nil},
	{DimPower, 1e6, "MW", nil},
	{DimPower, 1e9, "GW", nil},
	{DimPower, 745.699871582, "hp", []string{"hp", "horsepower"}},
	{DimPower, 735.49875, "PS", []string{"ps", "metrichorsepower"}},
	{DimPower, 0.29307107, "BTU/h", []string{"btu/h", "btuh", "btuperhour"}},

	// frequency
	{DimFrequency, 1, "Hz", nil},
	{DimFrequency, 1e3, "kHz", nil},
	{DimFrequency, 1e6, "MHz", nil},
	{DimFrequency, 1e9, "GHz", nil},
	{DimFrequency, 1.0 / 60.0, "rpm", []string{"rpm", "revolutionsperminute"}},
	{DimFrequency, 2 * math.Pi, "rad/s", []string{"rad/s", "radianspersecond"}},

	// angle
	{DimAngle, 1, "rad", []string{"rad", "radian", "radians"}},
	{DimAngle, math.Pi / 180, "°", []string{"deg", "degree", "degrees", "°"}},
	{DimAngle, math.Pi / 200, "gon", []string{"gon", "grad", "gradian", "gradians", "grade"}},
	{DimAngle, math.Pi / 648000, "arcsec", []string{"arcsec", "arcsecond", "arcseconds", "\""}},
	{DimAngle, math.Pi / 10800, "arcmin", []string{"arcmin", "arcminute", "arcminutes"}},
	{DimAngle, 2 * math.Pi, "turn", []string{"turn", "turns", "revolution", "revolutions", "rev"}},

	// data — bytes/bits handled as prefixables; keep explicit common ones
	// too, including the lowercase "mb"/"gb" spellings a launcher user
	// almost always means as bytes (not millibits).
	{DimData, 8, "B", []string{"byte", "bytes", "octet", "octets"}},
	{DimData, 8 * 1024, "KiB", []string{"kib", "kibibyte"}},
	{DimData, 8 * 1024 * 1024, "MiB", []string{"mib", "mebibyte"}},
	{DimData, 8 * 1024 * 1024 * 1024, "GiB", []string{"gib", "gibibyte"}},
	{DimData, 8 * 1024 * 1024 * 1024 * 1024, "TiB", []string{"tib", "tebibyte"}},
	{DimData, 8 * 1e3, "kB", []string{"kb", "kilobyte", "kilobytes"}},
	{DimData, 8 * 1e6, "MB", []string{"mb", "megabyte", "megabytes"}},
	{DimData, 8 * 1e9, "GB", []string{"gb", "gigabyte", "gigabytes"}},
	{DimData, 8 * 1e12, "TB", []string{"tb", "terabyte", "terabytes"}},
	{DimData, 8 * 1e15, "PB", []string{"pb", "petabyte", "petabytes"}},
	{DimData, 1, "bit", []string{"bit", "bits"}},
	{DimData, 1e3, "kbit", []string{"kbit", "kilobit", "kilobits", "kb-bit"}},
	{DimData, 1e6, "Mbit", []string{"mbit", "megabit", "megabits"}},
	{DimData, 1e9, "Gbit", []string{"gbit", "gigabit", "gigabits"}},

	// data rate
	{DimDataRate, 1, "bit/s", []string{"bps", "bit/s", "bitspersecond"}},
	{DimDataRate, 1e3, "kbit/s", []string{"kbps", "kbit/s"}},
	{DimDataRate, 1e6, "Mbit/s", []string{"mbps", "mbit/s"}},
	{DimDataRate, 1e9, "Gbit/s", []string{"gbps", "gbit/s"}},
	{DimDataRate, 8, "B/s", []string{"byte/s", "bytespersecond"}},
	{DimDataRate, 8e6, "MB/s", []string{"mbyte/s", "megabytespersecond"}},

	// fuel economy — base is metres per cubic metre (m/m³ = 1/(m²)); use
	// km per litre as base for readability. factor -> km/L.
	{DimFuel, 1, "km/L", []string{"kmpl", "km/l", "kilometersperliter"}},
	{DimFuel, 0.425143707, "mpg", []string{"mpg", "milespergallon", "usmpg"}},
	{DimFuel, 0.354006042, "mpgUK", []string{"mpguk", "milespergallonuk", "impmpg"}},
}

// invFuel marks the dimensions where a smaller number means "more" — L/100km
// is the odd one out and is handled as a special case in Convert.
var lPer100kmAliases = map[string]bool{
	"l/100km": true, "lper100km": true, "litersper100km": true, "litresper100km": true,
}

func buildRegistry() {
	registry = make(map[string]unitDef, 4096)

	put := func(name string, d unitDef) {
		key := normalizeUnit(name)
		if key == "" {
			return
		}
		if _, exists := registry[key]; !exists {
			registry[key] = d
		}
	}

	// Fixed units first so their canonical names are authoritative.
	for _, u := range fixedUnits {
		def := unitDef{dim: u.dim, factor: u.factor, offset: 0, canon: u.canon}
		put(u.canon, def)
		for _, a := range u.aliases {
			put(a, def)
		}
	}

	// Temperature: the two offset scales, added after the plain kelvin entry.
	registry[normalizeUnit("°C")] = unitDef{dim: DimTemperature, factor: 1, offset: 273.15, canon: "°C"}
	for _, a := range []string{"c", "°c", "celsius", "centigrade", "degc", "degreec", "degreescelsius"} {
		registry[normalizeUnit(a)] = registry[normalizeUnit("°C")]
	}
	registry[normalizeUnit("°F")] = unitDef{dim: DimTemperature, factor: 5.0 / 9.0, offset: 273.15 - 32.0*5.0/9.0, canon: "°F"}
	for _, a := range []string{"f", "fahrenheit", "degf", "degreef", "degreesfahrenheit"} {
		registry[normalizeUnit(a)] = registry[normalizeUnit("°F")]
	}
	registry[normalizeUnit("°R")] = unitDef{dim: DimTemperature, factor: 5.0 / 9.0, offset: 0, canon: "°R"}
	for _, a := range []string{"rankine", "degr"} {
		registry[normalizeUnit(a)] = registry[normalizeUnit("°R")]
	}

	// Prefixable metric units, across the whole SI (and IEC binary) range.
	for _, pu := range prefixables {
		base := unitDef{dim: pu.dim, factor: pu.factor, offset: 0, canon: pu.canon}
		put(pu.canon, base)
		for _, s := range pu.symbols {
			put(s, base)
		}
		for _, n := range pu.names {
			put(n, base)
			put(n+"s", base)
		}
		for _, p := range siPrefixes {
			d := unitDef{dim: pu.dim, factor: pu.factor * p.value, offset: 0, canon: p.sym + pu.canon}
			for _, s := range pu.symbols {
				put(p.sym+s, d)
			}
			for _, n := range pu.names {
				put(p.name+n, d)
				put(p.name+n+"s", d)
			}
		}
		if pu.binary {
			for _, p := range binPrefixes {
				d := unitDef{dim: pu.dim, factor: pu.factor * p.value, offset: 0, canon: p.sym + pu.canon}
				for _, s := range pu.symbols {
					put(p.sym+s, d)
				}
				for _, n := range pu.names {
					put(p.name+n, d)
					put(p.name+n+"s", d)
				}
			}
		}
	}

	// L/100km — inverse fuel economy, registered so LookupUnit finds it;
	// Convert special-cases the reciprocal.
	for a := range lPer100kmAliases {
		registry[normalizeUnit(a)] = unitDef{dim: DimFuel, factor: -1, offset: 0, canon: "L/100km"}
	}
}

// normalizeUnit lowercases (except the micro sign and a few case-significant
// symbols are already folded by the alias lists), strips spaces and a
// trailing plural "s" is NOT stripped here — the alias lists carry plurals
// explicitly so "ms" (millisecond) is never mistaken for a plural of "m".
func normalizeUnit(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	// Common typographic variants folded to ASCII where unambiguous.
	s = strings.ReplaceAll(s, "μ", "µ")
	lower := strings.ToLower(s)
	// Keep the exact form for the case-significant data symbols (B bytes
	// vs b bits) and kelvin (K) vs the kilo prefix. Everything else folds
	// to lower case — a launcher user writing "C", "N", "W" means the
	// everyday unit, not a rarely-wanted homograph.
	switch s {
	case "B", "KiB", "MiB", "GiB", "TiB", "PiB", "kB", "MB", "GB", "TB", "PB", "K":
		return s
	}
	return lower
}

func ensureRegistry() {
	if registry == nil {
		buildRegistry()
	}
}

// LookupUnit resolves a unit name (any registered alias, any SI-prefixed
// form) to its definition.
func lookupUnit(name string) (unitDef, bool) {
	ensureRegistry()
	if d, ok := registry[normalizeUnit(name)]; ok {
		return d, true
	}
	// Retry with a stripped trailing plural, but only for names long
	// enough that this cannot eat a real one-letter symbol.
	n := normalizeUnit(name)
	if len(n) > 3 && strings.HasSuffix(n, "s") {
		if d, ok := registry[n[:len(n)-1]]; ok {
			return d, true
		}
	}
	return unitDef{}, false
}

// UnitKnown reports whether name resolves to any registered unit.
func UnitKnown(name string) bool {
	_, ok := lookupUnit(name)
	return ok
}

// Convert converts value from one unit to another. It returns the numeric
// result, the shared Dimension, and the canonical spellings of the two
// units (for echoing back a clean "X to Y").
func Convert(value float64, from, to string) (result float64, dim Dimension, fromCanon, toCanon string, err error) {
	f, ok := lookupUnit(from)
	if !ok {
		return 0, "", "", "", fmt.Errorf("unknown unit %q", from)
	}
	t, ok := lookupUnit(to)
	if !ok {
		return 0, "", "", "", fmt.Errorf("unknown unit %q", to)
	}
	if f.dim != t.dim {
		return 0, "", "", "", fmt.Errorf("cannot convert %s (%s) to %s (%s)", from, f.dim, to, t.dim)
	}

	// Fuel economy: L/100km is reciprocal to km/L, so a plain linear map
	// does not work when exactly one side is L/100km.
	if f.dim == DimFuel {
		fromKmL := value * f.factor
		if f.factor < 0 { // value is L/100km
			if value == 0 {
				return 0, "", "", "", fmt.Errorf("division by zero")
			}
			fromKmL = 100.0 / value
		}
		if t.factor < 0 { // want L/100km
			if fromKmL == 0 {
				return 0, "", "", "", fmt.Errorf("division by zero")
			}
			return 100.0 / fromKmL, f.dim, f.canon, t.canon, nil
		}
		return fromKmL / t.factor, f.dim, f.canon, t.canon, nil
	}

	base := value*f.factor + f.offset
	out := (base - t.offset) / t.factor
	return out, f.dim, f.canon, t.canon, nil
}

// CommonTargets returns a small set of other units in the same dimension
// worth showing alongside a conversion (the rich result's table). It is a
// curated, per-dimension list, not "every unit we know".
func CommonTargets(dim Dimension, exclude ...string) []string {
	skip := map[string]bool{}
	for _, e := range exclude {
		if d, ok := lookupUnit(e); ok {
			skip[d.canon] = true
		}
	}
	var pool []string
	switch dim {
	case DimLength:
		pool = []string{"mm", "cm", "m", "km", "in", "ft", "yd", "mi"}
	case DimMass:
		pool = []string{"mg", "g", "kg", "t", "oz", "lb", "st"}
	case DimTime:
		pool = []string{"ms", "s", "min", "h", "day", "week", "year"}
	case DimTemperature:
		pool = []string{"°C", "°F", "K"}
	case DimArea:
		pool = []string{"cm²", "m²", "km²", "ft²", "acre", "ha"}
	case DimVolume:
		pool = []string{"mL", "L", "m³", "tsp", "tbsp", "cup", "floz", "pt", "gal"}
	case DimSpeed:
		pool = []string{"m/s", "km/h", "mph", "kn", "ft/s"}
	case DimPressure:
		pool = []string{"Pa", "kPa", "bar", "atm", "psi", "mmHg"}
	case DimEnergy:
		pool = []string{"J", "kJ", "cal", "kcal", "Wh", "kWh", "BTU"}
	case DimPower:
		pool = []string{"W", "kW", "hp", "PS"}
	case DimData:
		pool = []string{"bit", "byte", "kB", "MB", "GB", "KiB", "MiB", "GiB"}
	case DimAngle:
		pool = []string{"rad", "°", "gon", "turn"}
	case DimFrequency:
		pool = []string{"Hz", "kHz", "MHz", "GHz", "rpm"}
	default:
		return nil
	}
	out := make([]string, 0, len(pool))
	for _, p := range pool {
		if !skip[p] {
			out = append(out, p)
		}
	}
	return out
}

// AllUnitNames is a sorted, de-duplicated list of every canonical unit
// name, for `phi query`'s own --help / a future `phi convert --list`.
func AllUnitNames() []string {
	ensureRegistry()
	seen := map[string]bool{}
	for _, d := range registry {
		seen[d.canon] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
