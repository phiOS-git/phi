package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestBroker wires a Broker straight to a given upstream URL, bypassing
// broker.json / the key file so the streaming and header behaviour can be
// tested in isolation.
func newTestBroker(t *testing.T, upstream string, cfg func(*BrokerConfig)) *Broker {
	t.Helper()
	bc := BrokerConfig{
		Listen:     "127.0.0.1:0",
		Upstream:   upstream,
		AuthHeader: "x-api-key",
		AuthValue:  "{key}",
	}
	if cfg != nil {
		cfg(&bc)
	}
	m, err := newMeter(filepath.Join(t.TempDir(), "meter.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	tgt, err := url.Parse(upstream)
	if err != nil {
		t.Fatal(err)
	}
	b := &Broker{inst: A1, cfg: bc, key: "SECRET-KEY", target: tgt, meter: m}
	if bc.RateLimit.Requests > 0 {
		w := time.Duration(bc.RateLimit.WindowSeconds) * time.Second
		if w <= 0 {
			w = time.Minute
		}
		b.lim = newBucket(bc.RateLimit.Requests, w)
	}
	return b
}

// serve starts the broker handler on an ephemeral port and returns its base
// URL plus a stop func.
func serve(t *testing.T, b *Broker) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: b.handler()}
	go srv.Serve(ln)
	return "http://" + ln.Addr().String(), func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// TestBrokerStreamsWithoutBuffering is V-09: chunks emitted by upstream with
// a delay between them must reach the client with that delay preserved, not
// all at once at the end.
func TestBrokerStreamsWithoutBuffering(t *testing.T) {
	const chunks = 5
	const gap = 60 * time.Millisecond

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream ResponseWriter is not a Flusher")
			return
		}
		for i := 0; i < chunks; i++ {
			fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			fl.Flush()
			time.Sleep(gap)
		}
	}))
	defer up.Close()

	b := newTestBroker(t, up.URL, nil)
	base, stop := serve(t, b)
	defer stop()

	resp, err := http.Post(base+"/v1/messages", "application/json", strings.NewReader(`{"model":"x","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	var arrivals []time.Time
	start := time.Now()
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data: chunk-") {
			arrivals = append(arrivals, time.Now())
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(arrivals) != chunks {
		t.Fatalf("got %d chunks, want %d", len(arrivals), chunks)
	}
	// If the broker buffered, every arrival would be bunched at the end
	// (~chunks*gap). Unbuffered, the first chunk arrives quickly and the
	// last one much later — assert a real spread.
	firstOffset := arrivals[0].Sub(start)
	lastOffset := arrivals[len(arrivals)-1].Sub(start)
	if firstOffset > gap*2 {
		t.Errorf("first chunk took %v, expected it promptly (buffering?)", firstOffset)
	}
	if spread := lastOffset - firstOffset; spread < gap*time.Duration(chunks-1)/2 {
		t.Errorf("arrival spread %v too small; stream looks buffered", spread)
	}
}

// TestBrokerInjectsKeyAndStripsClientAuth: the client's own auth attempt is
// dropped and the real key is attached (§6.2). The key must never appear in
// what the client can see.
func TestBrokerInjectsKeyAndStripsClientAuth(t *testing.T) {
	var gotHeader http.Header
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		io.WriteString(w, "ok")
	}))
	defer up.Close()

	b := newTestBroker(t, up.URL, func(bc *BrokerConfig) {
		bc.AuthHeader = "x-api-key"
		bc.AuthValue = "{key}"
		bc.ExtraHeaders = map[string]string{"anthropic-version": "2023-06-01"}
	})
	base, stop := serve(t, b)
	defer stop()

	req, _ := http.NewRequest("POST", base+"/v1/messages", strings.NewReader("{}"))
	req.Header.Set("x-api-key", "CLIENT-PLACEHOLDER")
	req.Header.Set("Authorization", "Bearer CLIENT-PLACEHOLDER")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if got := gotHeader.Get("x-api-key"); got != "SECRET-KEY" {
		t.Errorf("upstream x-api-key = %q, want the real key", got)
	}
	if got := gotHeader.Get("Authorization"); got != "" {
		t.Errorf("client Authorization leaked upstream: %q", got)
	}
	if got := gotHeader.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("extra header not attached: %q", got)
	}
}

// TestBrokerRateLimit: once the window count is reached, further requests
// get 429 and never reach upstream.
func TestBrokerRateLimit(t *testing.T) {
	var upstreamHits int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		io.WriteString(w, "ok")
	}))
	defer up.Close()

	b := newTestBroker(t, up.URL, func(bc *BrokerConfig) {
		bc.RateLimit.Requests = 2
		bc.RateLimit.WindowSeconds = 60
	})
	base, stop := serve(t, b)
	defer stop()

	codes := make([]int, 0, 4)
	for i := 0; i < 4; i++ {
		resp, err := http.Post(base+"/v1/messages", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		codes = append(codes, resp.StatusCode)
		resp.Body.Close()
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 || codes[3] != 429 {
		t.Errorf("status sequence = %v, want [200 200 429 429]", codes)
	}
	if upstreamHits != 2 {
		t.Errorf("upstream hit %d times, want 2 (rate-limited requests must not forward)", upstreamHits)
	}
}

func TestBrokerRequireLoopback(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:8789", "localhost:1", "[::1]:9"} {
		if err := requireLoopback(ok); err != nil {
			t.Errorf("requireLoopback(%q) = %v, want nil", ok, err)
		}
	}
	// 0.0.0.0 (all interfaces) is the case §5.3 / V-17 exists to forbid;
	// a routable hostname and the IPv6 documentation range stand in for
	// "some real address" without putting a machine literal in the repo.
	for _, bad := range []string{"0.0.0.0:8789", "example.com:443", "[2001:db8::1]:443"} {
		if err := requireLoopback(bad); err == nil {
			t.Errorf("requireLoopback(%q) = nil, want error", bad)
		}
	}
}

func TestRequireLocalListen(t *testing.T) {
	good := []string{"127.0.0.1:8789", "localhost:1", "[::1]:9", "unix:/run/phi-agent/net/broker-a2.sock"}
	for _, a := range good {
		if err := requireLocalListen(a); err != nil {
			t.Errorf("requireLocalListen(%q) = %v, want nil", a, err)
		}
	}
	bad := []string{"0.0.0.0:8789", "example.com:443", "[2001:db8::1]:443", "unix:"}
	for _, a := range bad {
		if err := requireLocalListen(a); err == nil {
			t.Errorf("requireLocalListen(%q) = nil, want error", a)
		}
	}
	// requireLoopback still rejects unix.
	if err := requireLoopback("unix:/tmp/x.sock"); err == nil {
		t.Error("requireLoopback should reject a unix address")
	}
}

func TestBrokerUnixListen(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer up.Close()

	sock := filepath.Join(t.TempDir(), "broker.sock")
	b := newTestBroker(t, up.URL, func(bc *BrokerConfig) { bc.Listen = "unix:" + sock })

	ln, err := b.listen()
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: b.handler()}
	go srv.Serve(ln)
	defer srv.Close()

	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	resp, err := c.Post("http://unix/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBrokerMeterRecords(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello")
	}))
	defer up.Close()

	meterPath := filepath.Join(t.TempDir(), "m.jsonl")
	m, err := newMeter(meterPath)
	if err != nil {
		t.Fatal(err)
	}
	b := newTestBroker(t, up.URL, nil)
	b.meter = m
	base, stop := serve(t, b)
	defer stop()

	resp, _ := http.Post(base+"/v1/messages", "application/json", strings.NewReader(`{"model":"claude-x"}`))
	resp.Body.Close()

	data, err := os.ReadFile(meterPath)
	if err != nil {
		t.Fatal(err)
	}
	var rec meterRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &rec); err != nil {
		t.Fatalf("meter line not JSON: %v (%q)", err, data)
	}
	if rec.Model != "claude-x" || rec.Status != 200 || rec.Path != "/v1/messages" {
		t.Errorf("meter record = %+v", rec)
	}
}
