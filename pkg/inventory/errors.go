package inventory

import (
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Code identifies a kind of inventory diagnostic. Values match the
// string surfaced on [ast.Diagnostic.Code] so the inventory layer's
// vocabulary is greppable in CLI output without an intermediate
// mapping.
type Code string

const (
	// CodeInvalidBookingMethod indicates a booking method keyword that the
	// inventory layer cannot evaluate (e.g. AVERAGE, or an unparseable
	// keyword on an Open directive).
	CodeInvalidBookingMethod Code = "invalid-booking-method"
	// CodeAmbiguousLotMatch indicates that a reducing posting under STRICT
	// booking matched more than one lot.
	CodeAmbiguousLotMatch Code = "ambiguous-lot-match"
	// CodeNoMatchingLot indicates that a reducing posting matched no
	// existing lot in the account's inventory.
	CodeNoMatchingLot Code = "no-matching-lot"
	// CodeReductionExceedsInventory indicates that a reducing posting
	// requests more units than the matched lots contain.
	CodeReductionExceedsInventory Code = "reduction-exceeds-inventory"
	// CodeAugmentationRequiresCost indicates that an augmenting posting
	// specified an empty cost spec ("{}") where a concrete cost was
	// required to build a lot.
	CodeAugmentationRequiresCost Code = "augmentation-requires-cost"
	// CodeMultipleAutoPostings indicates that a transaction contains more
	// than one posting whose amount must be inferred.
	CodeMultipleAutoPostings Code = "multiple-auto-postings"
	// CodeUnresolvableInterpolation indicates that the booking layer was
	// unable to fill in a missing posting unknown — either an auto-
	// posting Amount or a partial cost spec's per-unit number — because
	// the transaction's residual was over- or under-determined. This
	// covers all of: zero residual where a non-zero one was needed, a
	// residual that spans more than one currency for a single-currency
	// unknown, and the case where two or more unknowns share the same
	// transaction.
	CodeUnresolvableInterpolation Code = "unresolvable-interpolation"
	// CodeInvalidAutoPosting indicates that an auto-balanced posting
	// (Amount == nil) carries a cost or price annotation, which the
	// inventory layer rejects as semantically ambiguous.
	CodeInvalidAutoPosting Code = "invalid-auto-posting"
	// CodeMixedInventory indicates that an inventory ended up holding
	// positions of the same commodity with conflicting sign or lot
	// structure that the booking method cannot reconcile.
	CodeMixedInventory Code = "mixed-inventory"
	// CodeInternalError indicates an internal inventory failure such as
	// an arithmetic error from the underlying decimal context, or a
	// defensive check against invariants that earlier stages should have
	// enforced.
	CodeInternalError Code = "internal-error"
)

// Error is an inventory error produced while booking postings against
// account state.
//
// Code/Span/Message carry the same role they do on [ast.Diagnostic]; the
// Account field records which account's inventory was being mutated
// when the error was detected so the surrounding posting context is not
// lost before the diagnostic reaches the user.
type Error struct {
	Code    Code
	Span    ast.Span
	Account ast.Account
	Message string
}

// Error returns a human-readable description of the inventory error,
// including source location and account when available:
//
//	file:line:col: account: message
//	file:line:col: message
//	account: message
//	message
//
// Whichever prefixes are present are emitted; the message is always last.
func (e Error) Error() string {
	pos := e.Span.Start
	hasLoc := pos.Filename != ""
	hasAcct := e.Account != ""
	switch {
	case hasLoc && hasAcct:
		return fmt.Sprintf("%s:%d:%d: %s: %s", pos.Filename, pos.Line, pos.Column, e.Account, e.Message)
	case hasLoc:
		return fmt.Sprintf("%s:%d:%d: %s", pos.Filename, pos.Line, pos.Column, e.Message)
	case hasAcct:
		return fmt.Sprintf("%s: %s", e.Account, e.Message)
	default:
		return e.Message
	}
}
