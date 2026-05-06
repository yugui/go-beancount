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
//
// A balance assertion on a parent account aggregates over the entire
// subtree rooted at that account, matching upstream's
// realization.compute_balance(real_account, leaf_only=False). The
// running balance is still keyed per (posting-account, currency); a
// posting only ever updates the bucket whose account it names, even
// when that account is itself a parent. The subtree sum is computed
// on demand at assertion time by scanning buckets with
// [ast.Account.Covers].
//
// Importing this package has the side effect of registering Apply in
// pkg/ext/postproc under the package's import path, so beancount
// `plugin "github.com/yugui/go-beancount/pkg/validation/balance"`
// directives can activate it.
package balance

import (
	"context"
	"fmt"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/internal/tolerance"
)

// Apply runs the balance-assertion check as a postproc plugin. It
// does not mutate the ledger; the function returns a Result with a nil
// Directives field so the runner preserves the input verbatim.
//
// Expects booked AST: postings without an Amount yield a
// CodeAutoPostingUnresolved diagnostic instead of being inferred.
func Apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}

	opts, diags := parseOptions(in.Options)
	balances := map[balanceKey]*apd.Decimal{}

	if in.Directives == nil {
		return api.Result{Diagnostics: diags}, nil
	}

	for _, d := range in.Directives {
		switch x := d.(type) {
		case *ast.Transaction:
			diags = append(diags, applyTransaction(x, balances)...)
		case *ast.Balance:
			diags = append(diags, checkBalance(x, balances, opts)...)
		}
	}

	return api.Result{Diagnostics: diags}, nil
}

// init registers Apply in the global registry so that, once this
// package is imported, a beancount `plugin "..."` directive can
// activate it by name.
func init() {
	postproc.Register("github.com/yugui/go-beancount/pkg/validation/balance", api.PluginFunc(Apply))
}

// balanceKey identifies a running balance bucket by (account, currency).
// It is kept local to this plugin so the running-balance map does not
// need to reach across package boundaries for an unexported helper.
type balanceKey struct {
	Account  ast.Account
	Currency string
}

// parseOptions wraps options.FromRaw and converts any parse failures
// into ast.Diagnostic values with code [validation.CodeInvalidOption].
// On error the returned *options.Values is still safe to use: FromRaw
// retains defaults for keys that failed to parse.
func parseOptions(raw map[string]string) (*options.Values, []ast.Diagnostic) {
	opts, optErrs := options.FromRaw(raw)
	var diags []ast.Diagnostic
	for _, perr := range optErrs {
		diags = append(diags, ast.Diagnostic{
			Code:    string(validation.CodeInvalidOption),
			Span:    perr.Span,
			Message: fmt.Sprintf("invalid option %q: %v", perr.Key, perr.Err),
		})
	}
	return opts, diags
}

// applyTransaction accumulates tx's postings into balances. The
// running balance is updated only from postings that carry an
// explicit Amount; postings without one are reported as
// CodeAutoPostingUnresolved and skipped, since auto-posting
// resolution is owned by the booking pass that runs before
// validation. Cross-posting structural diagnostics (unbalanced
// residual, multiple auto-postings) belong to the validations
// plugin and remain out of scope here.
func applyTransaction(tx *ast.Transaction, balances map[balanceKey]*apd.Decimal) []ast.Diagnostic {
	var diags []ast.Diagnostic
	for i := range tx.Postings {
		p := &tx.Postings[i]
		if p.Amount == nil {
			span := p.Span
			if span == (ast.Span{}) {
				span = tx.Span
			}
			diags = append(diags, ast.Diagnostic{
				Code:    string(validation.CodeAutoPostingUnresolved),
				Span:    span,
				Message: fmt.Sprintf("posting on account %q has no amount; booking pass should have resolved it", p.Account),
			})
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
			diags = append(diags, ast.Diagnostic{
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
	}
	return diags
}

// checkBalance verifies a balance assertion. The actual value is the
// sum of every (account, currency) bucket whose currency matches the
// assertion and whose account is covered by b.Account — i.e. the
// asserted account itself together with its entire subtree, mirroring
// upstream's realization.compute_balance with leaf_only=False. The
// diff is computed as expected - actual; the tolerance check operates
// on |diff|, so the sign of diff only affects error message
// formatting.
func checkBalance(b *ast.Balance, balances map[balanceKey]*apd.Decimal, opts *options.Values) []ast.Diagnostic {
	var diags []ast.Diagnostic
	actual := new(apd.Decimal)
	for k, v := range balances {
		if k.Currency != b.Amount.Currency {
			continue
		}
		if !b.Account.Covers(k.Account) {
			continue
		}
		if _, err := apd.BaseContext.Add(actual, actual, v); err != nil {
			diags = append(diags, ast.Diagnostic{
				Code: string(validation.CodeInternalError),
				Span: b.Span,
				Message: fmt.Sprintf(
					"failed to aggregate subtree balance for %s %s: %v",
					b.Account, b.Amount.Currency, err,
				),
			})
			return diags
		}
	}

	expCopy := b.Amount.Number
	expected := &expCopy

	diff := new(apd.Decimal)
	if _, err := apd.BaseContext.Sub(diff, expected, actual); err != nil {
		diags = append(diags, ast.Diagnostic{
			Code:    string(validation.CodeBalanceMismatch),
			Span:    b.Span,
			Message: fmt.Sprintf("failed to compute balance difference: %v", err),
		})
		return diags
	}

	var tol *apd.Decimal
	if b.Tolerance != nil {
		tol = new(apd.Decimal)
		if _, err := apd.BaseContext.Abs(tol, b.Tolerance); err != nil {
			diags = append(diags, ast.Diagnostic{
				Code:    string(validation.CodeBalanceMismatch),
				Span:    b.Span,
				Message: fmt.Sprintf("failed to normalize tolerance: %v", err),
			})
			return diags
		}
	} else {
		tol = tolerance.ForBalanceAssertion(opts, b.Amount)
	}

	ok, err := tolerance.Within(diff, tol)
	if err != nil {
		diags = append(diags, ast.Diagnostic{
			Code:    string(validation.CodeBalanceMismatch),
			Span:    b.Span,
			Message: fmt.Sprintf("failed to evaluate balance tolerance: %v", err),
		})
		return diags
	}
	if !ok {
		diags = append(diags, ast.Diagnostic{
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
	return diags
}
