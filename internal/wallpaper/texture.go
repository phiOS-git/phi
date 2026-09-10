// Package wallpaper generates the procedural texture overlays the shell's
// background layer composites over a solid colour (settings-overhaul batch
// D). The user's directive: a texture is picked from a small catalogue
// (grain, noise, paper, leather, rock, fabric …) with an intensity, and it
// is "generated once, not at runtime" — so this writes a finished RGBA PNG
// the shell just tiles at opacity 1, the strength already baked into the
// alpha channel.
//
// Output shape: mostly-transparent RGBA. A pixel is white where the noise
// is positive, black where it is negative, and its alpha is |noise| scaled
// by the intensity — exactly how a film-grain overlay works, so the shell
// needs no blend mode (which would mean a shader / GraphicalEffects, the
// surface Quickshell 0.3.x stability notes tell us to avoid).
//
// Determinism: the RNG is seeded from (mode, intensity, width, height) so
// the shell's cache key ($XDG_DATA_HOME/phi/textures/<mode>-<intensity>.png)
// is honest — the same inputs always produce the same file.
package wallpaper

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"math/rand"
	"sort"
	"strings"
)

// Modes is the catalogue, in menu order. Kept in sync with
// phios-dotfiles/design/tokens.common.sh PHI_TEXTURE_MODES.
var Modes = []string{"grain", "noise", "paper", "leather", "rock", "fabric"}

// maxAlpha caps how opaque the strongest speckle can get, so intensity 100
// is "clearly textured" and not "pixels of pure black and white".
const maxAlpha = 0.55

// Generate writes a width×height RGBA PNG texture for mode at the given
// intensity (0–100) to w.
func Generate(out io.Writer, mode string, intensity, width, height int) error {
	if !validMode(mode) {
		return fmt.Errorf("unknown texture mode %q (have: %s)", mode, strings.Join(Modes, ", "))
	}
	if intensity < 0 {
		intensity = 0
	}
	if intensity > 100 {
		intensity = 100
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("texture size must be positive, got %dx%d", width, height)
	}

	seed := seedFrom(mode, intensity, width, height)
	rng := rand.New(rand.NewSource(seed))
	field := buildField(mode, rng, width, height)

	strength := float64(intensity) / 100.0 * maxAlpha
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			n := field[y*width+x] // -1..1
			a := math.Abs(n) * strength
			if a > 1 {
				a = 1
			}
			var lum uint8
			if n >= 0 {
				lum = 255
			}
			img.SetNRGBA(x, y, color.NRGBA{R: lum, G: lum, B: lum, A: uint8(a * 255)})
		}
	}
	return png.Encode(out, img)
}

func validMode(m string) bool {
	for _, x := range Modes {
		if x == m {
			return true
		}
	}
	return false
}

func seedFrom(mode string, intensity, w, h int) int64 {
	hsh := fnv.New64a()
	fmt.Fprintf(hsh, "%s|%d|%d|%d", mode, intensity, w, h)
	return int64(hsh.Sum64())
}

// buildField returns a width*height slice of values in -1..1, shaped per
// mode. Every mode tiles seamlessly: white noise trivially, the lattice /
// cellular modes because their sources wrap toroidally.
func buildField(mode string, rng *rand.Rand, w, h int) []float64 {
	f := make([]float64, w*h)
	switch mode {
	case "grain":
		for i := range f {
			f[i] = rng.Float64()*2 - 1
		}
	case "noise":
		// Two octaves of value noise, smooth.
		a := valueNoise(rng, w, h, 16)
		b := valueNoise(rng, w, h, 48)
		for i := range f {
			f[i] = clamp11(a[i]*0.7 + b[i]*0.3)
		}
	case "paper":
		// Fibrous: faint horizontal streaks (per-row bias) plus fine grain.
		rowBias := make([]float64, h)
		for y := 0; y < h; y++ {
			rowBias[y] = (rng.Float64()*2 - 1) * 0.35
		}
		streak := valueNoise(rng, w, h, 8)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				g := (rng.Float64()*2 - 1) * 0.5
				f[y*w+x] = clamp11(rowBias[y] + streak[y*w+x]*0.4 + g)
			}
		}
	case "leather":
		// Small cellular bumps.
		c := worley(rng, w, h, 90)
		n := valueNoise(rng, w, h, 24)
		for i := range f {
			f[i] = clamp11((c[i]*2-1)*0.8 + n[i]*0.25)
		}
	case "rock":
		// Larger cells, rougher.
		c := worley(rng, w, h, 32)
		n := valueNoise(rng, w, h, 12)
		g := valueNoise(rng, w, h, 64)
		for i := range f {
			f[i] = clamp11((c[i]*2-1)*0.6 + n[i]*0.4 + g[i]*0.2)
		}
	case "fabric":
		// Woven: two out-of-phase sine grids plus grain.
		const periods = 64
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				u := math.Sin(float64(x) / float64(w) * periods * 2 * math.Pi)
				v := math.Sin(float64(y) / float64(h) * periods * 2 * math.Pi)
				weave := (u*v)*0.5 + (rng.Float64()*2-1)*0.35
				f[y*w+x] = clamp11(weave)
			}
		}
	}
	return f
}

func clamp11(v float64) float64 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

// valueNoise: a lattice of random values at spacing `cell`, bilinearly
// interpolated with a smoothstep, wrapping toroidally so the result tiles.
// Returns values in -1..1.
func valueNoise(rng *rand.Rand, w, h, cell int) []float64 {
	if cell < 2 {
		cell = 2
	}
	gx := w/cell + 1
	gy := h/cell + 1
	lat := make([]float64, gx*gy)
	for i := range lat {
		lat[i] = rng.Float64()*2 - 1
	}
	at := func(ix, iy int) float64 { return lat[(iy%gy)*gx+(ix%gx)] }

	out := make([]float64, w*h)
	for y := 0; y < h; y++ {
		fy := float64(y) / float64(cell)
		iy := int(fy)
		ty := smooth(fy - float64(iy))
		for x := 0; x < w; x++ {
			fx := float64(x) / float64(cell)
			ix := int(fx)
			tx := smooth(fx - float64(ix))
			v00 := at(ix, iy)
			v10 := at(ix+1, iy)
			v01 := at(ix, iy+1)
			v11 := at(ix+1, iy+1)
			top := v00 + (v10-v00)*tx
			bot := v01 + (v11-v01)*tx
			out[y*w+x] = top + (bot-top)*ty
		}
	}
	return out
}

func smooth(t float64) float64 { return t * t * (3 - 2*t) }

// worley: nearest-feature-point distance, `count` points spread over the
// tile and its 8 toroidal copies so cells wrap. Returns 0..1 (0 at a point,
// ~1 far from any).
func worley(rng *rand.Rand, w, h, count int) []float64 {
	type pt struct{ x, y float64 }
	pts := make([]pt, count)
	for i := range pts {
		pts[i] = pt{rng.Float64() * float64(w), rng.Float64() * float64(h)}
	}
	fw, fh := float64(w), float64(h)
	maxD := math.Hypot(fw, fh) / math.Sqrt(float64(count))
	out := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			best := math.MaxFloat64
			for _, p := range pts {
				dx := math.Abs(float64(x) - p.x)
				if dx > fw/2 {
					dx = fw - dx
				}
				dy := math.Abs(float64(y) - p.y)
				if dy > fh/2 {
					dy = fh - dy
				}
				d := math.Hypot(dx, dy)
				if d < best {
					best = d
				}
			}
			v := best / maxD
			if v > 1 {
				v = 1
			}
			out[y*w+x] = v
		}
	}
	return out
}

// ModeList is the catalogue as a sorted, comma-joined string for help text.
func ModeList() string {
	m := append([]string(nil), Modes...)
	sort.Strings(m)
	return strings.Join(m, ", ")
}
