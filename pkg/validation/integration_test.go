package validation_test

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/balance"
	"github.com/yugui/go-beancount/pkg/validation/pad"
	"github.com/yugui/go-beancount/pkg/validation/validations"
)

// loadFixture loads a beancount fixture from the testdata directory and
// fails the test if loading produces any error-severity diagnostics.
func loadFixture(t *testing.T, name string) *ast.Ledger {
	t.Helper()
	path := filepath.Join("testdata", name)
	ledger, err := ast.LoadFile(path)
	if err != nil {
		t.Fatalf("ast.LoadFile(%q): %v", path, err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Fatalf("ast.LoadFile(%q): diagnostic: %s", path, d.Message)
		}
	}
	return ledger
}

// runPipeline runs the 3-plugin pipeline (pad -> balance -> validations)
// against ledger and returns the merged diagnostics from every plugin in
// pipeline order. ledger is mutated in place via ReplaceAll whenever a
// plugin returns non-nil Directives; callers should not rely on the
// ledger's pre-call contents afterward.
func runPipeline(t *testing.T, ledger *ast.Ledger) []api.Error {
	t.Helper()
	ctx := context.Background()
	opts := options.BuildRaw(ledger)

	padRes, err := pad.Apply(ctx, api.Input{
		Directives: ledger.All(),
		Options:    opts,
	})
	if err != nil {
		t.Fatalf("pad.Apply: %v", err)
	}
	if padRes.Directives != nil {
		ledger.ReplaceAll(padRes.Directives)
	}

	balRes, err := balance.Apply(ctx, api.Input{
		Directives: ledger.All(),
		Options:    opts,
	})
	if err != nil {
		t.Fatalf("balance.Apply: %v", err)
	}
	if balRes.Directives != nil {
		ledger.ReplaceAll(balRes.Directives)
	}

	valRes, err := validations.Apply(ctx, api.Input{
		Directives: ledger.All(),
		Options:    opts,
	})
	if err != nil {
		t.Fatalf("validations.Apply: %v", err)
	}

	var all []api.Error
	all = append(all, padRes.Errors...)
	all = append(all, balRes.Errors...)
	all = append(all, valRes.Errors...)
	return all
}

// sortPipelineErrors returns a new slice containing errs sorted by
// (filename, byte offset, code) so tests can assert a deterministic
// ordering independent of which plugin emitted which error.
func sortPipelineErrors(errs []api.Error) []api.Error {
	out := make([]api.Error, len(errs))
	copy(out, errs)
	slices.SortStableFunc(out, func(a, b api.Error) int {
		if c := cmp.Compare(a.Span.Start.Filename, b.Span.Start.Filename); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Span.Start.Offset, b.Span.Start.Offset); c != 0 {
			return c
		}
		return cmp.Compare(a.Code, b.Code)
	})
	return out
}

// TestPipelineGoodLedger exercises the pad->balance->validations pipeline
// against a clean fixture and asserts no diagnostics.
func TestPipelineGoodLedger(t *testing.T) {
	ledger := loadFixture(t, "good_ledger.beancount")
	errs := runPipeline(t, ledger)
	if len(errs) != 0 {
		t.Errorf("good_ledger.beancount: got %d pipeline errors, want 0", len(errs))
		for _, e := range errs {
			t.Logf("  %s", e)
		}
	}
}

// TestPipelinePadAndBalance runs the full pipeline against the fixture
// that exercises pad/balance interaction. The pad plugin should
// synthesize a balancing transaction that satisfies the downstream
// balance assertion, so no diagnostics should be emitted.
func TestPipelinePadAndBalance(t *testing.T) {
	ledger := loadFixture(t, "pad_and_balance.beancount")
	errs := runPipeline(t, ledger)
	if len(errs) != 0 {
		t.Errorf("pad_and_balance.beancount: got %d pipeline errors, want 0", len(errs))
		for _, e := range errs {
			t.Logf("  %s", e)
		}
	}
}

// TestPipelineBadLedger asserts that the merged diagnostic set across
// the 3 plugins matches the expected set for bad_ledger.beancount. We
// compare as a sorted-by-(filename, offset, code) sequence, because
// plugins run in a fixed order (pad, balance, validations) and callers
// that need a stable global ordering sort by (filename, offset, code)
// themselves.
func TestPipelineBadLedger(t *testing.T) {
	ledger := loadFixture(t, "bad_ledger.beancount")
	errs := sortPipelineErrors(runPipeline(t, ledger))

	type got struct {
		Code     string
		Basename string
	}
	var actual []got
	for _, e := range errs {
		actual = append(actual, got{
			Code:     e.Code,
			Basename: filepath.Base(e.Span.Start.Filename),
		})
	}

	want := []got{
		{string(validation.CodeDuplicateOpen), "bad_ledger.beancount"},
		{string(validation.CodeAccountNotOpen), "bad_ledger.beancount"},
		{string(validation.CodeUnbalancedTransaction), "bad_ledger.beancount"},
		{string(validation.CodeCurrencyNotAllowed), "bad_ledger.beancount"},
		{string(validation.CodeCurrencyNotAllowed), "bad_ledger.beancount"},
		{string(validation.CodeBalanceMismatch), "bad_ledger.beancount"},
	}

	if len(actual) != len(want) {
		t.Fatalf("bad_ledger.beancount: got %d pipeline errors, want %d\nactual: %+v\nfull:\n%s",
			len(actual), len(want), actual, formatAPIErrors(errs))
	}
	for i, w := range want {
		a := actual[i]
		if a.Code != w.Code || a.Basename != w.Basename {
			t.Errorf("pipeline error[%d] = %+v, want %+v (message: %q)", i, a, w, errs[i].Message)
		}
	}

	// Verify determinism of ordering: non-decreasing by offset.
	for i := 1; i < len(errs); i++ {
		prev, cur := errs[i-1].Span.Start, errs[i].Span.Start
		if prev.Filename == cur.Filename && prev.Offset > cur.Offset {
			t.Errorf("pipeline errors not sorted by offset at index %d: %d > %d", i, prev.Offset, cur.Offset)
		}
	}
}

// TestPipelineDeterministicOrder runs the pipeline twice against the
// same fixture (loading a fresh ledger each time so directive
// replacement is isolated) and verifies the sorted diagnostic sequence
// is identical across runs.
func TestPipelineDeterministicOrder(t *testing.T) {
	first := sortPipelineErrors(runPipeline(t, loadFixture(t, "bad_ledger.beancount")))
	second := sortPipelineErrors(runPipeline(t, loadFixture(t, "bad_ledger.beancount")))
	if len(first) != len(second) {
		t.Fatalf("pipeline is non-deterministic: %d vs %d errors", len(first), len(second))
	}
	for i := range first {
		if first[i].Code != second[i].Code || first[i].Span.Start != second[i].Span.Start {
			t.Errorf("pipeline error[%d] differs between runs: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func formatAPIErrors(errs []api.Error) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString("  ")
		b.WriteString(e.Error())
		b.WriteByte('\n')
	}
	return b.String()
}
