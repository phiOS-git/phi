package query

import (
	"context"
	"strings"
	"testing"
)

func calcQuery(t *testing.T, q string) []Result {
	t.Helper()
	return CalculatorProvider{}.Query(context.Background(), q)
}

func TestCalculatorArithmetic(t *testing.T) {
	r := calcQuery(t, "2 + 2")
	if len(r) != 1 || r[0].Title != "4" {
		t.Fatalf("Query(2 + 2) = %v, want a single result titled 4", r)
	}
	if r[0].Score != 100 {
		t.Errorf("Score = %v, want 100", r[0].Score)
	}
	if r[0].Action.Kind != ActionCopyText || r[0].Action.Data["text"] != "4" {
		t.Errorf("Action = %+v, want copyText 4", r[0].Action)
	}
}

func TestCalculatorGluedConversion(t *testing.T) {
	// The regression this whole change exists for: "100km to m" must work.
	r := calcQuery(t, "100km to m")
	if len(r) == 0 || !strings.HasPrefix(r[0].Title, "100000") {
		t.Fatalf("Query(100km to m) = %v", r)
	}
	if r[0].Rich == nil || r[0].Rich.Kind != "convert" {
		t.Errorf("expected a rich convert payload, got %+v", r[0].Rich)
	}
}

func TestCalculatorSpacedConversionStillWorks(t *testing.T) {
	r := calcQuery(t, "10 km to mi")
	if len(r) == 0 {
		t.Fatal("Query(10 km to mi) returned nothing")
	}
}

func TestCalculatorSolveHasRichSteps(t *testing.T) {
	r := calcQuery(t, "solve x^2 - 4 = 0")
	if len(r) == 0 {
		t.Fatal("no result for solve")
	}
	if r[0].Rich == nil || len(r[0].Rich.Solutions) == 0 {
		t.Fatalf("expected rich solutions, got %+v", r[0].Rich)
	}
}

func TestCalculatorPlotHasCurve(t *testing.T) {
	r := calcQuery(t, "plot sin(x)")
	if len(r) == 0 || r[0].Rich == nil || r[0].Rich.Plot == nil {
		t.Fatalf("expected a plot payload, got %+v", r)
	}
	if len(r[0].Rich.Plot.Points) < 100 {
		t.Errorf("plot has %d points", len(r[0].Rich.Plot.Points))
	}
}

func TestCalculatorRejectsNonExpression(t *testing.T) {
	if r := calcQuery(t, "not an expression"); r != nil {
		t.Errorf("Query on prose should return nil, got %v", r)
	}
}

func TestCalculatorLeavesCurrencyToCurrencyProvider(t *testing.T) {
	if r := calcQuery(t, "100 usd to eur"); r != nil {
		t.Errorf("calculator should not answer a currency query, got %v", r)
	}
}

func TestCalculatorContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := (CalculatorProvider{}).Query(ctx, "2 + 2"); r != nil {
		t.Errorf("a cancelled context should yield no result, got %v", r)
	}
}
