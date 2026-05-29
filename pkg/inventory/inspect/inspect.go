// Package inspect resolves the beancount directive at a source location to a
// view of its effect on per-account inventory, in the style of upstream
// bean-doctor's "context" command.
//
// The package is rendering-agnostic: [Resolve] returns a neutral [View] that
// callers (the LSP server's hover, a future bean-doctor CLI) format however
// they like. It operates on a fully-processed [ast.Ledger] snapshot — one that
// has already been through booking and the pad pass — so booked amounts,
// resolved costs, and pad-synthesized transactions are visible.
package inspect

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
)

// ViewKind classifies a resolved [View].
type ViewKind int

const (
	// KindNone means the directive under the cursor has no inventory effect
	// to display: Open, Price, Note, Event, Document, Commodity, Query, the
	// header directives, an unused Pad, or a Custom that references nothing.
	KindNone ViewKind = iota
	// KindTransaction is the effect of a transaction (or a pad's synthesized
	// transaction): a before/after snapshot of every account it touches.
	KindTransaction
	// KindBalance is the state of the asserted account at the last
	// transaction strictly before the balance date.
	KindBalance
	// KindClose is the state of the closed account at the last transaction on
	// or before the close date.
	KindClose
)

// Target identifies a cursor location in ledger source by absolute file path
// and byte offset into that file.
type Target struct {
	Filename string
	Offset   int
}

// AccountView is one account's inventory under a resolved [View].
type AccountView struct {
	Account ast.Account
	// Before is the account's inventory before the transaction's effect; nil
	// when the account had not been seen yet. Unused (nil) for Balance/Close.
	Before *inventory.Inventory
	// After is the account's inventory after the effect. For Transaction it is
	// a non-nil snapshot of every touched account. For Balance/Close it is the
	// state at the last qualifying transaction, or nil when none exists.
	After *inventory.Inventory
	// Booked carries the per-posting booking outcomes for a Transaction view;
	// nil for Balance/Close.
	Booked []inventory.BookedPosting
}

// View is the resolved inventory effect of the directive under a [Target].
// The zero value (Kind KindNone) means "display nothing".
type View struct {
	Kind ViewKind
	// Date is the originating directive's date.
	Date time.Time
	// Accounts holds the affected accounts in a deterministic order:
	// transaction views follow posting order; Balance/Close hold the single
	// asserted account.
	Accounts []AccountView
	// Booked is the full per-posting booking outcome in booking order, with
	// auto-balanced amounts resolved and multi-lot reductions expanded into
	// one entry per matched lot. Populated for Transaction/Pad views; nil for
	// Balance/Close.
	Booked []inventory.BookedPosting
	// AssertedAccount is the account a Balance/Close targets; zero otherwise.
	AssertedAccount ast.Account
	// Asserted is a Balance directive's asserted amount, for comparison
	// against the actual state; nil for non-Balance kinds.
	Asserted *ast.Amount
	// Diagnostics surfaces findings collected while reconstructing the view.
	Diagnostics []ast.Diagnostic
}

// Resolve maps the directive under tgt to its inventory effect view. ledger
// must be a fully-processed snapshot (booking + pad applied). It returns a
// View with Kind [KindNone] (and a nil error) whenever nothing should be
// displayed — an unaffected directive type, a cursor outside every directive,
// or a nil ledger.
//
// The error return is reserved for booking-pass implementation bugs surfaced
// by the underlying reducer (see [inventory.Reducer.Walk]).
func Resolve(ledger *ast.Ledger, tgt Target) (View, error) {
	if ledger == nil {
		return View{}, nil
	}
	switch dir := pickDirective(ledger, tgt).(type) {
	case *ast.Transaction:
		return txnView(ledger, dir)
	case *ast.Pad:
		return padView(ledger, dir)
	case *ast.Balance:
		return balanceView(ledger, dir)
	case *ast.Close:
		return closeView(ledger, dir)
	case *ast.Custom:
		return customView(ledger, dir)
	default:
		return View{}, nil
	}
}

// pickDirective returns the directive whose span contains tgt, choosing the
// narrowest span and, on an exact tie, the source directive over a
// plugin-synthesized one (a Pad over the transaction it generated at the same
// span). Returns nil when no directive contains tgt.
func pickDirective(ledger *ast.Ledger, tgt Target) ast.Directive {
	want := filepath.Clean(tgt.Filename)
	var best ast.Directive
	var bestWidth int
	for _, d := range ledger.All() {
		sp := d.DirSpan()
		if filepath.Clean(sp.Start.Filename) != want {
			continue
		}
		if tgt.Offset < sp.Start.Offset || tgt.Offset > sp.End.Offset {
			continue
		}
		w := sp.End.Offset - sp.Start.Offset
		if best == nil || w < bestWidth || (w == bestWidth && sourceRank(d) < sourceRank(best)) {
			best, bestWidth = d, w
		}
	}
	return best
}

// sourceRank ranks directives for the pickDirective tie-break: a lower rank is
// preferred. Pad and Custom (which carry real source text) outrank the
// transaction a plugin may synthesize at the identical span.
func sourceRank(d ast.Directive) int {
	switch d.(type) {
	case *ast.Pad, *ast.Custom:
		return 0
	case *ast.Transaction:
		return 2
	default:
		return 1
	}
}

func txnView(ledger *ast.Ledger, txn *ast.Transaction) (View, error) {
	r := inventory.NewReducerWithOptions(ledger.All(), ledger.Options)
	insp, diags, err := r.Inspect(txn)
	if err != nil {
		return View{Diagnostics: diags}, err
	}
	if insp == nil {
		return View{Kind: KindNone, Diagnostics: diags}, nil
	}
	return View{
		Kind:        KindTransaction,
		Date:        txn.Date,
		Accounts:    accountViews(insp, txn),
		Booked:      insp.Booked,
		Diagnostics: diags,
	}, nil
}

// padView shows the padding transaction a pad generated. A resolved pad shares
// its span with the synthesized transaction(s); the earliest such transaction
// is shown (a pad typically yields exactly one). An unused pad has no sharing
// transaction and resolves to KindNone.
func padView(ledger *ast.Ledger, pad *ast.Pad) (View, error) {
	txns := txnsSharingSpan(ledger, pad.Span)
	if len(txns) == 0 {
		return View{Kind: KindNone}, nil
	}
	return txnView(ledger, txns[0])
}

func balanceView(ledger *ast.Ledger, b *ast.Balance) (View, error) {
	r := inventory.NewReducerWithOptions(ledger.All(), ledger.Options)
	// strictly before: the assertion holds at the start of the balance date.
	after, err := afterStateAt(r, b.Account, func(d time.Time) bool { return d.Before(b.Date) })
	if err != nil {
		return View{Diagnostics: r.Errors()}, err
	}
	amt := b.Amount
	return View{
		Kind:            KindBalance,
		Date:            b.Date,
		AssertedAccount: b.Account,
		Asserted:        &amt,
		Accounts:        []AccountView{{Account: b.Account, After: after}},
		Diagnostics:     r.Errors(),
	}, nil
}

func closeView(ledger *ast.Ledger, c *ast.Close) (View, error) {
	r := inventory.NewReducerWithOptions(ledger.All(), ledger.Options)
	// inclusive: the account is closed at the end of the close date.
	after, err := afterStateAt(r, c.Account, func(d time.Time) bool { return !d.After(c.Date) })
	if err != nil {
		return View{Diagnostics: r.Errors()}, err
	}
	return View{
		Kind:            KindClose,
		Date:            c.Date,
		AssertedAccount: c.Account,
		Accounts:        []AccountView{{Account: c.Account, After: after}},
		Diagnostics:     r.Errors(),
	}, nil
}

// customView follows directives that share the custom's span (e.g. ones a
// plugin synthesized from it) and resolves to the first that carries an
// inventory effect. Resolves to KindNone when nothing shares the span.
func customView(ledger *ast.Ledger, c *ast.Custom) (View, error) {
	for _, d := range directivesSharingSpan(ledger, c.Span) {
		switch dir := d.(type) {
		case *ast.Transaction:
			return txnView(ledger, dir)
		case *ast.Pad:
			return padView(ledger, dir)
		case *ast.Balance:
			return balanceView(ledger, dir)
		case *ast.Close:
			return closeView(ledger, dir)
		}
	}
	return View{Kind: KindNone}, nil
}

// afterStateAt returns the inventory of acct immediately after the last
// transaction whose date satisfies keep, or nil when no such transaction
// touches acct. It relies on chronological iteration: once keep fails the walk
// stops, because all later transactions also fail a date-monotone predicate.
func afterStateAt(r *inventory.Reducer, acct ast.Account, keep func(time.Time) bool) (*inventory.Inventory, error) {
	var last *inventory.Inventory
	_, _, err := r.Walk(func(txn *ast.Transaction, _, after map[ast.Account]*inventory.Inventory, _ []inventory.BookedPosting) bool {
		if !keep(txn.Date) {
			return false
		}
		if inv, ok := after[acct]; ok {
			last = inv
		}
		return true
	})
	return last, err
}

// accountViews builds the per-account before/after slice from insp, ordered by
// first appearance in txn's postings, with any remaining touched accounts
// appended in account-name order.
func accountViews(insp *inventory.Inspection, txn *ast.Transaction) []AccountView {
	bookedByAcct := map[ast.Account][]inventory.BookedPosting{}
	for _, b := range insp.Booked {
		bookedByAcct[b.Account] = append(bookedByAcct[b.Account], b)
	}

	seen := map[ast.Account]bool{}
	var order []ast.Account
	touched := func(a ast.Account) bool {
		_, inAfter := insp.After[a]
		_, inBefore := insp.Before[a]
		return inAfter || inBefore
	}
	add := func(a ast.Account) {
		if !seen[a] && touched(a) {
			seen[a] = true
			order = append(order, a)
		}
	}
	for _, p := range txn.Postings {
		add(p.Account)
	}
	var rest []ast.Account
	for a := range insp.After {
		if !seen[a] {
			seen[a] = true
			rest = append(rest, a)
		}
	}
	for a := range insp.Before {
		if !seen[a] {
			seen[a] = true
			rest = append(rest, a)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i] < rest[j] })
	order = append(order, rest...)

	out := make([]AccountView, 0, len(order))
	for _, a := range order {
		out = append(out, AccountView{
			Account: a,
			Before:  insp.Before[a],
			After:   insp.After[a],
			Booked:  bookedByAcct[a],
		})
	}
	return out
}

func txnsSharingSpan(ledger *ast.Ledger, span ast.Span) []*ast.Transaction {
	var out []*ast.Transaction
	for _, d := range ledger.All() {
		if t, ok := d.(*ast.Transaction); ok && t.Span == span {
			out = append(out, t)
		}
	}
	return out
}

// directivesSharingSpan returns effect-bearing directives whose span equals
// span, excluding Custom directives so a custom never matches itself or another
// custom.
func directivesSharingSpan(ledger *ast.Ledger, span ast.Span) []ast.Directive {
	var out []ast.Directive
	for _, d := range ledger.All() {
		if _, isCustom := d.(*ast.Custom); isCustom {
			continue
		}
		if d.DirSpan() == span {
			out = append(out, d)
		}
	}
	return out
}
