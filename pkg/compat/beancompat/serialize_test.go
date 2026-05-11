package beancompat

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/pkg/ast"
)

// cmpJSONRawMessage normalizes json.RawMessage values to their semantic Go
// representation before comparison, so cmp.Diff over Result (whose Meta,
// Data, and Options fields are json.RawMessage) ignores byte-level
// differences such as object-key order or whitespace while still catching
// genuine value-level divergence. The transformer unmarshal step tolerates
// nil/empty raw messages by surfacing them as nil any so JSON null and an
// absent value compare equal at the semantic level.
var cmpJSONRawMessage = cmp.Transformer("normalizeJSON", func(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		// Surface the raw bytes as a string so a malformed payload
		// shows up as a diff instead of being silently swallowed.
		return string(b)
	}
	return v
})

// mustDate parses s as a YYYY-MM-DD calendar date, failing the test on a
// malformed input. Tests use it inline in AST literals where time.Parse's
// error return would just clutter the table. The returned time.Time is
// UTC-anchored (time.Parse with a zone-less layout defaults to UTC), which
// callers comparing against directive Date fields should keep in mind to
// avoid subtle timezone-mismatch diffs.
func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("mustDate(%q): %v", s, err)
	}
	return d
}

// ledgerOf builds a *ast.Ledger from an explicit directive slice using the
// only public construction path the AST package exposes (an empty Ledger
// plus Insert per directive). Inserting directives in input order is
// sufficient for serializer tests because Ledger's canonical ordering is
// stable and deterministic; tests that care about envelope-level ordering
// pick dates/kinds that don't reorder.
func ledgerOf(t *testing.T, directives ...ast.Directive) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	for _, d := range directives {
		l.Insert(d)
	}
	return l
}

// assertSerializeMatches drives SerializeParsed over ledger and compares
// the result against wantJSON (a literal Result JSON payload) using
// cmpJSONRawMessage so semantically equivalent JSON shapes compare equal.
// On failure it dumps the canonical pretty-printed form of the actual
// result alongside the diff so the author can copy-paste the corrected
// expectation into the test source instead of hand-editing JSON to match
// a textual diff.
func assertSerializeMatches(t *testing.T, ledger *ast.Ledger, wantJSON string) {
	t.Helper()
	got, err := SerializeParsed(ledger)
	if err != nil {
		t.Fatalf("SerializeParsed: %v", err)
	}
	var want Result
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if diff := cmp.Diff(want, got, cmpJSONRawMessage); diff != "" {
		t.Errorf("SerializeParsed mismatch (-want +got):\n%s", diff)
		if b, err := json.MarshalIndent(got, "", "  "); err == nil {
			t.Logf("got (canonical JSON):\n%s", b)
		}
	}
}

// TestSerializeInfra_EmptyLedger is a smoke test that exercises ledgerOf,
// SerializeParsed, and assertSerializeMatches end-to-end on the trivial
// (empty) ledger so subsequent steps that add per-directive coverage start
// from a verified harness.
func TestSerializeInfra_EmptyLedger(t *testing.T) {
	assertSerializeMatches(t, ledgerOf(t), `{"errors": [], "directives": []}`)
}

// TestCmpJSONRawMessageNormalizesKeyOrder pins down the central guarantee
// of cmpJSONRawMessage: two json.RawMessage values that are byte-different
// but semantically equivalent (here, JSON objects with the same keys in a
// different order) must compare equal under cmp.Diff. Without this, the
// transformer could silently regress to a no-op and downstream tests would
// still pass on byte-identical fixtures, masking the breakage.
func TestCmpJSONRawMessageNormalizesKeyOrder(t *testing.T) {
	a := Result{Options: json.RawMessage(`{"a":1,"b":2}`)}
	b := Result{Options: json.RawMessage(`{"b": 2, "a": 1}`)}
	if diff := cmp.Diff(a, b, cmpJSONRawMessage); diff != "" {
		t.Errorf("cmpJSONRawMessage failed to normalize key order:\n%s", diff)
	}
}

// TestMustDate covers the happy path of mustDate: a well-formed
// YYYY-MM-DD string parses into the expected calendar fields. The error
// path is exercised implicitly by every other test that calls mustDate
// with a literal date — a malformed literal would be caught at first run.
func TestMustDate(t *testing.T) {
	got := mustDate(t, "2024-01-02")
	if y, m, d := got.Date(); y != 2024 || m != time.January || d != 2 {
		t.Errorf("mustDate fields = (%d, %s, %d), want (2024, January, 2)", y, m, d)
	}
}
