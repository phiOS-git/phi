package query

import (
	"context"
	"testing"
)

func testVerbs() map[string]bool {
	return map[string]bool{"theme": true, "doctor": true, "state": true}
}

func TestPhiCommandProviderRecognisesBareVerb(t *testing.T) {
	p := PhiCommandProvider{Verbs: testVerbs()}
	results := p.Query(context.Background(), "theme set dark")
	if len(results) != 1 {
		t.Fatalf("Query(%q) = %v, want exactly one result", "theme set dark", results)
	}
	got := results[0]
	if got.Title != "phi theme set dark" {
		t.Errorf("Title = %q, want %q", got.Title, "phi theme set dark")
	}
	if got.Action.Kind != ActionExecTerminal || got.Action.Data["command"] != "phi theme set dark" {
		t.Errorf("Action = %+v, want execTerminal running %q", got.Action, "phi theme set dark")
	}
}

func TestPhiCommandProviderCaseInsensitiveVerb(t *testing.T) {
	p := PhiCommandProvider{Verbs: testVerbs()}
	results := p.Query(context.Background(), "Doctor")
	if len(results) != 1 || results[0].Action.Data["command"] != "phi Doctor" {
		t.Fatalf("Query(%q) = %v, want a match preserving the typed casing in the command", "Doctor", results)
	}
}

func TestPhiCommandProviderIgnoresUnknownWord(t *testing.T) {
	p := PhiCommandProvider{Verbs: testVerbs()}
	if results := p.Query(context.Background(), "firefox"); results != nil {
		t.Errorf("Query(%q) = %v, want nil for a word that is not a phi verb", "firefox", results)
	}
}

func TestPhiCommandProviderIgnoresEmptyQuery(t *testing.T) {
	p := PhiCommandProvider{Verbs: testVerbs()}
	if results := p.Query(context.Background(), ""); results != nil {
		t.Errorf("Query(\"\") = %v, want nil", results)
	}
}

func TestPhiCommandProviderNilVerbsIsNoop(t *testing.T) {
	p := PhiCommandProvider{}
	if results := p.Query(context.Background(), "theme set dark"); results != nil {
		t.Errorf("Query with no Verbs set = %v, want nil", results)
	}
}

// TestPhiCommandProviderRecognisesPhiPrefix checks the literal "phi "
// prefix, not just the bare verb — CommandProvider answers that text
// instead, at a lower tier, so this provider must also recognise it
// directly.
func TestPhiCommandProviderRecognisesPhiPrefix(t *testing.T) {
	p := PhiCommandProvider{Verbs: testVerbs()}
	results := p.Query(context.Background(), "phi theme set dark")
	if len(results) != 1 {
		t.Fatalf("Query(%q) = %v, want exactly one result", "phi theme set dark", results)
	}
	got := results[0]
	if got.Title != "phi theme set dark" {
		t.Errorf("Title = %q, want %q", got.Title, "phi theme set dark")
	}
	if got.ID != "phi:theme set dark" {
		t.Errorf("ID = %q, want the same ID the bare-verb form produces, so frecency treats them as one command", got.ID)
	}
}

func TestPhiCommandProviderTagDefaultsListsEveryVerbAlphabetized(t *testing.T) {
	p := PhiCommandProvider{Verbs: testVerbs()}
	got := p.TagDefaults(context.Background(), "phi")
	if len(got) != 3 {
		t.Fatalf("TagDefaults(\"phi\") = %v, want one result per verb", got)
	}
	want := []string{"phi doctor", "phi state", "phi theme"} // alphabetized
	for i, title := range want {
		if got[i].Title != title {
			t.Fatalf("TagDefaults(\"phi\")[%d].Title = %q, want %q (full: %v)", i, got[i].Title, title, got)
		}
		if got[i].Action.Data["command"] != title {
			t.Errorf("TagDefaults(\"phi\")[%d] command = %q, want %q", i, got[i].Action.Data["command"], title)
		}
	}
}

func TestPhiCommandProviderTagDefaultsNilVerbsIsEmpty(t *testing.T) {
	p := PhiCommandProvider{}
	if got := p.TagDefaults(context.Background(), "phi"); len(got) != 0 {
		t.Errorf("TagDefaults with no Verbs set = %v, want empty", got)
	}
}

func TestPhiCommandProviderPhiPrefixCaseInsensitive(t *testing.T) {
	p := PhiCommandProvider{Verbs: testVerbs()}
	results := p.Query(context.Background(), "PHI theme set dark")
	if len(results) != 1 || results[0].Action.Data["command"] != "phi theme set dark" {
		t.Fatalf("Query(%q) = %v, want a match", "PHI theme set dark", results)
	}
}

func TestPhiCommandProviderIgnoresWordStartingWithPhi(t *testing.T) {
	// "phiwhatever" must not be misread as the prefix "phi " + "whatever" —
	// the prefix check requires the separating space.
	p := PhiCommandProvider{Verbs: testVerbs()}
	if results := p.Query(context.Background(), "phidoctor"); results != nil {
		t.Errorf("Query(%q) = %v, want nil — \"phidoctor\" is not \"phi \" + a verb", "phidoctor", results)
	}
}
