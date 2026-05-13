package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/yugui/go-beancount/pkg/compat/beancompat"
	"github.com/yugui/go-beancount/pkg/loader"
)

func writeTempBeancount(t *testing.T, src string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.beancount")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(src); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	return f.Name()
}

func TestRun_NoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(nil, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run(nil) = %d, want 2", got)
	}
	if stderr.Len() == 0 {
		t.Error("stderr empty, want usage message")
	}
}

func TestRun_TooManyArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run([]string{"a.beancount", "b.beancount"}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run(two args) = %d, want 2", got)
	}
}

func TestRun_MissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run([]string{filepath.Join(t.TempDir(), "no-such.beancount")}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("run(missing file) = %d, want 2", got)
	}
}

const openSrc = `2020-01-01 open Assets:Cash USD
2020-01-01 open Expenses:Food USD
`

func TestRun_SuccessExitsZero(t *testing.T) {
	path := writeTempBeancount(t, openSrc)
	var stdout, stderr bytes.Buffer
	got := run([]string{path}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("run(valid file) = %d, want 0; stderr=%q", got, stderr.String())
	}
	var result beancompat.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%q", err, stdout.String())
	}
}

// TestRun_JSONMatchesSerializeChecked checks structural equality between
// run's stdout and direct SerializeChecked output on the same source.
// Both paths call loader.Load on the same string content, so the Results
// should be identical.
func TestRun_JSONMatchesSerializeChecked(t *testing.T) {
	path := writeTempBeancount(t, openSrc)

	var stdout, stderr bytes.Buffer
	if code := run([]string{path}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit %d, stderr=%q", code, stderr.String())
	}

	var got beancompat.Result
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal run output: %v\nstdout=%q", err, stdout.String())
	}

	// Produce the reference result directly via the library path using
	// loader.Load(ctx, string(bytes)), which mirrors run's internal load call.
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp file: %v", err)
	}
	ledger, err := loader.Load(t.Context(), string(src))
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	want, err := beancompat.SerializeChecked(ledger)
	if err != nil {
		t.Fatalf("SerializeChecked: %v", err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("run output differs from SerializeChecked (-want +got):\n%s", diff)
	}
}

// TestRun_NoHTMLEscape verifies that SetEscapeHTML(false) is in effect:
// characters like '<' in a narration must reach the JSON output verbatim
// rather than being rewritten as the < JSON Unicode escape sequence.
// This is load-bearing for downstream Python adapters that compare
// narration strings literally.
func TestRun_NoHTMLEscape(t *testing.T) {
	src := "2020-06-15 * \"a<b\"\n  Assets:Cash  10 USD\n  Expenses:Food -10 USD\n"
	path := writeTempBeancount(t, src)
	var stdout, stderr bytes.Buffer
	if code := run([]string{path}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit %d, stderr=%q", code, stderr.String())
	}
	// Byte-level check: the literal '<' character (0x3C) must appear in
	// the output, not the JSON Unicode escape sequence <. Structural
	// unmarshaling is escape-insensitive (both decode to the same rune),
	// so only a raw bytes assertion catches a regression here.
	want := []byte("\"a<b\"") // literal less-than in the JSON string
	if !bytes.Contains(stdout.Bytes(), want) {
		t.Errorf("stdout does not contain literal '<'; got:\n%s", stdout.String())
	}
	escape := []byte("\\u003c") // 6-byte JSON Unicode escape for '<'
	if bytes.Contains(stdout.Bytes(), escape) {
		t.Errorf("stdout contains \\u003c escape; SetEscapeHTML(false) is not effective:\n%s", stdout.String())
	}
}

func TestRun_DirectivesPresent(t *testing.T) {
	path := writeTempBeancount(t, openSrc)
	var stdout, stderr bytes.Buffer
	if code := run([]string{path}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit %d, stderr=%q", code, stderr.String())
	}
	var got beancompat.Result
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := beancompat.Result{
		Errors: []string{},
		Directives: []beancompat.Directive{
			{Type: "open", Date: "2020-01-01"},
			{Type: "open", Date: "2020-01-01"},
		},
	}
	// Meta and Data are verified by TestRun_JSONMatchesSerializeChecked;
	// this test's stated purpose is "directives survive serialization with
	// correct type/date and count", so ignore those fields here.
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(beancompat.Directive{}, "Meta", "Data")); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}
}

func TestRun_LedgerErrorsInJSON(t *testing.T) {
	// beanparse exits 0 even when the ledger has diagnostics: errors go
	// into the JSON "errors" array, never cause a nonzero exit.
	src := "not valid beancount syntax @@@\n"
	path := writeTempBeancount(t, src)
	var stdout, stderr bytes.Buffer
	got := run([]string{path}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("run(ledger with error) = %d, want 0 (errors go in JSON); stderr=%q", got, stderr.String())
	}
	var result beancompat.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%q", err, stdout.String())
	}
	// The "errors" key must be present and non-empty for invalid input.
	if len(result.Errors) == 0 {
		t.Errorf("result.Errors = %v, want non-empty", result.Errors)
	}
	// The serializer initializes Directives as a non-nil slice; a nil slice
	// would produce JSON null instead of [] and break downstream JSON-to-ParseResult
	// conversion in the Python adapter.
	if result.Directives == nil {
		t.Error("result.Directives is nil, want non-nil slice (serialized as [])")
	}
}
