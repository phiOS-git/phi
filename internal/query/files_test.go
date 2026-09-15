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

// TestOpenCommandUsesImageWindowForImages guards docs/TODO.md's report that
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
