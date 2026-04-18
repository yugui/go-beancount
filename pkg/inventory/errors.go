package inventory

import (
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation"
)

// Code identifies a kind of inventory error.
type Code int

const (
	// CodeInvalidBookingMethod indicates a booking method keyword that the
	// inventory layer cannot evaluate (e.g. AVERAGE, or an unparseable
	// keyword on an Open directive).
	CodeInvalidBookingMethod Code = iota
	// CodeAmbiguousLotMatch indicates that a reducing posting under STRICT
	// booking matched more than one lot.
	CodeAmbiguousLotMatch
	// CodeNoMatchingLot indicates that a reducing posting matched no
	// existing lot in the account's inventory.
	CodeNoMatchingLot
	// CodeReductionExceedsInventory indicates that a reducing posting
	// requests more units than the matched lots contain.
	CodeReductionExceedsInventory
	// CodeAugmentationRequiresCost indicates that an augmenting posting
	// specified an empty cost spec ("{}") where a concrete cost was
	// required to build a lot.
	CodeAugmentationRequiresCost
	// CodeMultipleAutoPostings indicates that a transaction contains more
	// than one posting whose amount must be inferred.
	CodeMultipleAutoPostings
	// CodeUnresolvableAutoPosting indicates that the residual of a
	// transaction's balanced postings could not be expressed as a single
	// auto-posting amount.
	CodeUnresolvableAutoPosting
	// CodeInvalidAutoPosting indicates that an auto-balanced posting
	// (Amount == nil) carries a cost or price annotation, which the
	// inventory layer rejects as semantically ambiguous.
	CodeInvalidAutoPosting
	// CodeMixedInventory indicates that an inventory ended up holding
	// positions of the same commodity with conflicting sign or lot
	// structure that the booking method cannot reconcile.
	CodeMixedInventory
	// CodeInternalError indicates an internal inventory failure such as
	// an arithmetic error from the underlying decimal context, or a
	// defensive check against invariants that earlier stages should have
	// enforced.
	CodeInternalError
)

// Error is an inventory error produced while booking postings against
// account state.
//
// The shape mirrors validation.Error so the two can be surfaced through a
// common diagnostics channel, plus an Account field that records which
// account's inventory was being mutated when the error was detected.
type Error struct {
	Code    Code
	Span    ast.Span
	Account ast.Account
	Message string
}

// Error returns a human-readable description of the inventory error,
// including source location and account when available. The format
// follows validation.Error for consistency:
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

// AsValidationError converts e into a validation.Error so that a
// future unified diagnostics layer can present inventory and validation
// errors through one channel. Codes without a direct equivalent in the
// validation layer map to validation.CodeInternalError. The span and
// message are preserved; the account name, if any, is folded into the
// message so it survives the lossy conversion.
func (e Error) AsValidationError() validation.Error {
	var vc validation.Code
	switch e.Code {
	case CodeInvalidBookingMethod:
		vc = validation.CodeInvalidBookingMethod
	case CodeMultipleAutoPostings:
		vc = validation.CodeMultipleAutoPostings
	default:
		vc = validation.CodeInternalError
	}
	msg := e.Message
	if e.Account != "" {
		msg = fmt.Sprintf("%s: %s", e.Account, msg)
	}
	return validation.Error{
		Code:    vc,
		Span:    e.Span,
		Message: msg,
	}
}
