package query

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"phi/internal/state"
)

// CurrencyProvider converts between currency codes using a cached
// exchange rate (S-33 AGENT: "a rates source with no API key, cached,
// falling back to the last known value with no network"). The API
// (api.frankfurter.dev, ECB-sourced, needs no key) was verified LIVE from
// this agent's own environment before writing this file — a real GET
// request, response shape confirmed, not assumed — but this package's own
// call to it has never run end-to-end: that needs network access no
// session on this project has to a real phiOS host. Verified API,
// unverified integration — flagged plainly rather than claimed either way.
//
// providerTimeout (query.go, 120ms) is far too short for a real HTTPS
// round trip, so Query never makes one directly: it reads the on-disk
// cache, which holds whatever rate was last fetched, and answers from
// that — "falling back to the last known value with no network" is not a
// fallback path here, it is the only path any single query ever takes. A
// stale or missing cache entry triggers a background refresh (its own
// goroutine, its own 5s deadline, independent of the query that triggered
// it) so the NEXT query sees a fresher rate; a query against a cache with
// no entry at all yet returns nothing, the same as any other provider that
// cannot answer.
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
		go refreshCurrencyCache(conv.fromUnit, conv.toUnit)
	}
	if rate == 0 {
		return nil
	}
	out := conv.value * rate
	text := formatNumber(out)
	return []Result{{
		ID: "currency:" + q, Provider: p.Name(),
		Title: text + " " + conv.toUnit, Subtitle: q, Score: 100,
		Action: Action{Kind: ActionCopyText, Data: map[string]string{"text": text}},
	}}
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

func parseCurrencyQuery(s string) (currencyQuery, bool) {
	fields := strings.Fields(s)
	if len(fields) != 4 {
		return currencyQuery{}, false
	}
	if fields[2] != "to" && fields[2] != "in" {
		return currencyQuery{}, false
	}
	val, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return currencyQuery{}, false
	}
	from, to := strings.ToUpper(fields[1]), strings.ToUpper(fields[3])
	if len(from) != 3 || len(to) != 3 {
		return currencyQuery{}, false
	}
	return currencyQuery{value: val, fromUnit: from, toUnit: to}, true
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

// refreshCurrencyCache fetches a fresh rate and writes it to the cache for
// the NEXT query to read. Deliberately returns nothing to the query that
// triggered it: by the time an HTTP round trip could complete, that
// query's own providerTimeout has already elapsed and phi query has
// already printed its results and exited.
func refreshCurrencyCache(from, to string) {
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
