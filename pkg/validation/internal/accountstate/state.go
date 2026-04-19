// Package accountstate defines the per-account state tracked during
// ledger validation. The type is intended to be shared across the
// validator's forthcoming internal subpackages (pad, balance, and the
// general validations bundle) so they can cooperate without exposing
// per-account state to the public API.
package accountstate

import (
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

// State tracks the open/close lifecycle of a single account.
type State struct {
	// OpenSpan is the source span of the Open directive that activated the account.
	OpenSpan ast.Span
	// OpenDate is the date on which the account became active.
	OpenDate time.Time
	// Closed reports whether a Close directive has been observed for the account.
	Closed bool
	// CloseDate is the date on which the account was closed (zero when Closed is false).
	CloseDate time.Time
	// CloseSpan is the source span of the Close directive that deactivated the account (empty when Closed is false).
	CloseSpan ast.Span
	// Currencies is the currency allowlist declared on the Open directive; empty means unrestricted.
	Currencies []string
	// Booking is the cost-basis booking method declared on the Open directive.
	Booking ast.BookingMethod
}
