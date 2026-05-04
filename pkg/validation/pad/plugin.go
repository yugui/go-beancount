// Package pad implements the pad directive as an api.Plugin. It
// consumes each *ast.Pad, pairs it with the next subsequent
// *ast.Balance on the same account, and synthesizes a padding
// *ast.Transaction dated at the pad's own date that will make the
// subsequent assertion succeed. Unresolved pads emit a pad-unresolved
// diagnostic. The plugin replaces the ledger via Result.Directives,
// keeping the original Pad directive in place and inserting the
// synthesized Transaction immediately after it. It intentionally
// does not re-verify the downstream Balance directive — that is the
// balance plugin's job.
//
// Relation to upstream beancount: upstream beancount/ops/pad.py is a
// standard plugin registered via __plugins__ = ("pad",) that also
// materializes synthetic data.Transaction entries flagged with
// flags.FLAG_PADDING and inserts them immediately after the
// originating Pad directive. This plugin is a Go port producing the
// equivalent output shape. One remaining difference: upstream sorts
// entries per-account via realization.postings_by_account before
// walking, while this plugin walks directive-order in a single pass.
// Both approaches reach the same resolved/unresolved outcome.
package pad

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation"
)

// Apply transforms a beancount ledger by synthesizing padding
// transactions to resolve `pad`/`balance` discrepancies.
//
// Expects booked AST: postings without an Amount yield a
// CodeAutoPostingUnresolved diagnostic instead of being inferred.
// Auto-posting resolution is owned by the booking pass that runs
// before validation.
func Apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}

	// options.FromRaw is called for parity with the balance plugin:
	// future option keys may affect pad behavior. The parsed *Values
	// itself is currently unused.
	_, optErrs := options.FromRaw(in.Options)
	var diags []ast.Diagnostic
	for _, perr := range optErrs {
		diags = append(diags, ast.Diagnostic{
			Code:    "invalid-option",
			Span:    perr.Span,
			Message: fmt.Sprintf("invalid option %q: %v", perr.Key, perr.Err),
		})
	}

	if in.Directives == nil {
		return api.Result{Diagnostics: diags}, nil
	}

	// Running per-(account, currency) balance, accumulated from every
	// *ast.Transaction posting (real and previously synthesized). Used
	// to compute each pad's residual at the moment its matching
	// Balance directive is encountered.
	balances := map[balanceKey]*apd.Decimal{}

	// pending tracks the latest unresolved pad per target account. A
	// subsequent pad on the same account drops (and reports) the
	// earlier one, matching upstream beancount's pad-visit semantics.
	pending := map[ast.Account]*pendingPad{}

	// synth[i] is the synthesized transaction to be inserted
	// immediately after the Pad at directive index i. A pad index
	// without an entry in synth is unresolved; its Pad remains in the
	// output with no following synthesized transaction.
	synth := map[int]*ast.Transaction{}

	for i, d := range in.Directives {
		switch x := d.(type) {
		case *ast.Transaction:
			diags = append(diags, applyTransaction(x, balances)...)
		case *ast.Pad:
			diags = append(diags, recordPad(x, i, pending)...)
		case *ast.Balance:
			diags = append(diags, resolveBalance(x, pending, balances, synth)...)
		}
	}

	// Report any pads left pending at the end of the walk. Sort by
	// account for deterministic output, matching upstream beancount's
	// unresolved-pad reporting.
	if len(pending) > 0 {
		accounts := make([]ast.Account, 0, len(pending))
		for a := range pending {
			accounts = append(accounts, a)
		}
		slices.SortFunc(accounts, cmp.Compare[ast.Account])
		for _, a := range accounts {
			pp := pending[a]
			diags = append(diags, ast.Diagnostic{
				Code:    string(validation.CodePadUnresolved),
				Span:    pp.dir.Span,
				Message: fmt.Sprintf("pad directive for %s from %s was not followed by a matching balance assertion", pp.dir.Account, pp.dir.PadAccount),
			})
		}
	}

	// If no pad was successfully resolved, the ledger is unchanged.
	// Returning Directives=nil signals the runner to preserve the
	// input verbatim, matching the documented convention.
	if len(synth) == 0 {
		return api.Result{Diagnostics: diags}, nil
	}

	// Build the output slice: keep every original directive in place
	// and insert each synthesized Transaction immediately after its
	// originating Pad. Matches upstream beancount/ops/pad.py's final
	// loop:
	//   padded_entries.append(entry)
	//   if isinstance(entry, Pad):
	//       padded_entries.extend(new_entries[id(entry)])
	var out []ast.Directive
	for i, d := range in.Directives {
		out = append(out, d)
		if tx, ok := synth[i]; ok {
			out = append(out, tx)
		}
	}
	return api.Result{Directives: out, Diagnostics: diags}, nil
}

// init registers Apply in the global registry so that, once this
// package is imported, a beancount `plugin "..."` directive can
// activate it by name.
func init() {
	postproc.Register("github.com/yugui/go-beancount/pkg/validation/pad", api.PluginFunc(Apply))
}

// balanceKey identifies a running balance bucket by (account, currency).
// The pad plugin keeps its own running-balance map (in the native
// currency of each posting) in order to compute per-pad residuals.
type balanceKey struct {
	Account  ast.Account
	Currency string
}

// pendingPad tracks a pad directive awaiting resolution by a matching
// balance assertion on the same target account. Source order is
// preserved via the index so the output slice can reinstate the
// synthesized transaction at the pad's original position.
type pendingPad struct {
	dir   *ast.Pad
	index int
}

// applyTransaction accumulates tx's postings into balances. The
// running balance is updated only from postings that carry an
// explicit Amount; postings without one are reported as
// CodeAutoPostingUnresolved and skipped, since auto-posting
// resolution is owned by the booking pass that runs before
// validation.
func applyTransaction(tx *ast.Transaction, balances map[balanceKey]*apd.Decimal) []ast.Diagnostic {
	var diags []ast.Diagnostic
	for j := range tx.Postings {
		p := &tx.Postings[j]
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
		addToBalance(balances, p.Account, p.Amount.Currency, &num)
	}
	return diags
}

// recordPad registers p as the pending pad for its target account at
// directive index i. If a previous unresolved pad exists for the same
// account, it is evicted with a pad-unresolved diagnostic before being
// replaced, matching upstream beancount's pad-visit semantics.
func recordPad(p *ast.Pad, i int, pending map[ast.Account]*pendingPad) []ast.Diagnostic {
	var diags []ast.Diagnostic
	if prev, ok := pending[p.Account]; ok {
		diags = append(diags, ast.Diagnostic{
			Code:    string(validation.CodePadUnresolved),
			Span:    prev.dir.Span,
			Message: fmt.Sprintf("pad directive for %s from %s was not resolved before another pad", prev.dir.Account, prev.dir.PadAccount),
		})
	}
	pending[p.Account] = &pendingPad{dir: p, index: i}
	return diags
}

// resolveBalance pairs b with the pending pad on b.Account, computes
// the residual that the synthesized padding transaction must absorb,
// updates the running balance to reflect that synthesized effect,
// records the transaction in synth keyed by the pad's directive index,
// and clears the pending entry. If no pad is pending for b.Account,
// resolveBalance is a no-op.
func resolveBalance(b *ast.Balance, pending map[ast.Account]*pendingPad, balances map[balanceKey]*apd.Decimal, synth map[int]*ast.Transaction) []ast.Diagnostic {
	var diags []ast.Diagnostic
	pp, ok := pending[b.Account]
	if !ok {
		return nil
	}
	// Compute the residual the synthesized transaction must absorb
	// so that the downstream balance assertion passes:
	//   residual = expected - actual
	// where actual is the running balance for
	// (pad.Account, balance.Amount.Currency) at this point.
	key := balanceKey{Account: b.Account, Currency: b.Amount.Currency}
	actual := balances[key]
	if actual == nil {
		actual = new(apd.Decimal)
	}
	expCopy := b.Amount.Number
	expected := &expCopy
	delta := new(apd.Decimal)
	if _, err := apd.BaseContext.Sub(delta, expected, actual); err != nil {
		diags = append(diags, ast.Diagnostic{
			Code:    string(validation.CodeInternalError),
			Span:    pp.dir.Span,
			Message: fmt.Sprintf("failed to compute pad residual for %q: %v", pp.dir.Account, err),
		})
		delete(pending, b.Account)
		return diags
	}
	neg := new(apd.Decimal)
	if _, err := apd.BaseContext.Neg(neg, delta); err != nil {
		diags = append(diags, ast.Diagnostic{
			Code:    string(validation.CodeInternalError),
			Span:    pp.dir.Span,
			Message: fmt.Sprintf("failed to negate pad residual for %q: %v", pp.dir.Account, err),
		})
		delete(pending, b.Account)
		return diags
	}

	// Apply the synthesized effect to the running balance so later
	// pads resolved against subsequent balance assertions see the
	// correct baseline.
	addToBalance(balances, pp.dir.Account, b.Amount.Currency, delta)
	addToBalance(balances, pp.dir.PadAccount, b.Amount.Currency, neg)

	// Build the synthesized transaction dated at pad.Date. We use
	// explicit postings (no auto-posting) so downstream plugins
	// observe both legs unambiguously. Upstream beancount/ops/pad.py
	// uses
	//   PAD_DESC = "(Padding inserted for Balance of {} for difference {})"
	// (Python str.format brace syntax). The Go-side fmt.Sprintf form
	// below uses %s verbs purely as an adaptation to Go's formatting
	// conventions; it is not a direct port of upstream's format
	// syntax. This plugin also carries an extended form that repeats
	// the currency tag on each amount
	//   "(Padding inserted for Balance of %s %s for difference %s %s)"
	// to keep amount/currency pairing uniform with the rest of the
	// AST's formatting.
	targetAmt := ast.Amount{Number: *copyDecimal(delta), Currency: b.Amount.Currency}
	sourceAmt := ast.Amount{Number: *copyDecimal(neg), Currency: b.Amount.Currency}
	synth[pp.index] = &ast.Transaction{
		Span: pp.dir.Span,
		Date: pp.dir.Date,
		Flag: '*',
		Narration: fmt.Sprintf(
			"(Padding inserted for Balance of %s %s for difference %s %s)",
			expected.Text('f'), b.Amount.Currency,
			delta.Text('f'), b.Amount.Currency,
		),
		Postings: []ast.Posting{
			{Account: pp.dir.Account, Amount: &targetAmt},
			{Account: pp.dir.PadAccount, Amount: &sourceAmt},
		},
	}
	delete(pending, b.Account)
	return diags
}

// addToBalance mutates balances[(account, currency)] += delta,
// initializing the bucket on first write. Arithmetic errors are
// silently absorbed: apd.BaseContext.Add only fails on pathological
// exponents, and the caller context does not provide a useful place to
// surface the failure.
func addToBalance(balances map[balanceKey]*apd.Decimal, account ast.Account, currency string, delta *apd.Decimal) {
	if delta == nil {
		return
	}
	key := balanceKey{Account: account, Currency: currency}
	cur, ok := balances[key]
	if !ok {
		cur = new(apd.Decimal)
		balances[key] = cur
	}
	_, _ = apd.BaseContext.Add(cur, cur, delta)
}

// copyDecimal returns a freshly allocated copy of x so the synthesized
// transaction does not alias the plugin's working decimals.
func copyDecimal(x *apd.Decimal) *apd.Decimal {
	out := new(apd.Decimal)
	out.Set(x)
	return out
}
