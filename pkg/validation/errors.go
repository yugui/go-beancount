package validation

import (
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Code identifies a kind of validation error.
type Code int

const (
	// CodeAccountNotOpen indicates a directive references an account that has never been opened.
	CodeAccountNotOpen Code = iota
	// CodeAccountNotYetOpen indicates a directive references an account on a date before its open directive.
	CodeAccountNotYetOpen
	// CodeAccountClosed indicates a posting references an account after it has been closed.
	CodeAccountClosed
	// CodeDuplicateOpen indicates an account was opened more than once.
	CodeDuplicateOpen
	// CodeUnbalancedTransaction indicates a transaction's postings do not sum to zero.
	CodeUnbalancedTransaction
	// CodeMultipleAutoPostings indicates a transaction contains more than one posting
	// whose amount must be inferred.
	CodeMultipleAutoPostings
	// CodeBalanceMismatch indicates a balance assertion did not match the computed balance.
	CodeBalanceMismatch
	// CodePadUnresolved indicates a pad directive could not be resolved to a balance assertion.
	CodePadUnresolved
	// CodeCurrencyNotAllowed indicates a posting uses a currency not permitted by the account's open directive.
	CodeCurrencyNotAllowed
	// CodeCustomAssertionFailed indicates a user-defined custom assertion failed.
	CodeCustomAssertionFailed
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
