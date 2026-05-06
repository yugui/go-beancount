package loader_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// TestLoad_TotalCostAugmentationBalances pins the precision-preserving
// behavior of the booking pass for `{{ T CUR }}` augmentations. The
// posting weights cancel exactly in the user-written form (Σ ±T = 0
// JPY), and the booking pass must not rewrite the spec into a per-unit
// form whose value is the non-terminating quotient T/|units|: doing so
// would round in apd's 34-digit context and the transaction-balance
// validator would then reject a residual that is mathematically zero.
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
