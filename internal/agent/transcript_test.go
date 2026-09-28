package agent

import (
	"strings"
	"testing"
)

// Fixture lines built from pi's session-format.md / message-types.md.
func TestParseTranscript(t *testing.T) {
	cases := []struct {
		name        string
		lines       []string
		wantMsgs    []Message
		wantSession string
	}{
		{
			name: "user string content",
			lines: []string{
				`{"type":"session","version":3,"id":"u1","timestamp":"2024-12-03T14:00:00.000Z","cwd":"/project"}`,
				`{"type":"message","id":"a1b2c3d4","parentId":"prev1234","timestamp":"2024-12-03T14:00:01.000Z","message":{"role":"user","content":"Hello","timestamp":1733234401000}}`,
			},
			wantMsgs: []Message{{Role: "user", Text: "Hello"}},
		},
		{
			name: "assistant text blocks, thinking and tool calls ignored",
			lines: []string{
				`{"type":"message","id":"b2","parentId":"a1","timestamp":"2024-12-03T14:00:02.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"pondering"},{"type":"text","text":"Hi "},{"type":"toolCall","id":"c1","name":"read","arguments":{}},{"type":"text","text":"there!"}],"api":"anthropic-messages","provider":"anthropic","model":"m","usage":{},"stopReason":"stop","timestamp":1733234402000}}`,
			},
			wantMsgs: []Message{{Role: "assistant", Text: "Hi there!"}},
		},
		{
			name: "assistant error stop reason",
			lines: []string{
				`{"type":"message","id":"c3","parentId":"b2","timestamp":"2024-12-03T14:00:03.000Z","message":{"role":"assistant","content":[{"type":"text","text":"partial"}],"api":"a","provider":"p","model":"m","usage":{},"stopReason":"error","errorMessage":"upstream 500","timestamp":1733234403000}}`,
			},
			wantMsgs: []Message{{Role: "assistant", Text: "partial", Error: "upstream 500"}},
		},
		{
			name: "toolResult, usage and system entries produce no message",
			lines: []string{
				`{"type":"message","id":"c3d4e5f6","parentId":"b2c3d4e5","timestamp":"2024-12-03T14:00:03.000Z","message":{"role":"toolResult","toolCallId":"call_123","toolName":"bash","content":[{"type":"text","text":"output"}],"isError":false,"timestamp":1733234403000}}`,
				`{"type":"usage","id":"f6g7h8i9","parentId":"e5f6g7h8","timestamp":"2024-12-03T14:08:00.000Z","kind":"cache_warm","provider":"anthropic","model":"m","usage":{"input":0,"output":0,"cacheRead":50000,"cacheWrite":0,"totalTokens":50000,"cost":{"input":0,"output":0,"cacheRead":0.015,"cacheWrite":0,"total":0.015}}}`,
				`{"type":"message","id":"a0b1c2d3","parentId":null,"timestamp":"2024-12-03T14:00:00.000Z","message":{"role":"system","content":"","sections":{"preamble":"..."},"timestamp":1733234400000}}`,
			},
			wantMsgs: nil,
		},
		{
			name: "session_info sets the session name, last one wins",
			lines: []string{
				`{"type":"session_info","id":"k1","parentId":"j0","timestamp":"2024-12-03T14:30:00.000Z","name":"First name"}`,
				`{"type":"message","id":"m1","parentId":"k1","timestamp":"2024-12-03T14:31:00.000Z","message":{"role":"user","content":"hi","timestamp":1}}`,
				`{"type":"session_info","id":"k2","parentId":"m1","timestamp":"2024-12-03T14:32:00.000Z","name":"Renamed"}`,
			},
			wantMsgs:    []Message{{Role: "user", Text: "hi"}},
			wantSession: "Renamed",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := strings.NewReader(strings.Join(c.lines, "\n") + "\n")
			msgs, name, err := parseTranscript(r)
			if err != nil {
				t.Fatalf("parseTranscript: %v", err)
			}
			if len(msgs) != len(c.wantMsgs) {
				t.Fatalf("messages = %+v, want %+v", msgs, c.wantMsgs)
			}
			for i := range msgs {
				if msgs[i] != c.wantMsgs[i] {
					t.Errorf("message %d = %+v, want %+v", i, msgs[i], c.wantMsgs[i])
				}
			}
			if name != c.wantSession {
				t.Errorf("sessionName = %q, want %q", name, c.wantSession)
			}
		})
	}
}
