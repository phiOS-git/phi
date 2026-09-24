package view

import (
	"testing"

	"phi/internal/query"
)

// TestQueryResultsEmptyIsJSONArray covers the shape phi-shell's Launcher
// requires: it only trusts Array.isArray on the unstyled output, so a nil
// or empty result set must still marshal to "[]", never the JSON "null"
// json.Marshal(nil) would otherwise produce.
func TestQueryResultsEmptyIsJSONArray(t *testing.T) {
	if got := QueryResults(nil, false); got != "[]\n" {
		t.Errorf("QueryResults(nil, false) = %q, want %q", got, "[]\n")
	}
	if got := QueryResults([]query.Result{}, false); got != "[]\n" {
		t.Errorf("QueryResults([]query.Result{}, false) = %q, want %q", got, "[]\n")
	}
}
