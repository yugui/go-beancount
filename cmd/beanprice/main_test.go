package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// fakeSource is a minimal LatestSource that returns a fixed price per
// query. It is registered exactly once under "fake" so the package-
// global pkg/quote registry does not have to be torn down between
// tests; pkg/quote.Register panics on duplicates.
type fakeSource struct{}

func (fakeSource) Name() string { return "fake" }
func (fakeSource) Capabilities() api.Capabilities {
	return api.Capabilities{SupportsLatest: true, BatchPairs: true}
}

func (fakeSource) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	out := make([]ast.Price, 0, len(q))
	for _, query := range q {
		var d apd.Decimal
		if _, _, err := d.SetString("1.00"); err != nil {
			return nil, nil, err
		}
		out = append(out, ast.Price{
			Date:      time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			Commodity: query.Pair.Commodity,
			Amount:    ast.Amount{Number: d, Currency: query.Pair.QuoteCurrency},
		})
	}
	return out, nil, nil
}

var registerFakeOnce sync.Once

func registerFake(t *testing.T) {
	t.Helper()
	registerFakeOnce.Do(func() {
		quote.Register("fake", fakeSource{})
	})
}

func TestReport_NoErrorsExitsZero(t *testing.T) {
	var buf bytes.Buffer
	if got := report(&buf, nil, false); got != 0 {
		t.Errorf("report(nil) = %d, want 0", got)
	}
	if buf.Len() != 0 {
		t.Errorf("report wrote %q, want empty", buf.String())
	}
}

func TestReport_ErrorReturnsExit1(t *testing.T) {
	var buf bytes.Buffer
	diags := []ast.Diagnostic{{
		Code:     "broken",
		Message:  "oops",
		Severity: ast.Error,
	}}
	if got := report(&buf, diags, false); got != 1 {
		t.Errorf("report(error) = %d, want 1; stderr: %q", got, buf.String())
	}
	if !strings.Contains(buf.String(), "error: oops") {
		t.Errorf("report stderr = %q, want 'error: oops'", buf.String())
	}
}

func TestReport_StrictWarningExitsOne(t *testing.T) {
	var buf bytes.Buffer
	diags := []ast.Diagnostic{{
		Code:     "nit",
		Message:  "minor",
		Severity: ast.Warning,
	}}
	if got := report(&buf, diags, true); got != 1 {
		t.Errorf("report(warning, strict) = %d, want 1", got)
	}
	if got := report(&buf, diags, false); got != 0 {
		t.Errorf("report(warning, non-strict) = %d, want 0", got)
	}
}

func TestRun_NoRequests_ExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{"--latest"}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run() = %d, want 2; stderr: %q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no requests") {
		t.Errorf("stderr = %q, want it to mention 'no requests'", stderr.String())
	}
}

func TestRun_DateAndRangeMutex(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		"--source", "EUR=USD:fake/USD",
		"--date", "2026-04-25",
		"--range", "2026-01-01..2026-02-01",
	}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run() = %d, want 2; stderr: %q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Errorf("stderr = %q, want it to mention 'mutually exclusive'", stderr.String())
	}
}

func TestRun_LatestPlusDateMutex(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		"--source", "EUR=USD:fake/USD",
		"--latest",
		"--date", "2026-01-01",
	}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run() = %d, want 2; stderr: %q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Errorf("stderr = %q, want it to mention 'mutually exclusive'", stderr.String())
	}
}

func TestRun_LatestPlusRangeMutex(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		"--source", "EUR=USD:fake/USD",
		"--latest",
		"--range", "2026-01-01..2026-02-01",
	}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run() = %d, want 2; stderr: %q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Errorf("stderr = %q, want it to mention 'mutually exclusive'", stderr.String())
	}
}

func TestRun_BadFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{"--no-such-flag"}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run(--no-such-flag) = %d, want 2", got)
	}
}

func TestRun_HelpExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{"-h"}, &stdout, &stderr)
	if got != 0 {
		t.Errorf("run(-h) = %d, want 0; stderr: %q", got, stderr.String())
	}
	// Help is printed to stderr by the FlagSet; verify it has the
	// design-doc-mandated section headings.
	want := []string{"Usage: beanprice", "PRICE META FORMAT", "--source FLAG FORMAT", "EXIT CODES", "EXAMPLES"}
	for _, s := range want {
		if !strings.Contains(stderr.String(), s) {
			t.Errorf("help missing %q; got:\n%s", s, stderr.String())
		}
	}
}

func TestRun_InlineSource_HappyPath(t *testing.T) {
	registerFake(t)
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		"--source", "EUR=USD:fake/USD",
		"--latest",
	}, &stdout, &stderr)
	if got != 0 {
		t.Errorf("run() = %d, want 0; stderr: %q", got, stderr.String())
	}
	// FormatStream emits one canonical price-directive line per price.
	out := stdout.String()
	if !strings.Contains(out, "price EUR") || !strings.Contains(out, "USD") {
		t.Errorf("stdout = %q, want it to contain a price for EUR in USD", out)
	}
}

func TestRun_LedgerWalk_HappyPath(t *testing.T) {
	registerFake(t)
	path := filepath.Join("testdata", "with-meta.beancount")
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		"--ledger", path,
		"--latest",
	}, &stdout, &stderr)
	if got != 0 {
		t.Errorf("run(ledger) = %d, want 0; stderr: %q\nstdout: %q", got, stderr.String(), stdout.String())
	}
	out := stdout.String()
	// Two priced commodities → two price directives.
	lines := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, " price ") {
			lines++
		}
	}
	if lines != 2 {
		t.Errorf("stdout had %d price lines, want 2; full output:\n%s", lines, out)
	}
	if !strings.Contains(out, "price USD") {
		t.Errorf("stdout missing 'price USD' line: %q", out)
	}
	if !strings.Contains(out, "price AAPL") {
		t.Errorf("stdout missing 'price AAPL' line: %q", out)
	}
}

func TestRun_CommodityFilter(t *testing.T) {
	registerFake(t)
	path := filepath.Join("testdata", "with-meta.beancount")
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		"--ledger", path,
		"--commodity", "AAPL",
		"--latest",
	}, &stdout, &stderr)
	if got != 0 {
		t.Errorf("run(filter) = %d, want 0; stderr: %q", got, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "price AAPL") {
		t.Errorf("stdout missing 'price AAPL': %q", out)
	}
	if strings.Contains(out, "price USD") {
		t.Errorf("stdout unexpectedly contains 'price USD' line: %q", out)
	}
}

func TestRun_CommodityFilter_RequiresLedger(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		"--commodity", "AAPL",
		"--source", "EUR=USD:fake/USD",
		"--latest",
	}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run(commodity-without-ledger) = %d, want 2; stderr: %q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--commodity requires --ledger") {
		t.Errorf("stderr = %q, want it to mention '--commodity requires --ledger'", stderr.String())
	}
}

func TestRun_UnknownSource_Diagnostic(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		"--source", "EUR=USD:nonexistent/X",
		"--latest",
	}, &stdout, &stderr)
	if got == 0 {
		t.Errorf("run(unknown-source) = 0, want non-zero; stderr: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "quote-source-unknown") {
		t.Errorf("stderr = %q, want it to mention 'quote-source-unknown'", stderr.String())
	}
}

func TestRun_BadSourceFlagSyntax(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		"--source", "no-equals-sign",
		"--latest",
	}, &stdout, &stderr)
	// Bad --source produces an Error diagnostic and (since no other
	// requests came in) "no requests" — so exit 2.
	if got != 2 {
		t.Errorf("run(bad-source) = %d, want 2; stderr: %q", got, stderr.String())
	}
}

func TestRun_PluginLoadFailure(t *testing.T) {
	// A path that does not exist is the cheapest goplug.Load failure
	// we can produce without a real .so on disk.
	missing := filepath.Join(t.TempDir(), "missing.so")
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		"--plugin", missing,
		"--source", "EUR=USD:fake/USD",
		"--latest",
	}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run(missing-plugin) = %d, want 2; stderr: %q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "plugin load failed") {
		t.Errorf("stderr = %q, want it to mention 'plugin load failed'", stderr.String())
	}
}

func TestFormatDiagnostic(t *testing.T) {
	d := ast.Diagnostic{
		Span:     ast.Span{Start: ast.Position{Filename: "x.beancount", Line: 5, Column: 2}},
		Code:     "quote-x",
		Message:  "boom",
		Severity: ast.Error,
	}
	want := "x.beancount:5:2: error: boom [quote-x]"
	if got := formatDiagnostic(d); got != want {
		t.Errorf("formatDiagnostic = %q, want %q", got, want)
	}
}

// Ensure the testdata fixture is exercised by the build — guards
// against accidental deletion.
func TestFixtureExists(t *testing.T) {
	path := filepath.Join("testdata", "with-meta.beancount")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture %q: %v", path, err)
	}
}
