package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// rpcExchange feeds newline-delimited requests through RunMCP and returns
// the decoded responses in order.
func rpcExchange(t *testing.T, requests ...string) []rpcResponse {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out strings.Builder
	if err := RunMCP(context.Background(), in, &out); err != nil {
		t.Fatalf("RunMCP: %v", err)
	}
	var resps []rpcResponse
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var r rpcResponse
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		resps = append(resps, r)
	}
	return resps
}

func TestMCPInitializeAndList(t *testing.T) {
	resps := rpcExchange(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2 (the notification must not get one)", len(resps))
	}
	initRes, _ := resps[0].Result.(map[string]any)
	if initRes["protocolVersion"] != mcpProtocolVersion {
		t.Errorf("initialize protocolVersion = %v", initRes["protocolVersion"])
	}
	listRes, _ := resps[1].Result.(map[string]any)
	tools, _ := listRes["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools/list returned %d tools, want exactly 1 (§7.1)", len(tools))
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != "phi_context" {
		t.Errorf("tool name = %v, want phi_context", tool["name"])
	}
}

func TestMCPCallPhiContext(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("PHI_AGENT_INSTANCE", "a1")

	resps := rpcExchange(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"phi_context","arguments":{}}}`,
	)
	res, _ := resps[0].Result.(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatal("phi_context returned no content")
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if !strings.Contains(text, "phiOS agent context") || !strings.Contains(text, "instance: a1") {
		t.Errorf("phi_context text unexpected:\n%s", text)
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	resps := rpcExchange(t, `{"jsonrpc":"2.0","id":9,"method":"nonsense/thing"}`)
	if resps[0].Error == nil || resps[0].Error.Code != -32601 {
		t.Errorf("want method-not-found, got %+v", resps[0])
	}
}

func TestMCPParseError(t *testing.T) {
	resps := rpcExchange(t, `{not json`)
	if resps[0].Error == nil || resps[0].Error.Code != -32700 {
		t.Errorf("want parse error, got %+v", resps[0])
	}
}
