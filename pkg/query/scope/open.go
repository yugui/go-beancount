package scope

import (
	"iter"
	"slices"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
)

// SyntheticMetaKey marks a synthesized directive produced by scope. Its
// presence on a directive's metadata distinguishes scope-generated entries
// (opening-balance transfers, clear transfers) from user-authored ones.
// The value is the synthesis kind: "opening" for OPEN ON, "clearing" for
// CLEAR.
const SyntheticMetaKey = "__synthetic__"

// openSummarize implements OPEN ON D, optionally bounded above by CLOSE.
// The returned sequence is: Open directives dated < D, then per-account
// opening-balance transactions synthesized at D (sorted by account), then
// the original directives dated >= D (with d.DirDate() >= s.Close dropped
// when s.Close is non-zero). Zero-date header directives (Option, Plugin,
// Include) pass through the kept tail unchanged. Open directives dated
// exactly D fall into the kept tail and so are not duplicated.
//
// Walks l.All() twice (prefix for inventory and kept Opens; tail when the
// iterator is consumed); relies on its documented replayability.
func openSummarize(l *ast.Ledger, s Spec) iter.Seq2[int, ast.Directive] {
	openDate := s.Open

	invMap := map[ast.Account]*inventory.Inventory{}
	var preOpens []ast.Directive
	for _, d := range l.All() {
		switch v := d.(type) {
		case *ast.Open:
			if v.Date.Before(openDate) {
				preOpens = append(preOpens, v)
			}
		case *ast.Transaction:
			if !v.Date.Before(openDate) {
				continue
			}
			for i := range v.Postings {
				p := &v.Postings[i]
				pos, ok := postingPosition(p)
				if !ok {
					continue
				}
				inv, ok := invMap[p.Account]
				if !ok {
					inv = inventory.NewInventory()
					invMap[p.Account] = inv
				}
				_ = inv.Add(pos)
			}
		}
	}

	accounts := make([]ast.Account, 0, len(invMap))
	for a, inv := range invMap {
		if inv.IsEmpty() {
			continue
		}
		accounts = append(accounts, a)
	}
	slices.Sort(accounts)

	openingBalances := ast.Account(l.Options.String("account_previous_balances"))
	openings := make([]ast.Directive, 0, len(accounts))
	for _, a := range accounts {
		routing, isIE := classifyAccount(a, l.Options)
		openings = append(openings, synthesizeOpeningTxn(a, routing, isIE, openingBalances, invMap[a], openDate))
	}

	return func(yield func(int, ast.Directive) bool) {
		idx := 0
		emit := func(d ast.Directive) bool {
			if !yield(idx, d) {
				return false
			}
			idx++
			return true
		}
		for _, d := range preOpens {
			if !emit(d) {
				return
			}
		}
		for _, d := range openings {
			if !emit(d) {
				return
			}
		}
		for _, d := range l.All() {
			dd := d.DirDate()
			if !dd.IsZero() && dd.Before(openDate) {
				continue
			}
			if !s.Close.IsZero() && !dd.Before(s.Close) {
				continue
			}
			if !emit(d) {
				return
			}
		}
	}
}

// postingPosition builds the inventory Position a booked posting contributes:
// its units plus the booked lot, or ok=false when the posting has no amount.
// Mirrors the helper in pkg/query/table without exposing it across the
// package boundary.
func postingPosition(p *ast.Posting) (inventory.Position, bool) {
	if p.Amount == nil {
		return inventory.Position{}, false
	}
	var lot *inventory.Lot
	if p.Cost != nil && p.Cost.IsBooked() {
		lot = inventory.LotFromCost(p.Cost.(*ast.Cost))
	}
	return inventory.Position{Units: *p.Amount, Cost: lot}, true
}

// classifyAccount routes acct's boundary balance to its equity
// counterpart. Accounts under the income or expense root (per opts'
// name_income/name_expenses) route to account_previous_earnings; all
// others to account_previous_balances. opts is nil-safe and falls back
// to registry defaults.
func classifyAccount(acct ast.Account, opts *ast.OptionValues) (routing ast.Account, isIncomeOrExpense bool) {
	root := string(acct.Root())
	income := opts.String("name_income")
	expenses := opts.String("name_expenses")
	if root == income || root == expenses {
		return ast.Account(opts.String("account_previous_earnings")), true
	}
	return ast.Account(opts.String("account_previous_balances")), false
}

// synthesizeOpeningTxn builds the opening-balance transaction for one
// account. For asset/liability accounts (isIncomeOrExpense=false) each
// inventory position emits a posting on the account itself, preserving
// the booked Cost lot so subsequent reductions still match, paired with
// account_previous_balances. For income/expense accounts
// (isIncomeOrExpense=true) the account itself is not posted: its
// cumulative pre-D balance is transferred to account_previous_earnings,
// with the opposing side booked to account_previous_balances. The income
// or expense account's running total therefore resets to zero across the
// boundary, matching beanquery's summarize semantics. The returned
// transaction is balanced.
func synthesizeOpeningTxn(acct, routing ast.Account, isIncomeOrExpense bool, openingBalances ast.Account, inv *inventory.Inventory, date time.Time) *ast.Transaction {
	txn := &ast.Transaction{
		Date:      date,
		Flag:      '*',
		Narration: "Opening balance for '" + string(acct) + "'",
		Meta: ast.Metadata{
			Props: map[string]ast.MetaValue{
				SyntheticMetaKey: {Kind: ast.MetaString, String: "opening"},
			},
		},
	}
	for p := range inv.All() {
		units := p.Units.Clone()
		negated := p.Units.Clone()
		negated.Number.Negative = !negated.Number.Negative

		if isIncomeOrExpense {
			txn.Postings = append(txn.Postings,
				ast.Posting{Account: routing, Amount: units},
				ast.Posting{Account: openingBalances, Amount: negated},
			)
			continue
		}

		acctPosting := ast.Posting{Account: acct, Amount: units}
		if p.Cost != nil {
			acctPosting.Cost = p.Cost.ToCost()
		}
		txn.Postings = append(txn.Postings,
			acctPosting,
			ast.Posting{Account: routing, Amount: negated},
		)
	}
	return txn
}
