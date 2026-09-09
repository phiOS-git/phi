package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// phi MCP server — tool 5 of phios-agente.md §7.1, and the ONLY growth
// point for A1's capabilities. Every future capability is a new entry in
// mcpTools, never a change to the architecture.
//
// First version: exactly ONE read-only tool with no arguments
// (`phi_context`), enough to prove the connection (§7.1, P-04). Resist
// adding more here — a new capability earns its own step.
//
// Transport: newline-delimited JSON-RPC 2.0 over stdio, the MCP stdio
// transport. opencode registers it as:
//
//	"mcp": { "phi": { "type": "local", "command": ["phi", "agent", "mcp"] } }
//
// It runs INSIDE the containment: it may not assume network, a session
// bus, or any path outside the §4.3 mounts. It reads only the project and
// personality files already mounted for the agent.

const mcpProtocolVersion = "2025-06-18"

// mcpTool is one entry in the registry. Handler returns the tool result
// text. inputSchema is a JSON Schema object; for a no-argument tool it is
// an empty object schema.
type mcpTool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(ctx context.Context, args json.RawMessage) (string, error)
}

var mcpTools = []mcpTool{
	{
		Name: "phi_context",
		Description: "Return the phiOS agent context: the active project, its instructions " +
			"(progetto.md), the project memory (memoria.md), and the available personalities. " +
			"Read-only, no arguments.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			return phiContext()
		},
	},
}

// RunMCP serves the MCP protocol on r/w until r hits EOF or ctx is done.
func RunMCP(ctx context.Context, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(w)

	for sc.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(rpcError(nil, -32700, "parse error"))
			continue
		}
		resp, notification := dispatchMCP(ctx, &req)
		if notification {
			continue // JSON-RPC notifications get no response
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErrObj      `json:"error,omitempty"`
}

type rpcErrObj struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func rpcError(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcErrObj{Code: code, Message: msg}}
}

func rpcOK(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func dispatchMCP(ctx context.Context, req *rpcRequest) (rpcResponse, bool) {
	switch req.Method {
	case "initialize":
		return rpcOK(req.ID, map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{"name": "phi", "version": "1"},
		}), false

	case "notifications/initialized", "notifications/cancelled":
		return rpcResponse{}, true

	case "ping":
		return rpcOK(req.ID, map[string]any{}), false

	case "tools/list":
		tools := make([]map[string]any, 0, len(mcpTools))
		for _, t := range mcpTools {
			tools = append(tools, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
			})
		}
		return rpcOK(req.ID, map[string]any{"tools": tools}), false

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return rpcError(req.ID, -32602, "invalid params"), false
		}
		for _, t := range mcpTools {
			if t.Name != p.Name {
				continue
			}
			out, err := t.Handler(ctx, p.Arguments)
			if err != nil {
				return rpcOK(req.ID, map[string]any{
					"content": []map[string]any{{"type": "text", "text": "error: " + err.Error()}},
					"isError": true,
				}), false
			}
			return rpcOK(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": out}},
			}), false
		}
		return rpcError(req.ID, -32602, "unknown tool: "+p.Name), false

	default:
		if req.ID == nil {
			return rpcResponse{}, true // unknown notification
		}
		return rpcError(req.ID, -32601, "method not found: "+req.Method), false
	}
}

// phiContext assembles the read-only context blob. It works both inside the
// containment (fixed /home/agent paths) and outside it (the host Model),
// so `phi agent mcp` can be exercised directly for a protocol check.
func phiContext() (string, error) {
	var b strings.Builder
	inst := os.Getenv("PHI_AGENT_INSTANCE")
	if inst == "" {
		inst = "a1"
	}
	fmt.Fprintf(&b, "phiOS agent context\ninstance: %s\n", inst)

	// Container layout first.
	if fileExists("/home/agent/project/progetto.md") || dirExists("/home/agent/project") {
		project := os.Getenv("PHI_AGENT_PROJECT")
		fmt.Fprintf(&b, "project: %s\n\n", orNone(project))
		appendFile(&b, "## instructions (progetto.md)", "/home/agent/project/progetto.md")
		appendFile(&b, "## memory (memoria.md)", "/home/agent/project/memoria.md")
		appendDirList(&b, "## personalities", "/home/agent/.local/share/personalita")
		return b.String(), nil
	}

	// Host layout (direct invocation).
	m, err := OpenModel()
	if err != nil {
		return b.String(), err
	}
	active, _ := m.ActiveProject()
	fmt.Fprintf(&b, "project: %s\n\n", orNone(active))
	if active != "" {
		if s, _ := m.ProjectInstructions(active); s != "" {
			fmt.Fprintf(&b, "## instructions (progetto.md)\n\n%s\n", s)
		}
		if s, _ := m.MemoryText(active); s != "" {
			fmt.Fprintf(&b, "## memory (memoria.md)\n\n%s\n", s)
		}
	}
	if ps, _ := m.Personalities(); len(ps) > 0 {
		fmt.Fprintf(&b, "## personalities\n\n%s\n", strings.Join(ps, ", "))
	}
	return b.String(), nil
}

func orNone(s string) string {
	if s == "" {
		return "(none selected)"
	}
	return s
}

func fileExists(p string) bool { fi, err := os.Stat(p); return err == nil && !fi.IsDir() }
func dirExists(p string) bool  { fi, err := os.Stat(p); return err == nil && fi.IsDir() }

func appendFile(b *strings.Builder, heading, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	fmt.Fprintf(b, "%s\n\n%s\n\n", heading, strings.TrimRight(string(data), "\n"))
}

func appendDirList(b *strings.Builder, heading, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	sort.Strings(names)
	fmt.Fprintf(b, "%s\n\n%s\n\n", heading, strings.Join(names, ", "))
}
