package loader_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/loader"
)

const minimalSrc = `2024-01-01 open Assets:Bank USD
2024-01-01 open Equity:Opening
2024-01-15 * "deposit"
  Assets:Bank        100 USD
  Equity:Opening    -100 USD
`

func TestLoad_String(t *testing.T) {
	ctx := context.Background()
	ledger, err := loader.Load(ctx, minimalSrc)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("Load returned unexpected diagnostic: %s", d.Message)
		}
	}
	if got := ledger.Len(); got != 3 {
		t.Errorf("Directives count = %d, want 3", got)
	}
}

func TestLoadReader_RunsPlugins(t *testing.T) {
	// Unbalanced transaction — the validations plugin must report it.
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Equity:Opening
2024-01-15 * "broken"
  Assets:Bank        100 USD
  Equity:Opening     -50 USD
`
	ctx := context.Background()
	ledger, err := loader.LoadReader(ctx, strings.NewReader(src))
	if err != nil {
		t.Fatalf("loader.LoadReader: %v", err)
	}
	var errCount int
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			errCount++
		}
	}
	if errCount == 0 {
		t.Fatal("expected at least one diagnostic for unbalanced transaction")
	}
}

func TestLoadFile_Equivalent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(path, []byte(minimalSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	ledger, err := loader.LoadFile(ctx, path)
	if err != nil {
		t.Fatalf("loader.LoadFile: %v", err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("LoadFile returned unexpected diagnostic: %s", d.Message)
		}
	}
	if got := ledger.Len(); got != 3 {
		t.Errorf("Directives count = %d, want 3", got)
	}
	// LoadFile must stamp the absolute path into spans.
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Files) == 0 {
		t.Fatalf("LoadFile: ledger.Files is empty")
	}
	if got := ledger.Files[0].Filename; got != abs {
		t.Errorf("Files[0].Filename = %q, want %q", got, abs)
	}
}

func TestLoadCancellation(t *testing.T) {
	// minimalSrc parses without error, so the ctx check inside runBuiltin
	// (the first pipeline step that consults ctx) returns context.Canceled
	// directly rather than a wrapped pipeline error.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := loader.Load(ctx, minimalSrc)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("loader.Load(canceledCtx): err = %v, want context.Canceled", err)
	}
}

// TestLoad_CostAndPriceSaleBalances exercises upstream Beancount's
// "Trading with Capital Gains" example end-to-end: a posting that
// carries both a cost and a price annotation must use the cost as
// its balancing weight, leaving the price to drive the prices
// database while an explicit Income posting absorbs the realized
// gain. The transaction balances exactly when our PostingWeight
// honors that contract.
func TestLoad_CostAndPriceSaleBalances(t *testing.T) {
	const src = `1970-01-01 open Assets:ETrade:IVV
1970-01-01 open Assets:ETrade:Cash
1970-01-01 open Income:ETrade:CapitalGains
1970-01-01 open Equity:Opening

2014-01-01 * "buy"
  Assets:ETrade:IVV    10 IVV {183.07 USD}
  Equity:Opening

2014-07-11 * "Sold shares of S&P 500"
  Assets:ETrade:IVV               -10 IVV {183.07 USD} @ 197.90 USD
  Assets:ETrade:Cash          1979.00 USD
  Income:ETrade:CapitalGains  -148.30 USD
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("unexpected error diagnostic: [%s] %s", d.Code, d.Message)
		}
	}
}

// TestLoad_TotalCostAugmentationWithAutoPostingBalances is the
// minimal regression for the reported bug: a `{{T CUR}}` augmentation
// paired with an auto-posting that absorbs the cost-side of the
// transaction must balance even when T/|units| is non-terminating.
// The reducer's residual computation and the validator's weight
// computation now share a single divide-free path
// (PostingWeight via *Posting.TotalCost), so the auto-posting
// receives an exact JPY residual and tolerance.Infer is not narrowed
// to 10⁻³⁴ by spurious 34-digit fraction.
func TestLoad_TotalCostAugmentationWithAutoPostingBalances(t *testing.T) {
	const src = `1970-01-01 open Assets:A
1970-01-01 * "txn"
  Assets:A          3 STOCK {{ 1 JPY }}
  Assets:A
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("unexpected error diagnostic: [%s] %s", d.Code, d.Message)
		}
	}
}

// TestLoad_TotalCostAugmentationBalances pins the precision-preserving
// behavior of the booking pass for `{{ T CUR }}` augmentations. The
// posting weights cancel exactly in the user-written form (Σ ±T = 0
// JPY), and the booking pass must not rewrite the spec into a per-unit
// form whose value is the non-terminating quotient T/|units|: doing so
// would round in apd's 34-digit context and the transaction-balance
// validator would then reject a residual that is mathematically zero.
func TestLoad_TotalCostAugmentationBalances(t *testing.T) {
	const src = `2025-01-01 open Assets:A JPY,STOCK "NONE"
2025-01-01 open Assets:B JPY,STOCK "STRICT"

2025-01-01 * "txn"
  Assets:A           -4.1 STOCK {{   4.2 JPY }}
  Assets:A         -100   STOCK {{ 100 JPY }}
  Assets:B          104.1 STOCK {{ 104.2 JPY }}
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("unexpected error diagnostic: [%s] %s", d.Code, d.Message)
		}
	}
}

// TestLoad_StrictTotalCostReducingPostingMatchesByDerivedPerUnit pins
// the upstream-beancount behavior that a `{{ T CUR }}` reducing
// posting under STRICT booking is matched by the implicit per-unit
// cost T/|units|. Two augmenting lots at different per-unit costs
// would be ambiguous under a currency-only matcher, but the derived
// per-unit value selects exactly one lot.
func TestLoad_StrictTotalCostReducingPostingMatchesByDerivedPerUnit(t *testing.T) {
	const src = `1970-01-03 open Income:Gain
1970-01-01 open Assets:A "STRICT"

1970-01-01 * "txn"
  Assets:A          10 STOCK { 1 JPY }
  Assets:A

1970-01-02 * "txn"
  Assets:A          10 STOCK { 1.5 JPY }
  Assets:A

1970-01-03 * "txn"
  Assets:A          -10 STOCK {{ 10 JPY }}
  Assets:A
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("loader.Load: unexpected error diagnostic: [%s] %s", d.Code, d.Message)
		}
	}
	// Positive assertion: the booking pass must have installed the 1 JPY
	// lot's cost on the 1970-01-03 reduction. Looking for any other
	// per-unit value (or no Cost at all) means the disambiguation went
	// wrong even if no diagnostic was emitted.
	saleDate := time.Date(1970, 1, 3, 0, 0, 0, 0, time.UTC)
	want := apd.New(1, 0)
	var found bool
	for _, d := range ledger.All() {
		txn, ok := d.(*ast.Transaction)
		if !ok || !txn.Date.Equal(saleDate) {
			continue
		}
		for _, p := range txn.Postings {
			if p.Amount == nil || p.Amount.Currency != "STOCK" {
				continue
			}
			found = true
			cost, ok := p.Cost.(*ast.Cost)
			if !ok {
				t.Errorf("loader.Load: 1970-01-03 STOCK posting Cost = %T, want *ast.Cost", p.Cost)
				continue
			}
			if cost.Number.Cmp(want) != 0 || cost.Currency != "JPY" {
				t.Errorf("loader.Load: 1970-01-03 STOCK posting Cost = %s %s, want 1 JPY", cost.Number.String(), cost.Currency)
			}
		}
	}
	if !found {
		t.Errorf("loader.Load: 1970-01-03 STOCK posting not found in ledger output")
	}
}

// TestLoad_UnresolvableAutoPostingDoesNotCascade verifies that when
// booking fails to resolve an auto-posting (e.g. an already-balanced
// transaction with a redundant auto-posting), the booking-layer
// CodeUnresolvableInterpolation diagnostic is the sole error: the
// downstream pad / balance / validations passes see the transaction
// without the unresolved posting and do not re-emit
// CodeAutoPostingUnresolved.
func TestLoad_UnresolvableAutoPostingDoesNotCascade(t *testing.T) {
	const src = `1970-01-01 open Assets:Cash
1970-01-01 open Expenses:Food
1970-01-01 open Equity:Plug

1970-02-01 * "txn"
  Assets:Cash          100.00 USD
  Expenses:Food       -100.00 USD
  Equity:Plug
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	// Diagnostic codes are stable strings produced by inventory.Error
	// (mapped in pkg/loader/booking) and pkg/validation; ast.Diagnostic
	// carries them as plain strings rather than typed constants.
	var unresolvable, autoUnresolved int
	for _, d := range ledger.Diagnostics {
		if d.Severity != ast.Error {
			continue
		}
		switch d.Code {
		case "unresolvable-interpolation":
			unresolvable++
		case "auto-posting-unresolved":
			autoUnresolved++
		default:
			t.Errorf("loader.Load: unexpected diagnostic [%s] %s", d.Code, d.Message)
		}
	}
	if unresolvable != 1 {
		t.Errorf("loader.Load: unresolvable-interpolation count = %d, want 1", unresolvable)
	}
	if autoUnresolved != 0 {
		t.Errorf("loader.Load: auto-posting-unresolved count = %d, want 0 (must not cascade)", autoUnresolved)
	}
}

// TestLoad_OptionsParseErrorEmittedOnce verifies that a malformed option value
// produces exactly one "invalid-option" diagnostic through the loader pipeline.
func TestLoad_OptionsParseErrorEmittedOnce(t *testing.T) {
	t.Run("malformed option emitted exactly once", func(t *testing.T) {
		const src = `option "inferred_tolerance_multiplier" "not-a-decimal"
option "operating_currency" "USD"
`
		ctx := context.Background()
		ledger, err := loader.Load(ctx, src)
		if err != nil {
			t.Fatalf("loader.Load: %v", err)
		}

		var invalids []ast.Diagnostic
		for _, d := range ledger.Diagnostics {
			if d.Code == "invalid-option" {
				invalids = append(invalids, d)
			}
		}
		if got := len(invalids); got != 1 {
			t.Fatalf("invalid-option diagnostics = %d, want 1; all: %+v", got, ledger.Diagnostics)
		}

		d := invalids[0]
		if d.Severity != ast.Error {
			t.Errorf("Severity = %v, want Error", d.Severity)
		}
		if d.Span == (ast.Span{}) {
			t.Error("Span is zero, want directive-sourced span")
		}
		if got := d.Span.Start.Filename; got != "<input>" {
			t.Errorf("Span.Start.Filename = %q, want %q", got, "<input>")
		}
		if !strings.Contains(d.Message, "inferred_tolerance_multiplier") {
			t.Errorf("Message = %q, want it to contain %q", d.Message, "inferred_tolerance_multiplier")
		}
	})

	t.Run("valid options produce no invalid-option diagnostic", func(t *testing.T) {
		const src = `option "operating_currency" "USD"
option "operating_currency" "JPY"
option "infer_tolerance_from_cost" "TRUE"
option "tolerance_multiplier" "0.25"
`
		ctx := context.Background()
		ledger, err := loader.Load(ctx, src)
		if err != nil {
			t.Fatalf("loader.Load: %v", err)
		}

		for _, d := range ledger.Diagnostics {
			if d.Code == "invalid-option" {
				t.Errorf("unexpected invalid-option diagnostic: %s", d.Message)
			}
		}

		if got := ledger.Options.StringList("operating_currency"); len(got) != 2 || got[0] != "USD" || got[1] != "JPY" {
			t.Errorf("operating_currency = %v, want [USD JPY]", got)
		}
		if !ledger.Options.Bool("infer_tolerance_from_cost") {
			t.Error("infer_tolerance_from_cost = false, want true")
		}
		if d := ledger.Options.Decimal("tolerance_multiplier"); d == nil || d.String() != "0.25" {
			t.Errorf("tolerance_multiplier = %v, want 0.25", d)
		}
	})
}

// TestLoad_CurrencyOnlyCostDistinctWeightCurrencyBalances is the
// end-to-end pin for the user-reported scenario: two currency-only
// cost specs (`{ JPY }`, `{ USD }`) commit to distinct weight
// currencies and resolve cleanly against their respective cash legs,
// producing no error-severity diagnostics.
func TestLoad_CurrencyOnlyCostDistinctWeightCurrencyBalances(t *testing.T) {
	const src = `1970-01-01 open Income:Gain
1970-01-01 open Assets:A A "STRICT"
1970-01-01 open Assets:B B "STRICT"
1970-01-01 open Assets:Cash "STRICT"

1970-01-01 * "txn"
  Assets:A          10 A { JPY }
  Assets:B          10 B { USD }
  Assets:Cash       -100 JPY
  Assets:Cash       -1.00 USD
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("unexpected error diagnostic: [%s] %s", d.Code, d.Message)
		}
	}
}

// TestLoad_AmbiguousMultipleDeferred_SameWeightCurrency_DoesNotCascade
// pins the end-to-end shape of the same-currency-committed ambiguity
// case. Two deferred postings sharing `{ JPY }` are each reported as
// CodeUnresolvableInterpolation by the reducer (one diagnostic per
// posting). The whole JPY group is then dropped — including the
// successful Assets:Cash booking — so the transaction-balance
// validator never sees a nil-Amount posting and emits no additional
// CodeUnbalancedTransaction or CodeAutoPostingUnresolved diagnostic.
// The booking-layer diagnostics are the sole record of the failure.
func TestLoad_AmbiguousMultipleDeferred_SameWeightCurrency_DoesNotCascade(t *testing.T) {
	const src = `1970-01-01 open Assets:A
1970-01-01 open Assets:C
1970-01-01 open Assets:Cash

1970-01-01 * "txn"
  Assets:A          10 A { JPY }
  Assets:C          10 C { JPY }
  Assets:Cash    -100 JPY
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	var ambiguous, unbalanced, autoUnresolved int
	for _, d := range ledger.Diagnostics {
		if d.Severity != ast.Error {
			continue
		}
		switch d.Code {
		case "unresolvable-interpolation":
			if !strings.Contains(d.Message, "multiple unknown posting values") {
				t.Errorf("CodeUnresolvableInterpolation message = %q, want substring %q",
					d.Message, "multiple unknown posting values")
			}
			ambiguous++
		case "unbalanced-transaction":
			unbalanced++
		case "auto-posting-unresolved":
			autoUnresolved++
		default:
			t.Errorf("unexpected diagnostic [%s] %s", d.Code, d.Message)
		}
	}
	if ambiguous != 2 {
		t.Errorf("CodeUnresolvableInterpolation count = %d, want 2", ambiguous)
	}
	if unbalanced != 0 {
		t.Errorf("CodeUnbalancedTransaction count = %d, want 0 (must not cascade)", unbalanced)
	}
	if autoUnresolved != 0 {
		t.Errorf("CodeAutoPostingUnresolved count = %d, want 0 (must not cascade)", autoUnresolved)
	}
}

func TestLoadRawMode(t *testing.T) {
	// In raw mode the built-in pipeline is skipped, so an unbalanced
	// transaction must NOT produce a validations diagnostic.
	const src = `option "plugin_processing_mode" "raw"
2024-01-01 open Assets:Bank USD
2024-01-01 open Equity:Opening
2024-01-15 * "broken"
  Assets:Bank        100 USD
  Equity:Opening     -50 USD
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load (raw): %v", err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("loader.Load(raw): unexpected error diagnostic: %s", d.Message)
		}
	}
}
