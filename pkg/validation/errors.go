package validation

import (
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Code identifies a kind of validation error.
type Code string

const (
	// CodeAccountNotOpen indicates a directive references an account that has never been opened.
	CodeAccountNotOpen Code = "account-not-open"
	// CodeAccountNotYetOpen indicates a directive references an account on a date before its open directive.
	CodeAccountNotYetOpen Code = "account-not-yet-open"
	// CodeAccountClosed indicates a posting references an account after it has been closed.
	CodeAccountClosed Code = "account-closed"
	// CodeDuplicateOpen indicates an account was opened more than once.
	CodeDuplicateOpen Code = "duplicate-open"
	// CodeUnbalancedTransaction indicates a transaction's postings do not sum to zero.
	CodeUnbalancedTransaction Code = "unbalanced-transaction"
	// CodeMultipleAutoPostings indicates a transaction contains more than one posting
	// whose amount must be inferred.
	CodeMultipleAutoPostings Code = "multiple-auto-postings"
	// CodeBalanceMismatch indicates a balance assertion did not match the computed balance.
	CodeBalanceMismatch Code = "balance-mismatch"
	// CodePadUnresolved indicates a pad directive could not be resolved to a balance assertion.
	CodePadUnresolved Code = "pad-unresolved"
	// CodeCurrencyNotAllowed indicates a posting uses a currency not permitted by the account's open directive.
	CodeCurrencyNotAllowed Code = "currency-not-allowed"
	// CodeCustomAssertionFailed indicates a user-defined custom assertion failed.
	CodeCustomAssertionFailed Code = "custom-assertion-failed"
	// CodeInternalError indicates an internal validation failure such as an
	// arithmetic error from the underlying decimal context. These are not
	// user-facing ledger problems but signal a bug or pathological input.
	CodeInternalError Code = "internal-error"
	// CodeInvalidOption indicates a malformed value for a known option key.
	CodeInvalidOption Code = "invalid-option"
	// CodeInvalidBookingMethod indicates an Open directive's Booking keyword
	// could not be parsed into a known ast.BookingMethod value.
	//
	// Deprecated: ast.Open.Booking is now typed, and invalid keywords are
	// reported as parse diagnostics by the lowerer rather than as
	// validation errors. This constant is retained so existing imports and
	// validation.FromInventoryError keep compiling; the validation
	// package no longer emits it.
	CodeInvalidBookingMethod Code = "invalid-booking-method"
)

// Error is a validation error found in a ledger.
type Error struct {
	Code    Code
	Span    ast.Span
	Message string
}

// Error returns a human-readable description of the validation error, including source location when available.
func (e Error) Error() string {
	pos := e.Span.Start
	if pos.Filename != "" {
		return fmt.Sprintf("%s:%d:%d: %s", pos.Filename, pos.Line, pos.Column, e.Message)
	}
	return e.Message
}
