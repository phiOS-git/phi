package agent

import "testing"

func TestParseStatPPID(t *testing.T) {
	cases := []struct {
		name string
		stat string
		want int
		ok   bool
	}{
		{"plain", "1234 (zsh) S 1000 1234 1234 34816 1500 4194304 ...", 1000, true},
		{"comm with spaces and parens", "42 (foo )( bar) S 7 42 42 0 -1 ...", 7, true},
		{"comm with close-paren", "9 (a)b)c) R 3 9 9 0 0 ...", 3, true},
		{"no paren", "1234 zsh S 1000", 0, false},
		{"truncated after paren", "1234 (zsh)", 0, false},
		{"non-numeric ppid", "1234 (zsh) S notapid 1234", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseStatPPID(c.stat)
			if ok != c.ok || (ok && got != c.want) {
				t.Fatalf("parseStatPPID(%q) = (%d, %v), want (%d, %v)", c.stat, got, ok, c.want, c.ok)
			}
		})
	}
}
