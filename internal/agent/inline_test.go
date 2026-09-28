package agent

import (
	"bytes"
	"strings"
	"testing"
)

func TestStripFence(t *testing.T) {
	cases := []struct{ in, want string }{
		{"```\nhello\nworld\n```", "hello\nworld\n"},
		{"```go\nfmt.Println(1)\n```", "fmt.Println(1)\n"},
		{"no fence here", "no fence here"},
		{"```\nonly one fence line", "```\nonly one fence line"},
		{"prefix ```\nhello\n``` suffix", "prefix ```\nhello\n``` suffix"},
	}
	for _, c := range cases {
		if got := stripFence(c.in); got != c.want {
			t.Errorf("stripFence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTrimOneTrailingNewline(t *testing.T) {
	if got := trimOneTrailingNewline("hi\n", "no newline"); got != "hi" {
		t.Errorf("trimOneTrailingNewline: got %q, want %q", got, "hi")
	}
	if got := trimOneTrailingNewline("hi\n", "has newline\n"); got != "hi\n" {
		t.Errorf("trimOneTrailingNewline: got %q, want %q", got, "hi\n")
	}
	if got := trimOneTrailingNewline("hi", "no newline"); got != "hi" {
		t.Errorf("trimOneTrailingNewline: got %q, want %q", got, "hi")
	}
}

func TestBuildInlinePrompt(t *testing.T) {
	p := buildInlinePrompt(inlineRequest{Instruction: "translate", Text: "hola", Filetype: "txt"})
	if !strings.Contains(p, "translate") || !strings.Contains(p, "hola") || !strings.Contains(p, "Filetype: txt") {
		t.Errorf("prompt missing expected content: %q", p)
	}
	if !strings.Contains(p, "-----BEGIN TEXT-----") || !strings.Contains(p, "-----END TEXT-----") {
		t.Errorf("prompt missing delimiters: %q", p)
	}
}

func TestRunInlineRequiresInstruction(t *testing.T) {
	var out, errOut bytes.Buffer
	err := RunInline(strings.NewReader(`{"text":"hi"}`), &out, &errOut)
	if err == nil {
		t.Fatal("want an error for a missing instruction")
	}
	if !strings.Contains(errOut.String(), "instruction") {
		t.Errorf("stderr = %q, want it to mention instruction", errOut.String())
	}
}

func TestRunInlineInvalidJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	err := RunInline(strings.NewReader(`not json`), &out, &errOut)
	if err == nil {
		t.Fatal("want an error for invalid JSON")
	}
}

// End-to-end: a fake pi that replies with a fenced block, run through the
// real BuildLaunch + exec path.
func TestRunInlineEndToEndStripsFence(t *testing.T) {
	fakeContain(t, "cat >/dev/null\nprintf '```\\nHELLO\\n```'\n")
	_ = testModel(t)
	var out, errOut bytes.Buffer
	err := RunInline(strings.NewReader(`{"instruction":"upper","text":"hello"}`), &out, &errOut)
	if err != nil {
		t.Fatalf("RunInline: %v (stderr: %s)", err, errOut.String())
	}
	if out.String() != "HELLO" {
		t.Errorf("out = %q, want %q", out.String(), "HELLO")
	}
}

func TestRunInlineEndToEndEmptyOutputFails(t *testing.T) {
	fakeContain(t, "cat >/dev/null\n")
	_ = testModel(t)
	var out, errOut bytes.Buffer
	err := RunInline(strings.NewReader(`{"instruction":"upper","text":"hello"}`), &out, &errOut)
	if err == nil {
		t.Fatal("want an error for empty pi output")
	}
}
