package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// `phi agent ask` — J7 of phios-agente.md §1.3 / §10.2: a quick question in
// the terminal, a thin wrapper onto the ALREADY-RUNNING A1 service. No cold
// start: it talks to opencode's loopback HTTP API on the port
// phi-agent-a1.service serves. The session is ephemeral — created, used,
// and DELETEd — so it never appears in the panel list and never reaches
// memory (§10.2, V-15). Deleting the session (rather than filtering it
// client-side) satisfies both "excluded from the list" and "excluded from
// memory" with nothing for the panel to cooperate on.
//
// Fail-closed: if the A1 service is not up, this fails explicitly (§13,
// §4.7) — it never starts an engine of its own.

// AskConfig is the small set of knobs `phi agent ask` needs.
type AskConfig struct {
	BaseURL     string // http://127.0.0.1:4199 by default (phi-agent-a1's port)
	Personality string // opencode agent name; "" = the config default
	Timeout     time.Duration
}

// A1BaseURL is where phi-agent-a1.service serves. The port matches the
// unit's PHI_AGENT_A1_PORT default; override with PHI_AGENT_A1_URL.
func A1BaseURL() string {
	if v := os.Getenv("PHI_AGENT_A1_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:4199"
}

// Ask sends one prompt and writes the assistant's text reply to w.
func Ask(ctx context.Context, cfg AskConfig, prompt string, w io.Writer) error {
	if cfg.BaseURL == "" {
		cfg.BaseURL = A1BaseURL()
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}
	base := strings.TrimRight(cfg.BaseURL, "/")

	client := &http.Client{Timeout: cfg.Timeout}

	// Reachability check first, so "service not running" is a clear message
	// rather than a generic dial error mid-request.
	if err := pingOpencode(ctx, client, base); err != nil {
		return fmt.Errorf("A1 service not reachable at %s (%v) — is phi-agent-a1.service running?", base, err)
	}

	sess, err := opencodePost(ctx, client, base+"/session", map[string]any{
		"title": "inline (ephemeral)",
	})
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	var sid struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(sess, &sid)
	if sid.ID == "" {
		return fmt.Errorf("opencode did not return a session id: %s", truncate(sess, 200))
	}
	// Always clean up, even on error or cancellation — the session must not
	// linger (§10.2).
	defer func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(delCtx, http.MethodDelete, base+"/session/"+sid.ID, nil)
		if resp, e := client.Do(req); e == nil {
			_ = resp.Body.Close()
		}
	}()

	body := map[string]any{
		"parts": []map[string]any{{"type": "text", "text": prompt}},
	}
	if cfg.Personality != "" {
		body["agent"] = cfg.Personality
	}
	reply, err := opencodePost(ctx, client, base+"/session/"+sid.ID+"/message", body)
	if err != nil {
		return fmt.Errorf("sending message: %w", err)
	}

	text := extractAssistantText(reply)
	if text == "" {
		return fmt.Errorf("no text in the reply: %s", truncate(reply, 300))
	}
	_, err = io.WriteString(w, text)
	if !strings.HasSuffix(text, "\n") {
		_, _ = io.WriteString(w, "\n")
	}
	return err
}

func pingOpencode(ctx context.Context, c *http.Client, base string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/global/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned %d", resp.StatusCode)
	}
	return nil
}

func opencodePost(ctx context.Context, c *http.Client, url string, payload any) ([]byte, error) {
	buf, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s -> %d: %s", url, resp.StatusCode, truncate(data, 300))
	}
	return data, nil
}

// extractAssistantText pulls the concatenated text parts out of an
// opencode message response. The shape is { info, parts:[{type,text,...}] }
// (server.txt); tolerate either that or a bare parts array.
func extractAssistantText(data []byte) string {
	var withParts struct {
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(data, &withParts); err == nil && len(withParts.Parts) > 0 {
		var b strings.Builder
		for _, p := range withParts.Parts {
			if p.Type == "text" && p.Text != "" {
				b.WriteString(p.Text)
			}
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
