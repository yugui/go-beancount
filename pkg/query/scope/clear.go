package scope

import (
	"iter"
	"slices"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
)

// clearTail appends synthesized clearing transactions to the tail of the
// kept stream. The kept stream is the materialized output of OPEN and/or
// CLOSE (or l.All() when neither is set). Clearing zeroes the boundary-date
// inventory of income- and expense-rooted accounts by routing the offset to
// account_current_earnings.
//
// Boundary date: s.Close.AddDate(0,0,-1) when Close is non-zero, else the
// last non-zero DirDate in kept, else time.Now().UTC() truncated to date.
//
// Walks kept once to accumulate per-account inventories; emits one
// transaction per non-empty income/expense account, sorted lexicographically.
func clearTail(l *ast.Ledger, s Spec, kept []ast.Directive) iter.Seq2[int, ast.Directive] {
	boundary := clearBoundary(s, kept)

	invMap := map[ast.Account]*inventory.Inventory{}
	for _, d := range kept {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		for i := range txn.Postings {
			p := &txn.Postings[i]
			_, isIE := classifyAccount(p.Account, l.Options)
			if !isIE {
				continue
			}
			pos, ok := postingPosition(p)
			if !ok {
				continue
			}
			inv, ok := invMap[p.Account]
			if !ok {
				inv = inventory.NewInventory()
				invMap[p.Account] = inv
			}
			_ = inv.Add(pos) // inv.Add never fails for loader-booked positions
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

	routing := ast.Account(l.Options.String("account_current_earnings"))
	clearings := make([]ast.Directive, 0, len(accounts))
	for _, a := range accounts {
		clearings = append(clearings, synthesizeClearingTxn(a, routing, invMap[a], boundary))
	}

	return func(yield func(int, ast.Directive) bool) {
		idx := 0
		for _, d := range kept {
			if !yield(idx, d) {
				return
			}
			idx++
		}
		for _, d := range clearings {
			if !yield(idx, d) {
				return
			}
			idx++
		}
	}
}

// clearBoundary resolves the boundary date for CLEAR (see clearTail).
func clearBoundary(s Spec, kept []ast.Directive) time.Time {
	if !s.Close.IsZero() {
		return s.Close.AddDate(0, 0, -1)
	}
	for i := len(kept) - 1; i >= 0; i-- {
		d := kept[i].DirDate()
		if !d.IsZero() {
			return d
		}
	}
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// synthesizeClearingTxn builds the clearing transaction for one income or
// expense account: a posting pair per inventory position, the account leg
// negating the boundary balance and the routing leg carrying the original
// units to account_current_earnings. The returned transaction is balanced.
func synthesizeClearingTxn(acct, routing ast.Account, inv *inventory.Inventory, date time.Time) *ast.Transaction {
	txn := &ast.Transaction{
		Date:      date,
		Flag:      '*',
		Narration: "Clear balance for '" + string(acct) + "'",
		Meta: ast.Metadata{
			Props: map[string]ast.MetaValue{
				SyntheticMetaKey: {Kind: ast.MetaString, String: "clearing"},
			},
		},
	}
	for p := range inv.All() {
		units := p.Units.Clone()
		negated := p.Units.Clone()
		negated.Number.Negative = !negated.Number.Negative

		acctPosting := ast.Posting{Account: acct, Amount: negated}
		if p.Cost != nil {
			acctPosting.Cost = p.Cost.ToCost()
		}
		txn.Postings = append(txn.Postings,
			acctPosting,
			ast.Posting{Account: routing, Amount: units},
		)
	}
	return txn
}
