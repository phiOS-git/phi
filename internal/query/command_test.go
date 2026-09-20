package query

import (
	"context"
	"testing"
)

func TestCommandResolvableBinary(t *testing.T) {
	r := CommandProvider{}.Query(context.Background(), "ls -la")
	if len(r) != 1 || r[0].Action.Data["command"] != "ls -la" {
		t.Fatalf("Query(%q) = %v", "ls -la", r)
	}
}

func TestCommandUnresolvableFirstWordYieldsNothing(t *testing.T) {
	if r := (CommandProvider{}).Query(context.Background(), "definitely not a real binary"); r != nil {
		t.Errorf("Query(%q) = %v, want nil for a first word that isn't on PATH", "definitely not a real binary", r)
	}
}

// runner-bar prefix feature: "run <anything>" forces this
// provider's answer, bypassing the exec.LookPath gate — the same
// unresolvable-first-word text that yields nothing above must work once
// explicitly prefixed with "run ".
func TestCommandRunPrefixBypassesLookPath(t *testing.T) {
	r := CommandProvider{}.Query(context.Background(), "run definitely-not-a-real-binary --flag")
	if len(r) != 1 {
		t.Fatalf("Query(%q) = %v, want exactly one result", "run definitely-not-a-real-binary --flag", r)
	}
	want := "definitely-not-a-real-binary --flag"
	if r[0].Action.Data["command"] != want {
		t.Errorf("command = %q, want %q (the \"run \" keyword stripped)", r[0].Action.Data["command"], want)
	}
}

func TestCommandRunPrefixCaseInsensitive(t *testing.T) {
	r := CommandProvider{}.Query(context.Background(), "RUN htop")
	if len(r) != 1 || r[0].Action.Data["command"] != "htop" {
		t.Fatalf("Query(%q) = %v, want %q", "RUN htop", r, "htop")
	}
}

func TestCommandRunPrefixAloneYieldsNothing(t *testing.T) {
	if r := (CommandProvider{}).Query(context.Background(), "run "); r != nil {
		t.Errorf("Query(%q) = %v, want nil for the keyword with nothing after it", "run ", r)
	}
}

func TestCommandWordStartingWithRunNotMisread(t *testing.T) {
	// "runner status" must not be read as prefix "run" + "ner status".
	if r := (CommandProvider{}).Query(context.Background(), "runner status"); r != nil {
		t.Errorf("Query(%q) = %v, want nil — \"runner\" is not on PATH and not the \"run \" prefix", "runner status", r)
	}
}
