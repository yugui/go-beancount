package ast

import (
	"testing"
)

// TestSeverityZeroValueIsError pins the invariant that a freshly
// constructed Diagnostic literal omitting Severity defaults to Error.
// Every Diagnostic emitter in the codebase relies on this; if a future
// edit ever makes Error not the iota-0 constant, this test fails loudly
// instead of silently flipping every diagnostic's severity.
func TestSeverityZeroValueIsError(t *testing.T) {
	var s Severity
	if s != Error {
		t.Errorf("Severity zero value = %d, want %d (Error)", s, Error)
	}
	if got := (Diagnostic{}).Severity; got != Error {
		t.Errorf("Diagnostic{}.Severity = %d, want %d (Error)", got, Error)
	}
}
