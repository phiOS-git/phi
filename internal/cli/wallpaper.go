package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"phi/internal/wallpaper"
)

const wallpaperUsage = `usage: phi wallpaper <verb> [arguments]

Verbs:
  texture MODE [--intensity N] [--size WxH] [--out PATH]
                    generate a procedural texture overlay PNG for the
                    shell's background layer. MODE is one of: ` + `%s` + `.
                    --intensity is 0-100 (default 40). --size defaults to
                    512x512 (a seamless tile). Writes to PATH, or to
                    standard output when --out is omitted.

The texture is generated once and cached by the shell; changing MODE or
--intensity produces a new file. Output is deterministic for a given
(mode, intensity, size).
`

func runWallpaper(args []string, stdout, stderr io.Writer) int {
	usage := fmt.Sprintf(wallpaperUsage, wallpaper.ModeList())
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 1
	}
	switch args[0] {
	case "texture":
		return runWallpaperTexture(args[1:], usage, stdout, stderr)
	case "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "%s: wallpaper: unknown verb %q\n", progName, args[0])
		fmt.Fprint(stderr, usage)
		return 1
	}
}

func runWallpaperTexture(args []string, usage string, stdout, stderr io.Writer) int {
	intensity := 40
	width, height := 512, 512
	outPath := ""
	var mode string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--intensity":
			i++
			if i >= len(args) {
				fmt.Fprintf(stderr, "%s: wallpaper: --intensity needs a number\n", progName)
				return 1
			}
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(stderr, "%s: wallpaper: bad --intensity %q\n", progName, args[i])
				return 1
			}
			intensity = n
		case a == "--size":
			i++
			if i >= len(args) {
				fmt.Fprintf(stderr, "%s: wallpaper: --size needs WxH\n", progName)
				return 1
			}
			w, h, err := parseSize(args[i])
			if err != nil {
				fmt.Fprintf(stderr, "%s: wallpaper: %v\n", progName, err)
				return 1
			}
			width, height = w, h
		case a == "--out":
			i++
			if i >= len(args) {
				fmt.Fprintf(stderr, "%s: wallpaper: --out needs a path\n", progName)
				return 1
			}
			outPath = args[i]
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(stderr, "%s: wallpaper: unknown flag %q\n", progName, a)
			return 1
		default:
			if mode != "" {
				fmt.Fprint(stderr, usage)
				return 1
			}
			mode = a
		}
	}

	if mode == "" {
		fmt.Fprint(stderr, usage)
		return 1
	}

	var w io.Writer = stdout
	if outPath != "" {
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			fmt.Fprintf(stderr, "%s: wallpaper: %v\n", progName, err)
			return 1
		}
		f, err := os.Create(outPath)
		if err != nil {
			fmt.Fprintf(stderr, "%s: wallpaper: %v\n", progName, err)
			return 1
		}
		defer f.Close()
		w = f
	}

	if err := wallpaper.Generate(w, mode, intensity, width, height); err != nil {
		fmt.Fprintf(stderr, "%s: wallpaper: %v\n", progName, err)
		return 1
	}
	return 0
}

func parseSize(s string) (int, int, error) {
	parts := strings.SplitN(strings.ToLower(s), "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("size must be WxH, got %q", s)
	}
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("bad width in %q", s)
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("bad height in %q", s)
	}
	return w, h, nil
}
