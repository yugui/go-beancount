package scope

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

// TestClearBoundaryTimeNowBranch tests clearBoundary directly to cover the
// time.Now() fallback: exercising it via the exported API would require a
// zero-date Transaction (unreachable in a loader-validated ledger), so direct
// testing is the lower-cost path (CLAUDE.md unexported-helper exception).
func TestClearBoundaryTimeNowBranch(t *testing.T) {
	before := time.Now().UTC()
	boundary := clearBoundary(Spec{}, nil)
	after := time.Now().UTC()

	// Result must be a UTC midnight truncation of time.Now() at call time.
	y, mo, d := boundary.Date()
	if boundary != time.Date(y, mo, d, 0, 0, 0, 0, time.UTC) {
		t.Errorf("boundary = %v, want UTC midnight", boundary)
	}

	// Must fall within the test's time range (same day or the day after if
	// the test crosses midnight — both are acceptable).
	beforeDay := time.Date(before.Year(), before.Month(), before.Day(), 0, 0, 0, 0, time.UTC)
	afterDay := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, time.UTC)
	if boundary.Before(beforeDay) || boundary.After(afterDay) {
		t.Errorf("boundary %v outside expected range [%v, %v]", boundary, beforeDay, afterDay)
	}
}

// TestClearBoundaryClosePath verifies Close − 1 day takes priority over kept.
func TestClearBoundaryClosePath(t *testing.T) {
	closeDate := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	s := Spec{Close: closeDate}
	kept := []ast.Directive{
		&ast.Transaction{Date: time.Date(2021, 6, 15, 0, 0, 0, 0, time.UTC)},
	}
	want := time.Date(2021, 12, 31, 0, 0, 0, 0, time.UTC)
	if got := clearBoundary(s, kept); !got.Equal(want) {
		t.Errorf("clearBoundary(close) = %v, want %v", got, want)
	}
}

// TestClearBoundaryLastDatePath verifies the last-entry fallback.
func TestClearBoundaryLastDatePath(t *testing.T) {
	s := Spec{}
	kept := []ast.Directive{
		&ast.Transaction{Date: time.Date(2021, 3, 1, 0, 0, 0, 0, time.UTC)},
		&ast.Transaction{Date: time.Date(2022, 6, 15, 0, 0, 0, 0, time.UTC)},
	}
	want := time.Date(2022, 6, 15, 0, 0, 0, 0, time.UTC)
	if got := clearBoundary(s, kept); !got.Equal(want) {
		t.Errorf("clearBoundary(last-date) = %v, want %v", got, want)
	}
}
