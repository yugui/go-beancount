package print

import (
	"context"
	"io"
	"iter"
	"os"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// testPluginDir is a non-zero *ast.Plugin used as the api.Input.Directive
// fallback span in tests.
var testPluginDir = &ast.Plugin{Span: ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 1}}}

func seqOf(dirs []ast.Directive) iter.Seq2[int, ast.Directive] {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range dirs {
			if !yield(i, d) {
				return
			}
		}
	}
}

func amt(n int64, cur string) ast.Amount {
	var d apd.Decimal
	d.SetInt64(n)
	return ast.Amount{Number: d, Currency: cur}
}

// captureStderr redirects os.Stderr to a pipe, calls f, restores os.Stderr,
// and returns what was written.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	f()

	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stderr: %v", err)
	}
	return string(out)
}

// TestPrintWritesNonEmptyOutput: a single-directive ledger causes the plugin
// to write non-empty content to stderr.
func TestPrintWritesNonEmptyOutput(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date:      time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		Flag:      '*',
		Narration: "Coffee",
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Coffee", Amount: &neg},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Options:    ast.NewOptionValues(),
		Directives: seqOf([]ast.Directive{tx}),
	}

	got := captureStderr(t, func() {
		res, err := apply(context.Background(), in)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if res.Directives != nil {
			t.Errorf("res.Directives = %v, want nil (pass-through plugin)", res.Directives)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("len(res.Diagnostics) = %d, want 0", len(res.Diagnostics))
		}
	})

	if len(got) == 0 {
		t.Error("expected non-empty stderr output, got empty string")
	}
}

// TestPrintReturnsLedgerUnchanged: the plugin returns a zero Result and
// does not mutate the input directive sequence.
func TestPrintReturnsLedgerUnchanged(t *testing.T) {
	pos := amt(50, "EUR")
	neg := amt(-50, "EUR")
	tx := &ast.Transaction{
		Date:      time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		Flag:      '*',
		Narration: "Lunch",
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	origPostings := len(tx.Postings)
	in := api.Input{
		Directive:  testPluginDir,
		Options:    ast.NewOptionValues(),
		Directives: seqOf([]ast.Directive{tx}),
	}

	captureStderr(t, func() {
		res, err := apply(context.Background(), in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Directives != nil {
			t.Errorf("res.Directives = %v, want nil", res.Directives)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("len(res.Diagnostics) = %d, want 0", len(res.Diagnostics))
		}
	})

	if len(tx.Postings) != origPostings {
		t.Errorf("tx.Postings mutated: %d -> %d", origPostings, len(tx.Postings))
	}
}

// TestPrintCanceledContext: the plugin respects a canceled context.
func TestPrintCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := apply(ctx, api.Input{})
	if err == nil {
		t.Fatal("apply error = nil, want non-nil on canceled context")
	}
}

// TestPrintNilDirectives: a nil Directives iterator is handled gracefully —
// only option lines are written.
func TestPrintNilDirectives(t *testing.T) {
	in := api.Input{
		Directive: testPluginDir,
		Options:   ast.NewOptionValues(),
	}

	got := captureStderr(t, func() {
		res, err := apply(context.Background(), in)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if res.Directives != nil {
			t.Errorf("res.Directives = %v, want nil", res.Directives)
		}
	})
	_ = got // options are printed; we only verify no panic
}
