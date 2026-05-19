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
	CodeInvalidBookingMethod = "invalid-booking-method"
	// CodeAmbiguousLotMatch indicates that a reducing posting under STRICT
	// booking matched more than one lot.
	CodeAmbiguousLotMatch = "ambiguous-lot-match"
	// CodeNoMatchingLot indicates that a reducing posting matched no
	// existing lot in the account's inventory.
	CodeNoMatchingLot = "no-matching-lot"
	// CodeReductionExceedsInventory indicates that a reducing posting
	// requests more units than the matched lots contain.
	CodeReductionExceedsInventory = "reduction-exceeds-inventory"
	// CodeAugmentationRequiresCost indicates that an augmenting posting
	// specified an empty cost spec ("{}") where a concrete cost was
	// required to build a lot.
	CodeAugmentationRequiresCost = "augmentation-requires-cost"
	// CodeMultipleAutoPostings indicates that a transaction contains more
	// than one posting whose amount must be inferred.
	CodeMultipleAutoPostings = "multiple-auto-postings"
	// CodeUnresolvableInterpolation indicates that the booking layer was
	// unable to fill in a missing posting unknown — either an auto-
	// posting Amount or a partial cost spec's per-unit number — because
	// the transaction's residual was over- or under-determined. This
	// covers all of: zero residual where a non-zero one was needed, a
	// residual that spans more than one currency for a single-currency
	// unknown, and the case where two or more unknowns share the same
	// transaction.
	CodeUnresolvableInterpolation = "unresolvable-interpolation"
	// CodeInvalidAutoPosting indicates that an auto-balanced posting
	// (Amount == nil) carries a cost or price annotation, which the
	// inventory layer rejects as semantically ambiguous.
	CodeInvalidAutoPosting = "invalid-auto-posting"
	// CodeMixedInventory indicates that an inventory ended up holding
	// positions of the same commodity with conflicting sign or lot
	// structure that the booking method cannot reconcile.
	CodeMixedInventory = "mixed-inventory"
	// CodeZeroUnitsCostTotal indicates an augmenting posting whose cost
	// spec carries a Total but whose Amount is zero, so the per-unit
	// cost (Total / units) is undefined. The user has written something
	// structurally meaningless, e.g.
	// `Assets:Stock 0 ACME {{ 100 USD }}`.
	CodeZeroUnitsCostTotal = "zero-units-cost-total"
	// CodeInternalError marks a booking-pass implementation bug or
	// invariant violation surfaced to the user via the diagnostic
	// channel rather than aborting the whole load.
	//
	// The inventory package itself never constructs a diagnostic with
	// this code. Internal bugs flow up as plain `error` values
	// (typically via [fmt.Errorf]) and the booking adapter in
	// pkg/loader/booking translates them once into a [ast.Diagnostic]
	// with this code so a multi-diagnostic report still reaches the
	// user with the other findings.
	CodeInternalError = "internal-error"
)

// newDiag constructs an inventory-layer [ast.Diagnostic]. When account
// is non-empty it is folded into Message as the canonical
// "<account>: <msg>" prefix that the CLI and downstream tooling
// already expect for account-tagged booking findings; the empty
// account leaves msg untouched.
func newDiag(code Code, span ast.Span, account ast.Account, msg string) ast.Diagnostic {
	if account != "" {
		msg = string(account) + ": " + msg
	}
	return ast.Diagnostic{Code: string(code), Span: span, Message: msg}
}

// enrichDiagnostic fills in the surrounding posting's context on a
// finding that a lower-level helper produced without knowing the
// account or full posting span.
//
//   - When d.Span is the zero value it inherits p.Span.
//   - When p.Account is non-empty it is folded into d.Message as the
//     canonical "<p.Account>: <msg>" prefix.
//
// d is mutated in place; the same pointer is returned for chaining.
// Lower-level helpers ([ResolveCost], [Inventory.Reduce], …) MUST
// construct findings with an empty Account so this prepend does not
// double-stamp.
func enrichDiagnostic(d *ast.Diagnostic, p *ast.Posting) *ast.Diagnostic {
	if d == nil {
		return nil
	}
	if d.Span == (ast.Span{}) {
		d.Span = p.Span
	}
	if p.Account != "" {
		d.Message = string(p.Account) + ": " + d.Message
	}
	return d
}

// wrapSystemErr stamps the surrounding posting's source location on
// err so a developer has a repro anchor for the input that triggered
// the booking-pass bug. The chain is preserved via fmt.Errorf %w so
// [errors.Is] / [errors.As] still see the underlying cause. nil err
// passes through unchanged; an err produced under a span with no
// filename is also returned unwrapped (no location to stamp).
func wrapSystemErr(err error, p *ast.Posting) error {
	if err == nil {
		return nil
	}
	pos := p.Span.Start
	if pos.Filename == "" {
		return err
	}
	return fmt.Errorf("at %s:%d:%d: %w", pos.Filename, pos.Line, pos.Column, err)
}
