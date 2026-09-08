package query

import "testing"

func TestParseDesktopEntry(t *testing.T) {
	content := `[Desktop Entry]
Type=Application
Name=Firefox
Name[it]=Navigatore
Exec=firefox %u
NoDisplay=false
`
	e, ok := parseDesktopEntry(content)
	if !ok {
		t.Fatal("expected a match")
	}
	if e.name != "Firefox" {
		t.Errorf("name = %q, want Firefox (localized Name[it] must not win)", e.name)
	}
	if e.execClean != "firefox" {
		t.Errorf("execClean = %q, want %q (field code stripped)", e.execClean, "firefox")
	}
	if e.noDisplay {
		t.Error("noDisplay should be false")
	}
	if !e.isApplication {
		t.Error("isApplication should be true")
	}
}

func TestParseDesktopEntryRejectsNoDisplay(t *testing.T) {
	content := "[Desktop Entry]\nType=Application\nName=Hidden\nExec=hidden\nNoDisplay=true\n"
	e, ok := parseDesktopEntry(content)
	if !ok {
		t.Fatal("expected a structural match even when NoDisplay=true — the caller filters it")
	}
	if !e.noDisplay {
		t.Error("noDisplay should be true")
	}
}

func TestParseDesktopEntryRejectsNonApplication(t *testing.T) {
	content := "[Desktop Entry]\nType=Link\nName=A Link\nExec=xdg-open https://example.com\n"
	e, _ := parseDesktopEntry(content)
	if e.isApplication {
		t.Error("Type=Link must not be read as an application")
	}
}

func TestParseDesktopEntryRejectsMissingExec(t *testing.T) {
	content := "[Desktop Entry]\nType=Application\nName=No Exec Here\n"
	if _, ok := parseDesktopEntry(content); ok {
		t.Error("an entry with no Exec= must not match")
	}
}

func TestParseDesktopEntryIgnoresOtherSections(t *testing.T) {
	content := `[Desktop Action new-window]
Name=New Window
Exec=firefox --new-window

[Desktop Entry]
Type=Application
Name=Firefox
Exec=firefox
`
	e, ok := parseDesktopEntry(content)
	if !ok {
		t.Fatal("expected a match")
	}
	if e.name != "Firefox" || e.execClean != "firefox" {
		t.Errorf("a value from [Desktop Action ...] leaked into the result: %+v", e)
	}
}

func TestCleanExecString(t *testing.T) {
	cases := map[string]string{
		"firefox %u":           "firefox",
		"code --new-window %F": "code --new-window",
		"kitty -e yazi":        "kitty -e yazi",
	}
	for in, want := range cases {
		if got := cleanExecString(in); got != want {
			t.Errorf("cleanExecString(%q) = %q, want %q", in, got, want)
		}
	}
}
