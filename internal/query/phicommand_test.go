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
