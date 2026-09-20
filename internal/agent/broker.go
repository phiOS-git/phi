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

// The broker is a credential measure: the provider key is NEVER in the
// agent process. The broker (outside containment) holds the key, adds it to
// outbound requests, streams responses, meters consumption, and applies
// local rate limits. Deliberately provider-agnostic: config from
// broker.json, key from an external file.

// BrokerConfig is <ConfigDir>/broker.json; keep secrets outside it.
type BrokerConfig struct {
	// Listen: loopback address only (never reachable off-machine).
	Listen string `json:"listen"`

	// Upstream: provider's API base; request path is appended unchanged.
	Upstream string `json:"upstream"`

	// AuthHeader / AuthValue: how the key is attached (AuthValue replaces
	// "{key}" with file contents).
	AuthHeader string `json:"auth_header"`
	AuthValue  string `json:"auth_value"`

	// ExtraHeaders: added to every upstream request (never contain secrets).
	ExtraHeaders map[string]string `json:"extra_headers"`

	// StripRequestHeaders: removed before forwarding (AuthHeader always
	// stripped).
	StripRequestHeaders []string `json:"strip_request_headers"`

	// RateLimit: local cap (0 disables; recommend setting both local and provider).
	RateLimit struct {
		Requests      int `json:"requests"`
		WindowSeconds int `json:"window_seconds"`
	} `json:"rate_limit"`

	// MeterFile: where consumption records are appended as JSONL.
	MeterFile string `json:"meter_file"`

	// UpstreamTimeoutSeconds: bounds non-streaming requests (0 = no timeout).
	UpstreamTimeoutSeconds int `json:"upstream_timeout_seconds"`
}

// Broker is configured and ready to run.
type Broker struct {
	inst   Instance
	cfg    BrokerConfig
	key    string
	target *url.URL
	lim    *bucket // nil when rate limiting is disabled
	meter  *meter
}

// LoadBroker reads and validates broker.json and the key file.
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
	// Not DisallowUnknownFields: JSON example documents itself with "//"-prefixed
	// keys; required fields are validated explicitly below anyway.
	var cfg BrokerConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", cfgPath, err)
	}

	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8789"
	}
	if err := requireLocalListen(cfg.Listen); err != nil {
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

// loadProviderKey resolves the key file from env var, systemd credentials,
// or config directory (in that order).
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

// requireLocalListen accepts loopback or unix socket addresses only.
func requireLocalListen(addr string) error {
	if network, path, ok := splitUnix(addr); ok {
		if network != "unix" || path == "" {
			return fmt.Errorf("bad unix listen address %q", addr)
		}
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		if host == "localhost" {
			return nil
		}
		return fmt.Errorf("must be a loopback or unix address, got %q", host)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("must be a loopback or unix address, got %q", host)
	}
	return nil
}

// requireLoopback is kept for tests and rejects unix addresses too.
func requireLoopback(addr string) error {
	if _, _, ok := splitUnix(addr); ok {
		return fmt.Errorf("not a loopback address: %q", addr)
	}
	return requireLocalListen(addr)
}

func splitUnix(addr string) (network, path string, ok bool) {
	if strings.HasPrefix(addr, "unix:") {
		return "unix", strings.TrimPrefix(addr, "unix:"), true
	}
	return "", "", false
}

// listen binds the configured address, removing a stale unix socket first.
func (b *Broker) listen() (net.Listener, error) {
	if _, path, ok := splitUnix(b.cfg.Listen); ok {
		path = expandHome(path)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		// Remove stale socket from unclean stop.
		if fi, err := os.Stat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
			_ = os.Remove(path)
		}
		ln, err := net.Listen("unix", path)
		if err != nil {
			return nil, err
		}
		_ = os.Chmod(path, 0o600)
		return ln, nil
	}
	return net.Listen("tcp", b.cfg.Listen)
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// Check is the broker unit's ExecStartPre (validation done by LoadBroker).
func (b *Broker) Check() error { return nil }

// Summary returns a one-line description of broker configuration.
func (b *Broker) Summary() string {
	rl := "off"
	if b.lim != nil {
		rl = fmt.Sprintf("%d / %ds", b.cfg.RateLimit.Requests, b.cfg.RateLimit.WindowSeconds)
	}
	return fmt.Sprintf("instance=%s  listen=%s  upstream=%s  auth=%s  rate-limit=%s  key=loaded (%d bytes)",
		b.inst, b.cfg.Listen, b.cfg.Upstream, b.cfg.AuthHeader, rl, len(b.key))
}

// handler builds a reverse proxy that flushes immediately (for SSE/chunking).
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
			// Strip client auth and config-named headers, then add real credential.
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
			// Don't leak loopback client detail to upstream.
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

		// Extract model from body without consuming it.
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

// Run serves until ctx is cancelled (binds socket here for restartability).
func (b *Broker) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:    b.cfg.Listen,
		Handler: b.handler(),
		// No write deadline: long streaming completions are normal.
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := b.listen()
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

// sniffModel reads the request body, extracts the "model" field, and
// returns a fresh ReadCloser with the same bytes.
func sniffModel(rc io.ReadCloser) (string, io.ReadCloser) {
	if rc == nil {
		return "", http.NoBody
	}
	const maxBody = 1 << 20 // 1 MiB is more than enough
	buf, err := io.ReadAll(io.LimitReader(rc, maxBody+1))
	if err != nil {
		// Pass through what we read + the rest.
		return "", io.NopCloser(io.MultiReader(bytes.NewReader(buf), rc))
	}
	if len(buf) > maxBody {
		// Large request: pass through without sniffing.
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

// Flush forwards flush events from ReverseProxy after each chunk.
func (c *countingResponseWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
