package validations

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/internal/tolerance"
)

// transactionBalances enforces per-currency balance for every
// transaction, tolerating at most one auto-computed posting. It
// mirrors upstream beancount's check_balance routine, but without
// maintaining the running per-account balance map (balance assertions
// live in the sibling pkg/validation/balance plugin).
//
// Diagnostics produced:
//   - CodeMultipleAutoPostings when a transaction has more than one
//     auto-posting (Amount == nil).
//   - CodeUnbalancedTransaction when the per-currency residual exceeds the
//     inferred tolerance and no auto-posting absorbs it, or when more than
//     one residual currency remains for a single auto-posting to absorb.
//   - CodeInternalError when tolerance inference itself fails (surfaced
//     as `failed to derive transaction tolerance`).
//
// The validator reads the shared *options.Values so tolerance.Infer can
// honor the ledger's `inferred_tolerance_multiplier` and
// `infer_tolerance_from_cost` settings. Values is a read-only dependency
// from this validator's perspective.
type transactionBalances struct {
	opts *options.Values
}

// newTransactionBalances constructs a transactionBalances validator bound
// to the given options view. opts must be non-nil; the caller is expected
// to obtain it via options.FromRaw (or equivalent) before constructing
// the validator.
func newTransactionBalances(opts *options.Values) *transactionBalances {
	return &transactionBalances{opts: opts}
}

// Name identifies this validator for diagnostic and debugging purposes.
func (*transactionBalances) Name() string { return "transaction_balances" }

// ProcessEntry inspects a single directive. Non-transaction directives
// are ignored. For transactions, it replicates upstream beancount's
// check_balance control flow: count auto-postings, compute per-currency
// weight sums, infer tolerance, and emit at most one diagnostic per
// failure mode.
func (v *transactionBalances) ProcessEntry(d ast.Directive) []ast.Diagnostic {
	txn, ok := d.(*ast.Transaction)
	if !ok {
		return nil
	}

	sums := make(currencySum)
	autoCount := 0

	for i := range txn.Postings {
		p := &txn.Postings[i]
		if p.Amount == nil {
			autoCount++
			continue
		}
		w, cur, err := inventory.PostingWeight(p)
		if err != nil {
			return []ast.Diagnostic{{
				Code:    string(validation.CodeUnbalancedTransaction),
				Span:    txn.Span,
				Message: fmt.Sprintf("failed to compute posting weight: %v", err),
			}}
		}
		if err := sums.add(cur, w); err != nil {
			return []ast.Diagnostic{{
				Code:    string(validation.CodeUnbalancedTransaction),
				Span:    txn.Span,
				Message: fmt.Sprintf("failed to accumulate posting weight: %v", err),
			}}
		}
	}

	if autoCount > 1 {
		return []ast.Diagnostic{{
			Code:    string(validation.CodeMultipleAutoPostings),
			Span:    txn.Span,
			Message: fmt.Sprintf("transaction has %d auto-balanced postings; at most one is allowed", autoCount),
		}}
	}

	nonZero := sums.nonZeroCurrencies()
	tolerances, err := tolerance.Infer(txn.Postings, v.opts, nonZero)
	if err != nil {
		return []ast.Diagnostic{{
			Code:    string(validation.CodeInternalError),
			Span:    txn.Span,
			Message: fmt.Sprintf("failed to derive transaction tolerance: %v", err),
		}}
	}

	residual := make([]string, 0, len(nonZero))
	for _, cur := range nonZero {
		within, err := tolerance.Within(sums[cur], tolerances[cur])
		if err != nil {
			return []ast.Diagnostic{{
				Code:    string(validation.CodeUnbalancedTransaction),
				Span:    txn.Span,
				Message: fmt.Sprintf("failed to evaluate balance tolerance: %v", err),
			}}
		}
		if !within {
			residual = append(residual, cur)
		}
	}

	if autoCount == 1 {
		// The single auto-posting absorbs at most one residual currency.
		// Zero or one residual currencies → balanced; anything more is a
		// multi-currency auto-posting we cannot infer.
		switch len(residual) {
		case 0, 1:
			return nil
		default:
			return []ast.Diagnostic{{
				Code:    string(validation.CodeUnbalancedTransaction),
				Span:    txn.Span,
				Message: fmt.Sprintf("cannot infer auto-posting amount: residual has %d non-zero currencies (%s)", len(residual), strings.Join(residual, ", ")),
			}}
		}
	}

	// No auto-postings: any residual currency means the transaction is
	// unbalanced.
	if len(residual) > 0 {
		return []ast.Diagnostic{{
			Code:    string(validation.CodeUnbalancedTransaction),
			Span:    txn.Span,
			Message: fmt.Sprintf("transaction does not balance: non-zero residual in %s", strings.Join(residual, ", ")),
		}}
	}
	return nil
}

// Finish has no deferred diagnostics: all balance checks are
// per-directive and emit eagerly.
func (*transactionBalances) Finish() []ast.Diagnostic { return nil }

// currencySum accumulates signed per-currency totals. A nil currencySum
// panics on write; always initialise with make(currencySum). It is a
// small helper kept local to this validator to avoid reaching across
// package boundaries for an unexported type.
type currencySum map[string]*apd.Decimal

// add adds n to the running total for the given currency.
func (s currencySum) add(currency string, n *apd.Decimal) error {
	d, ok := s[currency]
	if !ok {
		d = new(apd.Decimal)
		s[currency] = d
	}
	_, err := apd.BaseContext.Add(d, d, n)
	return err
}

// nonZeroCurrencies returns the currencies whose running total is not
// exactly zero, sorted for deterministic reporting.
func (s currencySum) nonZeroCurrencies() []string {
	out := make([]string, 0, len(s))
	for cur, d := range s {
		if !d.IsZero() {
			out = append(out, cur)
		}
	}
	sort.Strings(out)
	return out
}
