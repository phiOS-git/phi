package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// meter appends one JSON object per brokered request to a file — the
// "natural place to measure consumption" of phios-agente.md §6.2. It is
// intentionally simple: a durable, greppable log, not a database. Token
// accounting is best-effort (Model from the request; a full usage breakdown
// needs provider-specific response parsing, which streaming makes awkward
// and which is deferred until a provider is actually chosen — see
// PROGRESS.md S-71).
type meter struct {
	mu   sync.Mutex
	path string
}

type meterRecord struct {
	Time        string `json:"time"`
	Instance    string `json:"instance"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Status      int    `json:"status"`
	Model       string `json:"model,omitempty"`
	RespBytes   int64  `json:"resp_bytes,omitempty"`
	DurMillis   int64  `json:"dur_ms,omitempty"`
	RateLimited bool   `json:"rate_limited,omitempty"`
}

func newMeter(path string) (*meter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// Touch it so a first `phi agent broker --check` and the user can see
	// where consumption will be recorded.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	return &meter{path: path}, nil
}

func (m *meter) record(r meterRecord) {
	line, err := json.Marshal(r)
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := os.OpenFile(m.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}
