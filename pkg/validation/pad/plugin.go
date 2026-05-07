// Package pad implements the pad directive as an api.Plugin. It
// consumes each *ast.Pad, pairs it with subsequent *ast.Balance
// directives on the same account, and synthesizes padding
// *ast.Transaction entries dated at the pad's own date that will make
// each assertion succeed. Unresolved pads emit a pad-unresolved
// diagnostic. The plugin replaces the ledger via Result.Directives,
// keeping the original Pad directive in place and inserting the
// synthesized Transactions immediately after it. It intentionally
// does not re-verify the downstream Balance directive — that is the
// balance plugin's job.
//
// One pad covers many balance assertions, mirroring upstream
// beancount: a Pad on account A stays active across every subsequent
// Balance on A until a new Pad on A replaces it. Each padded currency
// is recorded so that a later assertion on the same currency does not
// produce a redundant adjustment. This is what makes a single
// `pad Assets:A Equity:Opening` work for both
// `balance Assets:A N CCY1` and `balance Assets:A M CCY2`.
//
// The pad-target-has-cost diagnostic is checked per (account,
// currency) and only at the moment a balance assertion would actually
// require synthesis. Cost-bearing positions in one currency do not
// disable padding for an unrelated currency on the same account: the
// constraint that the synthesized lot has no (Cost, Date, Label)
// identity only matters for the currency being padded. See
// pkg/inventory's "# Lot identity" package doc for the full rationale.
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
//
// Importing this package has the side effect of registering Apply in
// pkg/ext/postproc under the package's import path, so beancount
// `plugin "github.com/yugui/go-beancount/pkg/validation/pad"`
// directives can activate it.
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
			Code:    string(validation.CodeInvalidOption),
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

	// Running per-(account, currency) sum of cost-bearing postings
	// only. A non-zero value at balance-assertion time means the
	// account currently holds a cost-bearing position in that
	// currency, which pad cannot fill (no lot identity to invent).
	// Tracked separately from balances so that a cost-bearing
	// position in one currency does not poison padding for an
	// unrelated currency on the same account.
	costBalances := map[balanceKey]*apd.Decimal{}

	// First-seen source span of a cost-bearing posting per
	// (account, currency). Used as the diagnostic Span when emitting
	// pad-target-has-cost so the message points at the offending
	// posting rather than at the pad directive itself.
	costSpans := map[balanceKey]ast.Span{}

	// pending tracks the latest pad per target account. A subsequent
	// pad on the same account replaces the earlier one; if the earlier
	// pad was never paired with a balance assertion, the replacement
	// emits a pad-unresolved diagnostic. Once a balance assertion has
	// been paired with the pad (used == true), the pad is considered
	// satisfied and no diagnostic is emitted on replacement.
	pending := map[ast.Account]*pendingPad{}

	// synth[i] is the list of synthesized transactions to be inserted
	// immediately after the Pad at directive index i. A single pad may
	// produce multiple entries — one per currency that needs padding.
	synth := map[int][]*ast.Transaction{}

	for i, d := range in.Directives {
		switch x := d.(type) {
		case *ast.Transaction:
			diags = append(diags, applyTransaction(x, balances, costBalances, costSpans)...)
		case *ast.Pad:
			diags = append(diags, recordPad(x, i, pending)...)
		case *ast.Balance:
			diags = append(diags, resolveBalance(x, pending, balances, costBalances, costSpans, synth)...)
		}
	}

	// Report any pad that was never paired with a balance assertion
	// at the end of the walk. Sort by account for deterministic
	// output, matching upstream beancount's unresolved-pad reporting.
	if len(pending) > 0 {
		accounts := make([]ast.Account, 0, len(pending))
		for a := range pending {
			accounts = append(accounts, a)
		}
		slices.SortFunc(accounts, cmp.Compare[ast.Account])
		for _, a := range accounts {
			pp := pending[a]
			if pp.used {
				continue
			}
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
		for _, tx := range synth[i] {
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

// pendingPad tracks a pad directive awaiting resolution by one or
// more matching balance assertions on the same target account. Source
// order is preserved via the index so the output slice can reinstate
// the synthesized transactions at the pad's original position.
//
// paddedCurrencies records which currencies have already been
// processed under this pad so that a second balance assertion on the
// same currency does not produce a duplicate synth or duplicate
// diagnostic.
//
// used is set as soon as any balance assertion on the pad's account
// is observed (regardless of whether padding was actually needed). It
// is what distinguishes a satisfied pad from a dangling one for
// pad-unresolved reporting.
type pendingPad struct {
	dir              *ast.Pad
	index            int
	paddedCurrencies map[string]struct{}
	used             bool
}

// applyTransaction accumulates tx's postings into balances and, for
// cost-bearing postings, into costBalances. The running balance is
// updated only from postings that carry an explicit Amount; postings
// without one are reported as CodeAutoPostingUnresolved and skipped,
// since auto-posting resolution is owned by the booking pass that
// runs before validation.
func applyTransaction(
	tx *ast.Transaction,
	balances map[balanceKey]*apd.Decimal,
	costBalances map[balanceKey]*apd.Decimal,
	costSpans map[balanceKey]ast.Span,
) []ast.Diagnostic {
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
		// costBalances tracks the running sum of cost-bearing
		// postings only. A reduction (sell) of a cost-held lot is
		// expected to also carry a Cost spec — that is the
		// upstream beancount convention enforced by the booking
		// pass — so a buy followed by a fully-matched sell sums
		// to zero and the cost gate correctly stops blocking. A
		// bare reduction posting without a Cost spec would leave
		// this bucket non-zero even after the inventory has been
		// emptied, which would over-trigger pad-target-has-cost.
		if p.Cost != nil {
			addToBalance(costBalances, p.Account, p.Amount.Currency, &num)
			key := balanceKey{Account: p.Account, Currency: p.Amount.Currency}
			if _, seen := costSpans[key]; !seen {
				span := p.Span
				if span == (ast.Span{}) {
					span = tx.Span
				}
				costSpans[key] = span
			}
		}
	}
	return diags
}

// recordPad registers p as the pending pad for its target account at
// directive index i. If a previous pad on the same account is still
// pending, the previous pad is replaced; a pad-unresolved diagnostic
// is emitted only when the previous pad was never paired with a
// balance assertion.
func recordPad(p *ast.Pad, i int, pending map[ast.Account]*pendingPad) []ast.Diagnostic {
	var diags []ast.Diagnostic
	if prev, ok := pending[p.Account]; ok && !prev.used {
		diags = append(diags, ast.Diagnostic{
			Code:    string(validation.CodePadUnresolved),
			Span:    prev.dir.Span,
			Message: fmt.Sprintf("pad directive for %s from %s was not resolved before another pad", prev.dir.Account, prev.dir.PadAccount),
		})
	}
	pending[p.Account] = &pendingPad{
		dir:              p,
		index:            i,
		paddedCurrencies: map[string]struct{}{},
	}
	return diags
}

// resolveBalance pairs b with the pending pad on b.Account (if any),
// computes the residual the synthesized padding transaction must
// absorb, and records the synthesized transaction in synth keyed by
// the pad's directive index. The pending pad is *not* cleared by a
// successful balance: the same pad may resolve further balance
// assertions on other currencies of the same account.
//
// resolveBalance also gates the cost check per-currency and per-need:
//   - cost is checked only when the residual is non-zero (no
//     synthesis would happen otherwise, so there is nothing to refuse);
//   - cost is checked only against the asserted currency's bucket, so
//     a cost-bearing position in CCY1 does not block padding for CCY2.
//
// If no pad is pending for b.Account, resolveBalance is a no-op.
func resolveBalance(
	b *ast.Balance,
	pending map[ast.Account]*pendingPad,
	balances map[balanceKey]*apd.Decimal,
	costBalances map[balanceKey]*apd.Decimal,
	costSpans map[balanceKey]ast.Span,
	synth map[int][]*ast.Transaction,
) []ast.Diagnostic {
	pp, ok := pending[b.Account]
	if !ok {
		return nil
	}
	pp.used = true
	if _, already := pp.paddedCurrencies[b.Amount.Currency]; already {
		return nil
	}

	// residual = expected - actual. When zero (within the exact-match
	// model used here; tolerance is the balance plugin's concern),
	// the assertion already passes and there is nothing to synthesize
	// — and therefore no reason to consult the cost bucket.
	key := balanceKey{Account: b.Account, Currency: b.Amount.Currency}
	actual := balances[key]
	if actual == nil {
		actual = new(apd.Decimal)
	}
	expCopy := b.Amount.Number
	expected := &expCopy
	delta := new(apd.Decimal)
	if _, err := apd.BaseContext.Sub(delta, expected, actual); err != nil {
		// Mark the currency as processed: an apd internal failure
		// will recur on a duplicate balance for the same currency,
		// and one diagnostic per (pad, currency) is enough.
		pp.paddedCurrencies[b.Amount.Currency] = struct{}{}
		return []ast.Diagnostic{{
			Code:    string(validation.CodeInternalError),
			Span:    pp.dir.Span,
			Message: fmt.Sprintf("failed to compute pad residual for %q: %v", pp.dir.Account, err),
		}}
	}
	if delta.IsZero() {
		pp.paddedCurrencies[b.Amount.Currency] = struct{}{}
		return nil
	}

	// Per-currency cost gate: pad refuses to invent a lot identity
	// for the asserted currency only if that currency itself is
	// held at cost on the target account. See pkg/inventory's
	// "# Lot identity" package doc for the full rationale.
	if cb := costBalances[key]; cb != nil && !cb.IsZero() {
		span := costSpans[key]
		if span == (ast.Span{}) {
			span = pp.dir.Span
		}
		// Mark the currency as processed so a follow-up balance
		// assertion on the same currency does not re-emit the
		// same diagnostic.
		pp.paddedCurrencies[b.Amount.Currency] = struct{}{}
		return []ast.Diagnostic{{
			Code:    string(validation.CodePadTargetHasCost),
			Span:    span,
			Message: fmt.Sprintf("cannot pad account %q: holds cost-bearing position", pp.dir.Account),
		}}
	}

	neg := new(apd.Decimal)
	if _, err := apd.BaseContext.Neg(neg, delta); err != nil {
		// Same recurrence-suppression rationale as the Sub failure
		// above: mark the currency processed so a duplicate balance
		// does not emit a duplicate internal-error diagnostic.
		pp.paddedCurrencies[b.Amount.Currency] = struct{}{}
		return []ast.Diagnostic{{
			Code:    string(validation.CodeInternalError),
			Span:    pp.dir.Span,
			Message: fmt.Sprintf("failed to negate pad residual for %q: %v", pp.dir.Account, err),
		}}
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
	targetAmt := ast.Amount{Number: *ast.CloneDecimal(delta), Currency: b.Amount.Currency}
	sourceAmt := ast.Amount{Number: *ast.CloneDecimal(neg), Currency: b.Amount.Currency}
	synth[pp.index] = append(synth[pp.index], &ast.Transaction{
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
	})
	pp.paddedCurrencies[b.Amount.Currency] = struct{}{}
	return nil
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
