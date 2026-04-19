// Package balance implements the balance-assertion check as an
// api.Plugin. It mirrors upstream beancount's ops/balance.py: walk
// directives in canonical order, maintain a per-(account, currency)
// running balance from every *ast.Transaction's postings, and verify
// each *ast.Balance directive against the running balance at its
// date. Account-open enforcement is intentionally left to the
// validations plugin; this plugin owns only balance-mismatch
// diagnostics.
//
// The running balance is accumulated in the posting's NATIVE currency
// (p.Amount.Currency), not in the weight currency returned by
// inventory.PostingWeight. This matches upstream beancount: balance
// assertions verify the units held in an account, not the converted
// weight. For a plain posting the two are identical; for a posting
// with a price (@, @@) or cost ({}, {{}}) the weight currency differs
// from the units currency, and balance assertions always check units.
package balance

import (
	"context"
	"fmt"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/postproc"
	"github.com/yugui/go-beancount/pkg/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/internal/tolerance"
)

// Plugin runs the balance-assertion check as a postproc plugin. It
// does not mutate the ledger; the function returns Result.Directives
// == nil so the runner preserves the input verbatim.
var Plugin api.PluginFunc = func(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}

	opts, errs := parseOptions(in.Options)
	balances := map[balanceKey]*apd.Decimal{}

	if in.Directives == nil {
		return api.Result{Errors: errs}, nil
	}

	for _, d := range in.Directives {
		switch x := d.(type) {
		case *ast.Transaction:
			errs = append(errs, applyTransaction(x, balances)...)
		case *ast.Balance:
			errs = append(errs, checkBalance(x, balances, opts)...)
		}
	}

	return api.Result{Errors: errs}, nil
}

// init registers Plugin under its canonical package-path name so that
// beancount `plugin "..."` directives can activate it.
func init() {
	postproc.Register("github.com/yugui/go-beancount/pkg/validation/balance", Plugin)
}

// balanceKey identifies a running balance bucket by (account, currency).
// It is kept local to this plugin so the running-balance map does not
// need to reach across package boundaries for an unexported helper.
type balanceKey struct {
	Account  ast.Account
	Currency string
}

// parseOptions wraps options.FromRaw and converts any parse failures
// into api.Error values with code "invalid-option". On error the
// returned *options.Values is still safe to use: FromRaw retains
// defaults for keys that failed to parse.
func parseOptions(raw map[string]string) (*options.Values, []api.Error) {
	opts, optErrs := options.FromRaw(raw)
	var errs []api.Error
	for _, perr := range optErrs {
		errs = append(errs, api.Error{
			Code:    "invalid-option",
			Span:    perr.Span,
			Message: fmt.Sprintf("invalid option %q: %v", perr.Key, perr.Err),
		})
	}
	return opts, errs
}

// applyTransaction accumulates tx's postings into balances and
// handles auto-posting inference. It mirrors upstream beancount's
// two-pass posting weight application: a subsequent balance
// assertion against an auto-posting's account sees the inferred
// amount rather than zero.
func applyTransaction(tx *ast.Transaction, balances map[balanceKey]*apd.Decimal) []api.Error {
	var errs []api.Error

	// First pass: accumulate explicit postings into the running
	// balance (in the posting's NATIVE currency) and compute a
	// per-transaction residual per currency.
	txResidual, autoCount, autoPosting, accErrs := accumulatePostings(tx, balances)
	errs = append(errs, accErrs...)

	// Second pass: if exactly one auto-posting is present AND
	// exactly one currency has a non-zero residual, infer the
	// auto-posting's amount as the negation of that residual and
	// add it to balances[(autoAccount, cur)]. Otherwise do
	// nothing: malformed-transaction diagnostics (multiple
	// auto-postings, multi-currency residual) are owned by the
	// validations plugin, and the balance plugin must stay silent.
	if autoCount == 1 && autoPosting != nil {
		errs = append(errs, inferAutoPosting(tx, autoPosting, txResidual, balances)...)
	}
	return errs
}

// accumulatePostings walks tx.Postings, adding explicit posting
// amounts into balances (native currency) and tracking per-currency
// transaction residuals. It returns the residual map along with the
// count of auto-postings observed and a pointer to the (last) auto
// posting so the caller can attempt inference. When autoCount is
// greater than 1, the returned pointer refers to the last auto-posting
// encountered and should not be used for inference.
func accumulatePostings(tx *ast.Transaction, balances map[balanceKey]*apd.Decimal) (map[string]*apd.Decimal, int, *ast.Posting, []api.Error) {
	var errs []api.Error
	txResidual := map[string]*apd.Decimal{}
	autoCount := 0
	var autoPosting *ast.Posting
	for i := range tx.Postings {
		p := &tx.Postings[i]
		if p.Amount == nil {
			autoCount++
			autoPosting = p
			continue
		}
		num := p.Amount.Number
		key := balanceKey{Account: p.Account, Currency: p.Amount.Currency}
		cur, ok := balances[key]
		if !ok {
			cur = new(apd.Decimal)
			balances[key] = cur
		}
		if _, err := apd.BaseContext.Add(cur, cur, &num); err != nil {
			errs = append(errs, api.Error{
				Code:    string(validation.CodeInternalError),
				Span:    tx.Span,
				Message: fmt.Sprintf("failed to update running balance: %v", err),
			})
			// Continue processing remaining postings; a
			// single arithmetic failure should not silently
			// poison every later directive with stale state,
			// but we also cannot meaningfully recover the
			// bucket. Apd failures are pathological (huge
			// exponents); emitting once per failure suffices.
		}
		resid, ok := txResidual[p.Amount.Currency]
		if !ok {
			resid = new(apd.Decimal)
			txResidual[p.Amount.Currency] = resid
		}
		if _, err := apd.BaseContext.Add(resid, resid, &num); err != nil {
			errs = append(errs, api.Error{
				Code:    string(validation.CodeInternalError),
				Span:    tx.Span,
				Message: fmt.Sprintf("failed to compute transaction residual: %v", err),
			})
		}
	}
	return txResidual, autoCount, autoPosting, errs
}

// inferAutoPosting applies the single-auto-posting inference rule:
// if exactly one currency has a non-zero residual, add its negation
// to the running balance under the auto-posting's account. Any
// other shape is left untouched (diagnosed elsewhere).
func inferAutoPosting(tx *ast.Transaction, autoPosting *ast.Posting, txResidual map[string]*apd.Decimal, balances map[balanceKey]*apd.Decimal) []api.Error {
	var errs []api.Error
	var inferCur string
	nonZeroCount := 0
	for cur, resid := range txResidual {
		if !resid.IsZero() {
			nonZeroCount++
			inferCur = cur
		}
	}
	if nonZeroCount != 1 {
		return nil
	}
	inferred := new(apd.Decimal)
	if _, err := apd.BaseContext.Neg(inferred, txResidual[inferCur]); err != nil {
		errs = append(errs, api.Error{
			Code:    string(validation.CodeInternalError),
			Span:    tx.Span,
			Message: fmt.Sprintf("failed to infer auto-posting amount: %v", err),
		})
		return errs
	}
	key := balanceKey{Account: autoPosting.Account, Currency: inferCur}
	cur, ok := balances[key]
	if !ok {
		cur = new(apd.Decimal)
		balances[key] = cur
	}
	if _, err := apd.BaseContext.Add(cur, cur, inferred); err != nil {
		errs = append(errs, api.Error{
			Code:    string(validation.CodeInternalError),
			Span:    tx.Span,
			Message: fmt.Sprintf("failed to update running balance: %v", err),
		})
	}
	return errs
}

// checkBalance verifies a balance assertion against the running
// balance for (account, currency). The diff is computed as
// expected - actual (mirroring upstream beancount). The tolerance
// check operates on |diff|, so the sign of diff only affects error
// message formatting.
func checkBalance(b *ast.Balance, balances map[balanceKey]*apd.Decimal, opts *options.Values) []api.Error {
	var errs []api.Error
	key := balanceKey{Account: b.Account, Currency: b.Amount.Currency}
	actual := balances[key]
	if actual == nil {
		actual = new(apd.Decimal)
	}

	expCopy := b.Amount.Number
	expected := &expCopy

	diff := new(apd.Decimal)
	if _, err := apd.BaseContext.Sub(diff, expected, actual); err != nil {
		errs = append(errs, api.Error{
			Code:    string(validation.CodeBalanceMismatch),
			Span:    b.Span,
			Message: fmt.Sprintf("failed to compute balance difference: %v", err),
		})
		return errs
	}

	var tol *apd.Decimal
	if b.Tolerance != nil {
		tol = new(apd.Decimal)
		if _, err := apd.BaseContext.Abs(tol, b.Tolerance); err != nil {
			errs = append(errs, api.Error{
				Code:    string(validation.CodeBalanceMismatch),
				Span:    b.Span,
				Message: fmt.Sprintf("failed to normalize tolerance: %v", err),
			})
			return errs
		}
	} else {
		tol = tolerance.ForAmount(opts, b.Amount)
	}

	ok, err := withinTolerance(diff, tol)
	if err != nil {
		errs = append(errs, api.Error{
			Code:    string(validation.CodeBalanceMismatch),
			Span:    b.Span,
			Message: fmt.Sprintf("failed to evaluate balance tolerance: %v", err),
		})
		return errs
	}
	if !ok {
		errs = append(errs, api.Error{
			Code: string(validation.CodeBalanceMismatch),
			Span: b.Span,
			Message: fmt.Sprintf(
				"balance assertion failed: account %s: expected %s %s, got %s %s",
				b.Account,
				expected.Text('f'),
				b.Amount.Currency,
				actual.Text('f'),
				b.Amount.Currency,
			),
		})
	}
	return errs
}

// withinTolerance reports whether |diff| <= tolerance.
func withinTolerance(diff, tol *apd.Decimal) (bool, error) {
	abs := new(apd.Decimal)
	if _, err := apd.BaseContext.Abs(abs, diff); err != nil {
		return false, err
	}
	return abs.Cmp(tol) <= 0, nil
}
