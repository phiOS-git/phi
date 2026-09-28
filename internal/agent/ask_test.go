package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Ask is a thin BuildLaunch(ModePrint) + exec wrapper; its own argv shape is
// covered by TestBuildLaunchAskShape (launch_test.go). These tests exercise
// the exec plumbing end to end against a fake phi-agent-contain.

func TestAskRunsAndReturnsOutput(t *testing.T) {
	fakeContain(t, "cat >/dev/null\nprintf 'answer to: %s' \"$*\"\n")
	_ = testModel(t)

	var out strings.Builder
	err := Ask(context.Background(), AskConfig{Profile: General, Timeout: 5 * time.Second}, "what is 2+2", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "what is 2+2") {
		t.Errorf("output = %q, want it to contain the question (proving argv reached pi)", out.String())
	}
}

func TestAskFailsOnNonZeroExit(t *testing.T) {
	fakeContain(t, "exit 1\n")
	_ = testModel(t)

	err := Ask(context.Background(), AskConfig{Profile: General, Timeout: 5 * time.Second}, "hi", new(strings.Builder))
	if err == nil {
		t.Fatal("want an error when pi exits non-zero")
	}
}

func TestAskFailsClosedWhenLauncherMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_ = testModel(t)

	err := Ask(context.Background(), AskConfig{Profile: General}, "hi", new(strings.Builder))
	if err == nil || !strings.Contains(err.Error(), "phi-agent-contain") {
		t.Errorf("want a clear error naming phi-agent-contain, got %v", err)
	}
}
