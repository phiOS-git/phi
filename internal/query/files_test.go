package query

import "testing"

func TestIsImageFile(t *testing.T) {
	cases := map[string]bool{
		"/home/user/Pictures/holiday.jpg":  true,
		"/home/user/Pictures/holiday.JPEG": true,
		"/home/user/Pictures/logo.png":     true,
		"/home/user/Pictures/anim.gif":     true,
		"/home/user/scan.TIFF":             true,
		"/home/user/notes.txt":             false,
		"/home/user/archive.tar.gz":        false,
		"/home/user/noext":                 false,
	}
	for path, want := range cases {
		if got := isImageFile(path); got != want {
			t.Errorf("isImageFile(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"/home/user/plain.jpg":           `'/home/user/plain.jpg'`,
		"/home/user/My Pictures/pic.png": `'/home/user/My Pictures/pic.png'`,
		"/home/user/it's a photo.jpg":    `'/home/user/it'\''s a photo.jpg'`,
		"/home/user/$(rm -rf ~).jpg":     `'/home/user/$(rm -rf ~).jpg'`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestIsWeakFile covers isWeakFile's four ways in: depth past
// fileDeepComponents, a hidden (dot-prefixed) directory, a named
// systemDirNames directory, and a path outside home entirely — plus the
// boundary and non-weak cases that must stay unaffected.
func TestIsWeakFile(t *testing.T) {
	const home = "/home/user"
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"file directly under home", "/home/user/report.txt", false},
		{"exactly fileDeepComponents down stays non-weak", "/home/user/a/b/c/report.txt", false},
		{"one past fileDeepComponents is weak", "/home/user/a/b/c/d/report.txt", true},
		{"hidden directory near home", "/home/user/.cache/thumbnails/pic.png", true},
		{"hidden directory further down", "/home/user/code/proj/.git/objects/x.pack", true},
		{"node_modules is weak regardless of depth", "/home/user/code/node_modules/pkg/index.js", true},
		{"vendor directory", "/home/user/code/proj/vendor/lib/x.go", true},
		{"target directory", "/home/user/code/proj/target/debug/x", true},
		{"__pycache__ directory", "/home/user/code/proj/__pycache__/x.pyc", true},
		{"outside home entirely", "/etc/hosts", true},
		// A directory literally named "..data" is not the outside-home ".."
		// token (isWeakFile's rel == ".." check does not fire on it), but it
		// is still weak on the separate, correct ground that it starts with
		// a dot — the same rule that catches ".cache" above.
		{"a directory that merely starts with .. is weak as hidden, not as outside-home", "/home/user/..data/x.txt", true},
	}
	for _, c := range cases {
		if got := isWeakFile(home, c.path); got != c.want {
			t.Errorf("%s: isWeakFile(%q, %q) = %v, want %v", c.name, home, c.path, got, c.want)
		}
	}
}

// TestIsStrongFileMatch covers the two bands isWeakFile's short-query
// exception trusts — exact and prefix, both case-insensitive — and that a
// mere substring or unrelated query does not qualify.
func TestIsStrongFileMatch(t *testing.T) {
	cases := []struct {
		name, path, q string
		want          bool
	}{
		{"exact basename match", "/home/user/notes.txt", "notes.txt", true},
		{"exact match is case-insensitive", "/home/user/Notes.txt", "notes.txt", true},
		{"basename-prefix match", "/home/user/report.txt", "rep", true},
		{"prefix match is case-insensitive", "/home/user/Report.txt", "rep", true},
		{"substring that is not a prefix does not qualify", "/home/user/report.txt", "ort", false},
		{"unrelated query", "/home/user/report.txt", "xyz", false},
	}
	for _, c := range cases {
		if got := isStrongFileMatch(c.path, c.q); got != c.want {
			t.Errorf("%s: isStrongFileMatch(%q, %q) = %v, want %v", c.name, c.path, c.q, got, c.want)
		}
	}
}

// TestFileResultScore covers the backlog scenarios end to end: a short
// query suppresses a weak (deep or system/hidden) result unless it matches
// strongly, a long query always allows a weak result through but penalizes
// its score below fileBaseScore, and a non-weak result is unaffected by
// query length either way.
func TestFileResultScore(t *testing.T) {
	const home = "/home/user"
	const deepPath = "/home/user/a/b/c/d/report.txt"             // 4 dirs down: weak by depth
	const deepShortNamePath = "/home/user/a/b/c/d/abc"           // 4 dirs down, 3-char basename
	const hiddenPath = "/home/user/.cache/x/report.txt"          // weak: hidden, shallow
	const systemPath = "/home/user/code/node_modules/report.txt" // weak: system dir, shallow
	const shallowPath = "/home/user/Documents/report.txt"        // non-weak

	cases := []struct {
		name      string
		path, q   string
		wantOK    bool
		wantScore float64
	}{
		{
			name: "short query, deep path, no strong match: suppressed",
			path: deepPath, q: "ort", // substring of report.txt, not a prefix
			wantOK: false,
		},
		{
			name: "short query, deep path, strong prefix match: kept at full score",
			path: deepPath, q: "rep",
			wantOK: true, wantScore: fileBaseScore,
		},
		{
			name: "short query, deep path, strong exact match: kept at full score",
			path: deepShortNamePath, q: "abc", // basename is exactly q, and short enough that q < fileWeakQueryLen
			wantOK: true, wantScore: fileBaseScore,
		},
		{
			name: "long query, deep path: kept but ranked below a non-weak result",
			path: deepPath, q: "report",
			wantOK: true, wantScore: fileBaseScore - fileWeakPenalty,
		},
		{
			name: "short query, hidden directory, no strong match: suppressed",
			path: hiddenPath, q: "ort",
			wantOK: false,
		},
		{
			name: "long query, hidden directory: kept but penalized",
			path: hiddenPath, q: "report",
			wantOK: true, wantScore: fileBaseScore - fileWeakPenalty,
		},
		{
			name: "long query, system directory (node_modules), shallow: still weak, penalized",
			path: systemPath, q: "report",
			wantOK: true, wantScore: fileBaseScore - fileWeakPenalty,
		},
		{
			name: "short query, non-weak result: unchanged",
			path: shallowPath, q: "ort",
			wantOK: true, wantScore: fileBaseScore,
		},
		{
			name: "long query, non-weak result: unchanged",
			path: shallowPath, q: "report",
			wantOK: true, wantScore: fileBaseScore,
		},
	}
	for _, c := range cases {
		score, ok := fileResultScore(home, c.path, c.q)
		if ok != c.wantOK {
			t.Errorf("%s: fileResultScore(%q, %q) ok = %v, want %v", c.name, c.path, c.q, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if score != c.wantScore {
			t.Errorf("%s: fileResultScore(%q, %q) score = %v, want %v", c.name, c.path, c.q, score, c.wantScore)
		}
		if score <= 0 {
			t.Errorf("%s: fileResultScore(%q, %q) score = %v, must stay > 0 or Rank() re-scores it via matchWeight", c.name, c.path, c.q, score)
		}
	}
}

// TestOpenCommandUsesImageWindowForImages guards the reported case where
// opening an image flashed a terminal window instead of persisting: an
// image result must go through phi-shell's own native image window (the
// interface rework replaced the earlier explicitly-classed imv viewer with
// this), never the unmanaged xdg-open resolution this repository does not
// configure.
func TestOpenCommandUsesImageWindowForImages(t *testing.T) {
	cases := map[string]string{
		"/home/user/Pictures/holiday.jpg": "qs -p ~/.config/quickshell/phi ipc call image open '/home/user/Pictures/holiday.jpg'",
		"/home/user/Documents/report.pdf": "xdg-open '/home/user/Documents/report.pdf'",
	}
	for path, want := range cases {
		if got := openCommand(path); got != want {
			t.Errorf("openCommand(%q) = %q, want %q", path, got, want)
		}
	}
}
