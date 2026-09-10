package query

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"phi/internal/mathx"
	"phi/internal/state"
)

// CurrencyProvider converts between currency codes using a cached
// exchange rate (S-33 AGENT: "a rates source with no API key, cached,
// falling back to the last known value with no network"). The API
// (api.frankfurter.dev, ECB-sourced, needs no key) was verified LIVE from
// this agent's own environment before writing this file — a real GET
// request, response shape confirmed, not assumed.
//
// REAL BUG found on first real-hardware verification (phi-shell's own
// Launcher, M4): the original design fired the refresh as `go
// refreshCurrencyCache(...)` — a goroutine. `phi query` is a FRESH,
// SHORT-LIVED PROCESS per invocation (this package's own header, cold
// start), and a goroutine does not survive its process exiting — Query()
// returns almost immediately (the cache read is local and instant), `phi
// query`'s own caller prints the results and the process ends, and the Go
// runtime kills every goroutine at that point, mid-HTTP-request. The rate
// cache could never actually populate: every single invocation started
// the same doomed fetch and died before it finished. Fixed by spawning a
// genuinely detached OS-level CHILD PROCESS instead (spawnCurrencyRefresh
// below) — `phi query refresh-currency FROM TO`, its own hidden CLI
// sub-verb (internal/cli/query.go), started via exec.Command with Setsid
// so it survives the parent's exit as an orphan, does the fetch
// synchronously with a real 5s deadline, and writes the cache for the
// NEXT query to read.
//
// providerTimeout (query.go, 120ms) is still far too short for a real
// HTTPS round trip, so the query path itself still never makes one
// directly — it only ever reads the on-disk cache and, when there's
// nothing usable yet, returns a transient ActionLoading result (see
// query.go) so the launcher can show something and try again shortly,
// instead of silently returning nothing the way this provider used to.
type CurrencyProvider struct{}

func (CurrencyProvider) Name() string { return "currency" }

const currencyCacheMaxAge = 24 * time.Hour

func (p CurrencyProvider) Query(_ context.Context, q string) []Result {
	conv, ok := parseCurrencyQuery(q)
	if !ok {
		return nil
	}
	cache := loadCurrencyCache()
	rate, fresh := cache.rate(conv.fromUnit, conv.toUnit)
	if !fresh {
		spawnCurrencyRefresh(conv.fromUnit, conv.toUnit)
	}
	if rate == 0 {
		// Nothing cached at all yet: a transient result, not silence, so
		// the launcher has something to show and a reason to ask again in
		// a moment (Action.Kind == ActionLoading, phi-shell/CLAUDE.md's
		// own new convention for "a provider is working on it").
		return []Result{{
			ID: "currency:" + q, Provider: p.Name(),
			Title: "Fetching exchange rate…", Subtitle: q, Score: 100,
			Action: Action{Kind: ActionLoading},
		}}
	}
	out := conv.value * rate
	text := mathx.FormatNumber(out)
	return []Result{{
		ID: "currency:" + q, Provider: p.Name(),
		Title: text + " " + conv.toUnit, Subtitle: q, Score: 100,
		Action: Action{Kind: ActionCopyText, Data: map[string]string{"text": text}},
	}}
}

// spawnCurrencyRefresh starts `phi query refresh-currency FROM TO` as a
// detached child and does not wait for it — see this file's own header
// for why a goroutine cannot do this job. os.Executable() resolves the
// currently-running phi binary's own path; a failure there (should not
// happen for an installed binary) just skips the refresh attempt,
// consistent with every other "degrade rather than fail the query" choice
// in this package.
func spawnCurrencyRefresh(from, to string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "query", "refresh-currency", from, to)
	cmd.SysProcAttr = detachedSysProcAttr()
	_ = cmd.Start()
}

// parseCurrencyQuery matches "<number> <CODE> to|in <CODE>" — the same
// shape parseConversion (calculator.go) uses for physical units, kept as a
// separate function because a currency code is not a fixed, known-in-
// advance set the way length/mass units are: any three-letter code is
// accepted here, and it is the cache/API that decides whether it is real.
type currencyQuery struct {
	value            float64
	fromUnit, toUnit string
}

// currencySymbols maps the common single-character currency signs to their
// ISO code so "$100 to eur" and "100 usd to eur" both parse.
var currencySymbols = strings.NewReplacer(
	"$", " usd ", "€", " eur ", "£", " gbp ", "¥", " jpy ",
	"₹", " inr ", "₽", " rub ", "₩", " krw ", "₺", " try ",
	"₪", " ils ", "₫", " vnd ", "R$", " brl ", "CHF", " chf ",
)

func parseCurrencyQuery(s string) (currencyQuery, bool) {
	s = currencySymbols.Replace(s)
	// Split a number glued to a code: "100usd" -> "100 usd".
	s = reGluedNumberCode.ReplaceAllString(s, "$1 $2")
	// Normalise the arrow connectors the physical-unit parser also takes.
	s = strings.NewReplacer("->", " to ", "=>", " to ", "→", " to ", " into ", " to ", " as ", " to ").Replace(s)

	fields := strings.Fields(s)

	// "<CODE> to <CODE>" with an implicit amount of 1.
	if len(fields) == 3 && isConnector(fields[1]) {
		if from, to, ok := codePair(fields[0], fields[2]); ok {
			return currencyQuery{value: 1, fromUnit: from, toUnit: to}, true
		}
	}
	if len(fields) != 4 {
		return currencyQuery{}, false
	}
	if !isConnector(fields[2]) {
		return currencyQuery{}, false
	}
	val, err := strconv.ParseFloat(strings.ReplaceAll(fields[0], ",", ""), 64)
	if err != nil {
		return currencyQuery{}, false
	}
	from, to, ok := codePair(fields[1], fields[3])
	if !ok {
		return currencyQuery{}, false
	}
	return currencyQuery{value: val, fromUnit: from, toUnit: to}, true
}

var reGluedNumberCode = regexp.MustCompile(`(?i)(\d)([a-z]{3})\b`)

func isConnector(s string) bool {
	switch strings.ToLower(s) {
	case "to", "in":
		return true
	}
	return false
}

func codePair(a, b string) (from, to string, ok bool) {
	from, to = strings.ToUpper(a), strings.ToUpper(b)
	if len(from) != 3 || len(to) != 3 || !isAlpha(strings.ToLower(from)) || !isAlpha(strings.ToLower(to)) {
		return "", "", false
	}
	return from, to, true
}

type currencyCacheEntry struct {
	Rate      float64   `json:"rate"`
	FetchedAt time.Time `json:"fetchedAt"`
}

type currencyCache map[string]currencyCacheEntry // key: currencyPairKey(from, to)

func currencyPairKey(from, to string) string { return from + "_" + to }

func currencyCachePath() (string, error) {
	dir, err := state.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "currency-cache.json"), nil
}

func loadCurrencyCache() currencyCache {
	path, err := currencyCachePath()
	if err != nil {
		return currencyCache{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return currencyCache{}
	}
	var c currencyCache
	if err := json.Unmarshal(data, &c); err != nil {
		return currencyCache{}
	}
	return c
}

// rate returns the cached rate and whether it is fresh enough that no
// background refresh is needed. rate == 0 with fresh == false means
// "no entry at all yet" — the caller's cue to answer nothing this time.
func (c currencyCache) rate(from, to string) (rate float64, fresh bool) {
	e, ok := c[currencyPairKey(from, to)]
	if !ok {
		return 0, false
	}
	return e.Rate, time.Since(e.FetchedAt) < currencyCacheMaxAge
}

// RefreshCurrencyCache fetches a fresh rate and writes it to the cache.
// Exported: internal/cli's "query refresh-currency" sub-verb (the detached
// child spawnCurrencyRefresh starts) calls this directly and synchronously
// — it IS the whole job of that child process, not a fire-and-forget
// helper called from within a live query anymore (see this file's own
// header for why the previous goroutine-based version of that idea could
// never work).
func RefreshCurrencyCache(from, to string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reqURL := "https://api.frankfurter.dev/v1/latest?base=" + from + "&symbols=" + to
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	var parsed struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return
	}
	rate, ok := parsed.Rates[to]
	if !ok {
		return
	}

	path, err := currencyCachePath()
	if err != nil {
		return
	}
	cache := loadCurrencyCache()
	if cache == nil {
		cache = currencyCache{}
	}
	cache[currencyPairKey(from, to)] = currencyCacheEntry{Rate: rate, FetchedAt: time.Now()}
	data, err := json.Marshal(cache)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
