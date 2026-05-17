package ast_test

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

// mustLoad is a test helper that calls ast.Load and fails fatally on error.
func mustLoad(t *testing.T, src string) *ast.Ledger {
	t.Helper()
	ledger, err := ast.Load(src)
	if err != nil {
		t.Fatalf("ast.Load: %v", err)
	}
	return ledger
}

// checkMostCommon fails the test if MostCommon does not return (wantPrec, true).
func checkMostCommon(t *testing.T, p *ast.PrecisionProfile, currency string, wantPrec int) {
	t.Helper()
	prec, ok := p.MostCommon(currency)
	if !ok {
		t.Errorf("MostCommon(%q): ok = false, want true", currency)
		return
	}
	if prec != wantPrec {
		t.Errorf("MostCommon(%q) = %d, want %d", currency, prec, wantPrec)
	}
}

// checkNotObserved fails the test if MostCommon returns ok=true.
func checkNotObserved(t *testing.T, p *ast.PrecisionProfile, currency string) {
	t.Helper()
	if _, ok := p.MostCommon(currency); ok {
		t.Errorf("MostCommon(%q): ok = true, want false (should be unobserved)", currency)
	}
}

func TestPrecisionProfileLoad_TransactionPostings(t *testing.T) {
	src := `
2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food JPY

2024-06-01 * "lunch"
  Expenses:Food  1000 JPY
  Assets:Bank   -8.50 USD
`
	ledger := mustLoad(t, src)
	p := ledger.PrecisionProfile
	if p == nil {
		t.Fatal("PrecisionProfile is nil after Load")
	}
	checkMostCommon(t, p, "USD", 2)
	checkMostCommon(t, p, "JPY", 0)
}

func TestPrecisionProfileLoad_BalanceDirective(t *testing.T) {
	// The balance directive at 3dp should combine with the transaction at 2dp.
	// MostCommon picks 2dp because it appears more often.
	src := `
2024-01-01 open Assets:Bank USD

2024-06-01 * "a"
  Assets:Bank  10.00 USD
  Assets:Bank  -5.00 USD

2024-06-02 balance Assets:Bank  5.000 USD
`
	ledger := mustLoad(t, src)
	p := ledger.PrecisionProfile
	if p == nil {
		t.Fatal("PrecisionProfile is nil after Load")
	}
	checkMostCommon(t, p, "USD", 2)
}

func TestPrecisionProfileLoad_PriceDirective(t *testing.T) {
	src := `
2024-01-01 price BTC  42000.1234 USD
`
	ledger := mustLoad(t, src)
	p := ledger.PrecisionProfile
	if p == nil {
		t.Fatal("PrecisionProfile is nil after Load")
	}
	checkMostCommon(t, p, "USD", 4)
}

func TestPrecisionProfileLoad_CostNotObserved(t *testing.T) {
	// The cost annotation `{30000.00 USD}` must NOT be observed.
	// The auto-balanced posting (nil Amount) contributes nothing either.
	// With no direct USD posting amount, USD should be unobserved.
	src := `
2024-01-01 open Assets:Broker BTC
2024-01-01 open Assets:Cash USD

2024-06-01 * "buy"
  Assets:Broker  10 BTC {30000.00 USD}
  Assets:Cash
`
	ledger := mustLoad(t, src)
	p := ledger.PrecisionProfile
	if p == nil {
		t.Fatal("PrecisionProfile is nil after Load")
	}
	checkMostCommon(t, p, "BTC", 0)
	checkNotObserved(t, p, "USD")
}

func TestPrecisionProfileLoad_PostingPriceAnnotationNotObserved(t *testing.T) {
	// The `@ 1.23456 JPY` annotation must NOT be observed.
	// With no other JPY observations, JPY should be unobserved.
	src := `
2024-01-01 open Assets:Bank USD
2024-01-01 open Assets:FX JPY

2024-06-01 * "fx"
  Assets:FX     100.00 USD @ 145.12345 JPY
  Assets:Bank
`
	ledger := mustLoad(t, src)
	p := ledger.PrecisionProfile
	if p == nil {
		t.Fatal("PrecisionProfile is nil after Load")
	}
	// USD at 2dp is observed from the posting amount.
	checkMostCommon(t, p, "USD", 2)
	// JPY from the price annotation must not be observed.
	checkNotObserved(t, p, "JPY")
}

func TestPrecisionProfileLoad_HandBuiltLedgerIsNil(t *testing.T) {
	l := &ast.Ledger{}
	if l.PrecisionProfile != nil {
		t.Errorf("hand-built Ledger.PrecisionProfile = %v, want nil", l.PrecisionProfile)
	}
}

func TestPrecisionProfileLoad_MutatorsDoNotRefresh(t *testing.T) {
	src := `
2024-01-01 open Assets:Bank USD

2024-06-01 * "a"
  Assets:Bank  1.00 USD
  Assets:Bank  -1.00 USD
`
	ledger := mustLoad(t, src)
	original := ledger.PrecisionProfile

	// Add a directive carrying a EUR amount; if PrecisionProfile were refreshed it would appear.
	extra := &ast.Transaction{
		Date:      time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),
		Narration: "extra",
	}
	ledger.Insert(extra)

	if ledger.PrecisionProfile != original {
		t.Error("Insert changed PrecisionProfile; mutators must not refresh it")
	}

	ledger.InsertAll([]ast.Directive{extra})
	if ledger.PrecisionProfile != original {
		t.Error("InsertAll changed PrecisionProfile; mutators must not refresh it")
	}

	ledger.ReplaceAll([]ast.Directive{extra})
	if ledger.PrecisionProfile != original {
		t.Error("ReplaceAll changed PrecisionProfile; mutators must not refresh it")
	}
}
