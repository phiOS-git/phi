package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// `phi agent inline` (PI-05): the editor's inline-edit command (§7). Reads
// one JSON request from stdin, runs it through pi in print mode under the
// inline profile — no tools, no context files, no memory, no session file,
// no sidecar (§3, §6) — and writes the replacement text, and nothing else,
// to stdout.

type inlineRequest struct {
	Instruction string `json:"instruction"`
	Text        string `json:"text"`
	Filetype    string `json:"filetype"`
}

// RunInline implements `phi agent inline`. On failure it writes a message to
// stderr and returns a non-nil error; stdout is only ever the replacement
// text.
func RunInline(stdin io.Reader, stdout, stderr io.Writer) error {
	data, err := io.ReadAll(stdin)
	if err != nil {
		err = fmt.Errorf("phi agent inline: reading stdin: %w", err)
		fmt.Fprintln(stderr, err)
		return err
	}
	var req inlineRequest
	if err := json.Unmarshal(data, &req); err != nil {
		err = fmt.Errorf("phi agent inline: invalid JSON request: %w", err)
		fmt.Fprintln(stderr, err)
		return err
	}
	if strings.TrimSpace(req.Instruction) == "" {
		err := fmt.Errorf(`phi agent inline: "instruction" is required`)
		fmt.Fprintln(stderr, err)
		return err
	}

	argv, err := BuildLaunch(LaunchSpec{Profile: Inline, Mode: ModePrint})
	if err != nil {
		err = fmt.Errorf("phi agent inline: %w", err)
		fmt.Fprintln(stderr, err)
		return err
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(buildInlinePrompt(req))
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		err = fmt.Errorf("phi agent inline: pi exited with an error: %w", err)
		fmt.Fprintln(stderr, err)
		return err
	}

	result := stripFence(out.String())
	result = trimOneTrailingNewline(result, req.Text)
	if strings.TrimSpace(result) == "" {
		err := fmt.Errorf("phi agent inline: empty output from pi")
		fmt.Fprintln(stderr, err)
		return err
	}
	_, err = io.WriteString(stdout, result)
	return err
}

// buildInlinePrompt assembles pi's stdin prompt: the instruction, the
// filetype when known, then the original text between unambiguous
// delimiters so pi never confuses instruction and subject.
func buildInlinePrompt(req inlineRequest) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(req.Instruction))
	b.WriteString("\n\n")
	if req.Filetype != "" {
		fmt.Fprintf(&b, "Filetype: %s\n\n", req.Filetype)
	}
	b.WriteString("Reply with the replacement text only, nothing else.\n\n")
	b.WriteString("-----BEGIN TEXT-----\n")
	b.WriteString(req.Text)
	if !strings.HasSuffix(req.Text, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("-----END TEXT-----\n")
	return b.String()
}

// stripFence removes a single Markdown fence wrapping the WHOLE trimmed
// output (```, optionally with a language tag on the opening line, down to a
// closing ``` on its own line). Output that is not a single fenced block is
// returned unchanged.
func stripFence(s string) string {
	t := strings.Trim(s, "\n")
	lines := strings.Split(t, "\n")
	if len(lines) < 2 {
		return s
	}
	first := strings.TrimSpace(lines[0])
	last := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(first, "```") || last != "```" {
		return s
	}
	return strings.Join(lines[1:len(lines)-1], "\n") + "\n"
}

// trimOneTrailingNewline drops exactly one trailing "\n" from s when the
// original input text had none — pi's reply otherwise always gains one from
// being written as its own line.
func trimOneTrailingNewline(s, inputText string) string {
	if !strings.HasSuffix(inputText, "\n") && strings.HasSuffix(s, "\n") {
		return s[:len(s)-1]
	}
	return s
}
