package beancompat

import "fmt"

// DiagKind classifies a single containment mismatch surfaced by Match.
//
// Each Kind exists so that a human reading a failure can distinguish among
// qualitatively different defects without parsing the message string —
// e.g. a value mismatch (real divergence) is different from a precision
// mismatch (often a serializer-formatting issue), and both differ from a
// length mismatch (a structural omission).
type DiagKind int

const (
	// DiagMissingKey indicates an object key required by expected is absent in actual.
	DiagMissingKey DiagKind = iota
	// DiagValueMismatch indicates a primitive (non-decimal) value differs.
	DiagValueMismatch
	// DiagDecimalValueMismatch indicates a numeric value differs (apd.Cmp returned non-zero).
	DiagDecimalValueMismatch
	// DiagDecimalPrecisionMismatch indicates two decimal values are numerically equal but their stored precision (apd.Decimal.Exponent) differs.
	DiagDecimalPrecisionMismatch
	// DiagLengthMismatch indicates an array length differs.
	DiagLengthMismatch
	// DiagTypeMismatch indicates expected and actual disagree on shape (object vs scalar vs array).
	DiagTypeMismatch
	// DiagMissingError indicates an error string declared in expected is absent in actual.
	DiagMissingError
)

// Diagnostic carries enough context for a human to reproduce a Match
// failure without re-running the test. Path is a JSON-pointer-style locator
// (e.g. "directives[3].data.postings[1].cost.number"), and Got/Want hold
// the offending values verbatim.
type Diagnostic struct {
	Path string
	Kind DiagKind
	Got  any
	Want any
	Msg  string
}

// String returns the pre-formatted human-readable summary of the
// diagnostic. Msg is set at construction time so callers see a stable
// rendering even if Want/Got are re-walked later.
func (d Diagnostic) String() string { return d.Msg }

// Match returns nil iff actual contains expected per beancompat's
// containment rules. On mismatch it returns one Diagnostic per discovered
// violation; it does not short-circuit. Step 3 of the integration plan
// replaces this stub with the real implementation; the allowlist is empty
// until then, so the matcher is unreachable from fixture tests in Step 2.
func Match(expected, actual Result) []Diagnostic {
	return nil
}

// formatFailure renders the bundle of information a test driver should
// dump when Match returns diagnostics. Step 3 expands this into the full
// "diagnostic list + pretty JSON + cmp.Diff" report described in the plan;
// the Step 2 version exists so runFixtures can be wired up against a
// stable signature even though the path is unreachable today.
func formatFailure(expected, actual Result, diags []Diagnostic) string {
	return fmt.Sprintf("containment failure: %d diagnostic(s)", len(diags))
}
