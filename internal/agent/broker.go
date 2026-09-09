package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The broker is the level-3 credential measure of phios-agente.md §6: the
// provider key is NEVER in the agent process. opencode (inside the
// containment) is configured with a provider whose baseURL points at this
// broker on loopback, and speaks to it in clear (§6.2). The broker — outside
// the containment — holds the key, adds it to the outbound request, streams
// the response straight back, meters consumption, and applies a local rate
// limit. If it is down, opencode gets connection-refused and no agent works
// (§6.2 "se il servizio è fermo, nessun agente funziona", consistent with
// the fail-closed rule §4.7).
//
// It is deliberately provider-agnostic: no planning document names the
// provider, and §2.1 makes neutrality a goal. upstream, the auth header, and
// any extra headers all come from broker.json; the key comes from a file
// outside every repository.

// BrokerConfig is <ConfigDir>/broker.json. Ship broker.example.json, copy,
// edit. Nothing secret belongs in it — the key is a separate file.
type BrokerConfig struct {
	// Listen is the loopback address to serve on, e.g. "127.0.0.1:8789".
	// A non-loopback host is rejected: the broker must never be reachable
	// off the machine (§6.2).
	Listen string `json:"listen"`

	// Upstream is the provider's API base, e.g. "https://api.anthropic.com".
	// The incoming request path is appended unchanged.
	Upstream string `json:"upstream"`

	// AuthHeader / AuthValue is how the key is attached. AuthValue may
	// contain the literal token "{key}", replaced with the key file
	// contents. Examples:
	//   Anthropic:          "x-api-key" / "{key}"
	//   OpenAI-compatible:  "authorization" / "Bearer {key}"
	AuthHeader string `json:"auth_header"`
	AuthValue  string `json:"auth_value"`

	// ExtraHeaders are added to every upstream request (e.g. Anthropic's
	// "anthropic-version"). They never contain secrets.
	ExtraHeaders map[string]string `json:"extra_headers"`

	// StripRequestHeaders are removed from the incoming request before
	// forwarding — the client's own auth attempts, first of all. AuthHeader
	// is always stripped regardless of this list.
	StripRequestHeaders []string `json:"strip_request_headers"`

	// RateLimit is the local cap. Zero requests disables it (not
	// recommended — §6.3 wants a local limit as well as the provider cap).
	RateLimit struct {
		Requests      int `json:"requests"`
		WindowSeconds int `json:"window_seconds"`
	} `json:"rate_limit"`

	// MeterFile is where per-request consumption records are appended as
	// JSONL. Defaults to <StateDir>/broker-meter.jsonl.
	MeterFile string `json:"meter_file"`

	// UpstreamTimeoutSeconds bounds a whole non-streaming request. 0 keeps
	// the Go default of no timeout, which streaming needs.
	UpstreamTimeoutSeconds int `json:"upstream_timeout_seconds"`
}

// Broker is a configured, ready-to-run broker.
type Broker struct {
	inst   Instance
	cfg    BrokerConfig
	key    string
	target *url.URL
	lim    *bucket // nil when rate limiting is disabled
	meter  *meter
}

// LoadBroker reads broker.json and the key file for the instance and
// validates everything the broker needs to start. It does NOT bind the
// socket — see Check and Run.
func LoadBroker(inst Instance) (*Broker, error) {
	dir, err := inst.ConfigDir()
	if err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(dir, "broker.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no broker config: %s (copy broker.example.json and edit)", cfgPath)
		}
		return nil, err
	}
	// Not DisallowUnknownFields: the example file documents itself with
	// "//"-prefixed keys, a common JSON-config convention. The fields that
	// matter are all explicitly required below, so a typo in one of them is
	// caught anyway.
	var cfg BrokerConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", cfgPath, err)
	}

	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8789"
	}
	if err := requireLoopback(cfg.Listen); err != nil {
		return nil, fmt.Errorf("%s: listen: %w", cfgPath, err)
	}
	if cfg.Upstream == "" {
		return nil, fmt.Errorf("%s: upstream is required", cfgPath)
	}
	target, err := url.Parse(cfg.Upstream)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("%s: upstream is not an absolute URL: %q", cfgPath, cfg.Upstream)
	}
	if cfg.AuthHeader == "" {
		return nil, fmt.Errorf("%s: auth_header is required", cfgPath)
	}
	if !strings.Contains(cfg.AuthValue, "{key}") {
		return nil, fmt.Errorf("%s: auth_value must contain {key}", cfgPath)
	}

	key, keySrc, err := loadProviderKey(dir)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("provider key at %s is empty", keySrc)
	}

	b := &Broker{inst: inst, cfg: cfg, key: key, target: target}

	if cfg.RateLimit.Requests > 0 {
		w := time.Duration(cfg.RateLimit.WindowSeconds) * time.Second
		if w <= 0 {
			w = time.Minute
		}
		b.lim = newBucket(cfg.RateLimit.Requests, w)
	}

	meterPath := cfg.MeterFile
	if meterPath == "" {
		sd, err := inst.StateDir()
		if err != nil {
			return nil, err
		}
		meterPath = filepath.Join(sd, "broker-meter.jsonl")
	}
	m, err := newMeter(expandHome(meterPath))
	if err != nil {
		return nil, err
	}
	b.meter = m

	return b, nil
}

// loadProviderKey resolves the key file in precedence order: the explicit
// env var (set by the systemd unit from LoadCredential), then the systemd
// credentials directory, then the in-config-dir fallback for a hand-run
// broker. The key is never a literal anywhere and never in a repository.
func loadProviderKey(cfgDir string) (key, src string, err error) {
	candidates := []string{
		os.Getenv("PHI_AGENT_BROKER_KEY_FILE"),
	}
	if c := os.Getenv("CREDENTIALS_DIRECTORY"); c != "" {
		candidates = append(candidates, filepath.Join(c, "provider-key"))
	}
	candidates = append(candidates, filepath.Join(cfgDir, "provider-key"))

	for _, p := range candidates {
		if p == "" {
			continue
		}
		v, e := readTrimmedFile(p)
		if e == nil {
			return v, p, nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			return "", p, fmt.Errorf("reading provider key %s: %w", p, e)
		}
	}
	return "", "", errors.New("no provider key file found (set PHI_AGENT_BROKER_KEY_FILE, or create <config>/phi-agent/<instance>/provider-key with mode 600)")
}

func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		if host == "localhost" {
			return nil
		}
		return fmt.Errorf("must be a loopback address, got %q", host)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("must be a loopback address, got %q", host)
	}
	return nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// Check validates configuration and key availability without serving. It is
// the broker unit's ExecStartPre and the user's "is this wired right" test.
func (b *Broker) Check() error { return nil } // LoadBroker already did the work

// Summary is a one-line human description for `phi agent broker --check`.
func (b *Broker) Summary() string {
	rl := "off"
	if b.lim != nil {
		rl = fmt.Sprintf("%d / %ds", b.cfg.RateLimit.Requests, b.cfg.RateLimit.WindowSeconds)
	}
	return fmt.Sprintf("instance=%s  listen=%s  upstream=%s  auth=%s  rate-limit=%s  key=loaded (%d bytes)",
		b.inst, b.cfg.Listen, b.cfg.Upstream, b.cfg.AuthHeader, rl, len(b.key))
}

// handler builds the reverse proxy. FlushInterval < 0 makes ReverseProxy
// flush to the client immediately after every read from upstream — this is
// the documented way to pass an SSE / chunked stream through without
// buffering (§6.2 "deve trasferire lo streaming senza bufferizzare"), and
// V-09 is exactly this property.
func (b *Broker) handler() http.Handler {
	rp := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = b.target.Scheme
			pr.Out.URL.Host = b.target.Host
			pr.Out.Host = b.target.Host
			if b.target.Path != "" && b.target.Path != "/" {
				pr.Out.URL.Path = strings.TrimRight(b.target.Path, "/") + pr.Out.URL.Path
			}
			// Drop the client's own auth attempts and anything the config
			// names, then attach the real credential.
			pr.Out.Header.Del(b.cfg.AuthHeader)
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("X-Api-Key")
			for _, h := range b.cfg.StripRequestHeaders {
				pr.Out.Header.Del(h)
			}
			pr.Out.Header.Set(b.cfg.AuthHeader, strings.ReplaceAll(b.cfg.AuthValue, "{key}", b.key))
			for k, v := range b.cfg.ExtraHeaders {
				pr.Out.Header.Set(k, v)
			}
			// X-Forwarded-* would leak the loopback client detail upstream
			// for no benefit.
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Host")
			pr.Out.Header.Del("X-Forwarded-Proto")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "phi agent broker: upstream error: "+err.Error(), http.StatusBadGateway)
		},
	}
	if b.cfg.UpstreamTimeoutSeconds > 0 {
		rp.Transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: time.Duration(b.cfg.UpstreamTimeoutSeconds) * time.Second,
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		if b.lim != nil && !b.lim.allow() {
			w.Header().Set("Retry-After", fmt.Sprint(b.cfg.RateLimit.WindowSeconds))
			http.Error(w, `{"error":"phi agent broker: local rate limit exceeded"}`, http.StatusTooManyRequests)
			b.meter.record(meterRecord{
				Time: start.UTC().Format(time.RFC3339), Instance: string(b.inst),
				Method: r.Method, Path: r.URL.Path, Status: http.StatusTooManyRequests,
				RateLimited: true,
			})
			return
		}

		// Sniff the model from the request body without consuming it.
		model, body := sniffModel(r.Body)
		r.Body = body

		cw := &countingResponseWriter{ResponseWriter: w}
		rp.ServeHTTP(cw, r)

		b.meter.record(meterRecord{
			Time:      start.UTC().Format(time.RFC3339),
			Instance:  string(b.inst),
			Method:    r.Method,
			Path:      r.URL.Path,
			Status:    cw.status,
			Model:     model,
			RespBytes: cw.n,
			DurMillis: time.Since(start).Milliseconds(),
		})
	})
}

// Run serves until ctx is cancelled. It binds the socket here (not in
// LoadBroker) so a bind failure is a run failure the unit can restart.
func (b *Broker) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:    b.cfg.Listen,
		Handler: b.handler(),
		// No ReadTimeout/WriteTimeout: a long streaming completion is a
		// normal request here, and a write deadline would truncate it.
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", b.cfg.Listen)
	if err != nil {
		return fmt.Errorf("bind %s: %w", b.cfg.Listen, err)
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return ctx.Err()
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// sniffModel reads the request body fully (opencode's messages are small
// JSON), extracts a top-level "model" string if present, and returns a
// fresh ReadCloser with the same bytes. On any error it returns "" and the
// original body unread.
func sniffModel(rc io.ReadCloser) (string, io.ReadCloser) {
	if rc == nil {
		return "", http.NoBody
	}
	const maxBody = 1 << 20 // 1 MiB is far more than a chat turn's request
	buf, err := io.ReadAll(io.LimitReader(rc, maxBody+1))
	if err != nil {
		// Hand back what we have followed by the rest, unsniffed.
		return "", io.NopCloser(io.MultiReader(bytes.NewReader(buf), rc))
	}
	if len(buf) > maxBody {
		// Unexpectedly large: do not risk corrupting it, just pass through.
		return "", struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(buf), rc), rc}
	}
	_ = rc.Close()
	var probe struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(buf, &probe)
	return probe.Model, io.NopCloser(bytes.NewReader(buf))
}

type countingResponseWriter struct {
	http.ResponseWriter
	status int
	n      int64
}

func (c *countingResponseWriter) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *countingResponseWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	n, err := c.ResponseWriter.Write(p)
	c.n += int64(n)
	return n, err
}

// Flush forwards the flush ReverseProxy issues after each streamed chunk —
// without this method the stream would buffer inside net/http and V-09
// would fail.
func (c *countingResponseWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
