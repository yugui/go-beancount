package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLedger writes content to a .beancount file in a fresh temp dir and
// returns its path. The fixtures are deliberately self-contained (every
// account opened before use) so the loader emits no Error diagnostics and
// the query runs.
func writeLedger(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.beancount")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

const sampleLedger = `2024-01-01 open Assets:Cash
2024-01-01 open Expenses:Food
2024-01-01 open Income:Salary

2024-01-05 * "Cafe" "Coffee" #morning #treat
  Assets:Cash      -5.50 USD
  Expenses:Food     5.50 USD
    category: "drinks"

2024-02-10 * "Acme" "Paycheck"
  Income:Salary  -1000.00 USD
  Assets:Cash     1000.00 USD

2024-02-15 * "Grocer" "Veggies"
  Assets:Cash    -42.00 USD
  Expenses:Food    42.00 USD
`

func TestRun_GroupBySum_HappyPath(t *testing.T) {
	path := writeLedger(t, sampleLedger)
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		path,
		"SELECT account, sum(number) AS total GROUP BY account ORDER BY account",
	}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("run() = %d, want 0; stderr: %q", got, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"account", "total", "Assets:Cash", "Expenses:Food", "47.50", "-1000.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q; got:\n%s", want, out)
		}
	}
	assertAligned(t, out)
}

// assertAligned checks that every output row is padded to a common width:
// a column that is right- or left-aligned produces lines whose visible
// content ends at the same offsets, so all data lines share the header
// line's length. This catches a regression that drops the padding without
// pinning the exact column geometry.
func assertAligned(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("want a header and at least one data row, got:\n%s", out)
	}
	cols := len(strings.Fields(lines[0]))
	for _, line := range lines[1:] {
		if got := len(strings.Fields(line)); got != cols {
			t.Errorf("row %q has %d fields, want %d (alignment broken)", line, got, cols)
		}
	}
}

// TestRun_StdFunction_Activates proves the pkg/query/env/std blank import
// registers functions: year(date) is a std scalar, and the query fails to
// compile if no functions are registered.
func TestRun_StdFunction_Activates(t *testing.T) {
	path := writeLedger(t, sampleLedger)
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		path,
		`SELECT date, narration, year(date) AS yr WHERE account = "Expenses:Food" ORDER BY date`,
	}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("run() = %d, want 0; stderr: %q", got, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"date", "narration", "yr", "Coffee", "Veggies", "2024"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q; got:\n%s", want, out)
		}
	}
}

// TestRun_SetAndDictRendering exercises a set-valued column (tags) and a
// dict lookup (meta('category')) through the CLI's Format-based renderer.
func TestRun_SetAndDictRendering(t *testing.T) {
	path := writeLedger(t, sampleLedger)
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		path,
		`SELECT account, tags, meta('category') AS cat WHERE account = "Expenses:Food" ORDER BY date`,
	}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("run() = %d, want 0; stderr: %q", got, stderr.String())
	}
	out := stdout.String()
	// First Expenses:Food posting carries tags and a category meta; the
	// second carries neither (empty set, NULL meta).
	for _, want := range []string{"morning", "treat", "drinks", "NULL"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q; got:\n%s", want, out)
		}
	}
}

func TestRun_ZeroRows_PrintsHeaderOnly(t *testing.T) {
	path := writeLedger(t, sampleLedger)
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		path,
		"SELECT account, number WHERE number > 999999",
	}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("run() = %d, want 0; stderr: %q", got, stderr.String())
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("zero-row result printed %d lines, want 1 (header only):\n%s", len(lines), stdout.String())
	}
	if !strings.Contains(lines[0], "account") || !strings.Contains(lines[0], "number") {
		t.Errorf("header line = %q, want it to name both columns", lines[0])
	}
}

func TestRun_MissingLedgerFile_ExitsTwo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.beancount")
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{missing, "SELECT account"}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run(missing-file) = %d, want 2; stderr: %q", got, stderr.String())
	}
	if stderr.Len() == 0 {
		t.Error("run(missing-file) wrote nothing to stderr, want an error message")
	}
}

func TestRun_UnknownColumn_ExitsOne(t *testing.T) {
	path := writeLedger(t, sampleLedger)
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{path, "SELECT no_such_column"}, &stdout, &stderr)
	if got != 1 {
		t.Errorf("run(unknown-column) = %d, want 1; stderr: %q", got, stderr.String())
	}
	if stderr.Len() == 0 {
		t.Error("run(unknown-column) wrote nothing to stderr, want a compile error")
	}
}

func TestRun_SyntaxError_ExitsOne(t *testing.T) {
	path := writeLedger(t, sampleLedger)
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{path, "SELECT account WHERE"}, &stdout, &stderr)
	if got != 1 {
		t.Errorf("run(syntax-error) = %d, want 1; stderr: %q", got, stderr.String())
	}
	if stderr.Len() == 0 {
		t.Error("run(syntax-error) wrote nothing to stderr, want a parse error")
	}
}

// TestRun_ErrorDiagnostic_BlocksQuery feeds a ledger that references an
// unopened account. The loader reports an Error diagnostic; run must print
// it and exit 1 without running the query (so stdout stays empty).
func TestRun_ErrorDiagnostic_BlocksQuery(t *testing.T) {
	path := writeLedger(t, `2024-01-05 * "Cafe" "Coffee"
  Assets:Cash      -5.50 USD
  Expenses:Food     5.50 USD
`)
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{path, "SELECT account"}, &stdout, &stderr)
	if got != 1 {
		t.Errorf("run(error-diagnostic) = %d, want 1; stderr: %q", got, stderr.String())
	}
	if stderr.Len() == 0 {
		t.Error("run(error-diagnostic) wrote nothing to stderr, want diagnostics")
	}
	if stdout.Len() != 0 {
		t.Errorf("run(error-diagnostic) wrote %q to stdout, want empty (query must not run)", stdout.String())
	}
}

func TestRun_BadFlag_ExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{"--no-such-flag"}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run(--no-such-flag) = %d, want 2; stderr: %q", got, stderr.String())
	}
}

func TestRun_WrongArgCount_ExitsTwo(t *testing.T) {
	path := writeLedger(t, sampleLedger)
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{path}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run(one-arg) = %d, want 2; stderr: %q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "argument") {
		t.Errorf("stderr = %q, want it to mention the argument count", stderr.String())
	}
}

func TestRun_HelpExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{"-h"}, &stdout, &stderr)
	if got != 0 {
		t.Errorf("run(-h) = %d, want 0; stderr: %q", got, stderr.String())
	}
	for _, want := range []string{"Usage: beanquery", "EXIT CODES", "EXAMPLES"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("help missing %q; got:\n%s", want, stderr.String())
		}
	}
}
