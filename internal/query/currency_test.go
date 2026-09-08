package query

import (
	"testing"
	"time"
)

func TestParseCurrencyQuery(t *testing.T) {
	c, ok := parseCurrencyQuery("100 usd to eur")
	if !ok {
		t.Fatal("expected a match")
	}
	if c.value != 100 || c.fromUnit != "USD" || c.toUnit != "EUR" {
		t.Errorf("parseCurrencyQuery = %+v", c)
	}

	if _, ok := parseCurrencyQuery("100 usd toward eur"); ok {
		t.Error("only 'to'/'in' should be accepted as the connector")
	}
	if _, ok := parseCurrencyQuery("100 dollars to eur"); ok {
		t.Error("a non-three-letter code must not match")
	}
	if _, ok := parseCurrencyQuery("2 + 2"); ok {
		t.Error("plain arithmetic must not be read as a currency query")
	}
}

func TestCurrencyCacheFreshness(t *testing.T) {
	c := currencyCache{
		"USD_EUR": {Rate: 0.86, FetchedAt: time.Now()},
		"USD_JPY": {Rate: 150, FetchedAt: time.Now().Add(-48 * time.Hour)},
	}

	if rate, fresh := c.rate("USD", "EUR"); rate != 0.86 || !fresh {
		t.Errorf("USD_EUR: rate=%v fresh=%v, want 0.86 true", rate, fresh)
	}
	if rate, fresh := c.rate("USD", "JPY"); rate != 150 || fresh {
		t.Errorf("USD_JPY (48h old): rate=%v fresh=%v, want 150 false (stale)", rate, fresh)
	}
	if rate, fresh := c.rate("USD", "GBP"); rate != 0 || fresh {
		t.Errorf("USD_GBP (no entry): rate=%v fresh=%v, want 0 false", rate, fresh)
	}
}

// CurrencyProvider.Query itself is deliberately not exercised here: it
// reads the real $XDG_STATE_HOME/phi cache and, on a miss, fires a
// background network call (refreshCurrencyCache) — not something a unit
// test should trigger. parseCurrencyQuery and currencyCache.rate above are
// the pure logic; the integration is flagged in this file's own header as
// unverified rather than exercised impurely just to claim coverage.
