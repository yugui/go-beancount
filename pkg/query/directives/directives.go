// Package directives provides the immutable, query-wide index of a ledger's
// account- and currency-scoped directives (Open, Close, Commodity) that BQL
// directive-context functions read. Its central type [Index] is built once,
// lazily, and is safe for concurrent read-only use thereafter.
package directives

import (
	"fmt"
	"sync"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/metaval"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// Canonical beancount account-type ordering used by [Index.Sign] and
// [Index.SortKey]: Assets, Liabilities, Equity, Income, Expenses.
const (
	rootAssets = iota
	rootLiabilities
	rootEquity
	rootIncome
	rootExpenses
	rootCount
)

// rootSign holds the beancount sign convention indexed by account-type:
// +1 for Assets/Expenses, -1 for Liabilities/Equity/Income.
var rootSign = [rootCount]int{
	rootAssets:      +1,
	rootLiabilities: -1,
	rootEquity:      -1,
	rootIncome:      -1,
	rootExpenses:    +1,
}

// Index is an immutable index of a ledger's account- and currency-scoped
// directives. It maps each account to its first Open and (if any) Close, and
// each currency to its Commodity directive, and answers account-type queries
// (Sign, SortKey) using the ledger's name_* options.
//
// The index is built once, lazily, on the first lookup (via sync.Once); after
// that the stored data never changes, so an Index is safe for concurrent
// read-only use by many goroutines (Decision 6; see pkg/query/ARCHITECTURE.md
// §4). A query that never calls a directive-context function therefore never
// pays the indexing cost.
//
// A nil ledger yields an empty index: every directive lookup misses, while the
// account-type methods (Sign, SortKey) still work using the option defaults.
type Index struct {
	ledger *ast.Ledger
	opts   *ast.OptionValues
	once   sync.Once

	opens       map[ast.Account]*ast.Open
	closes      map[ast.Account]*ast.Close
	commodities map[string]*ast.Commodity
	rootIndex   map[string]int
}

// NewIndex returns an Index over ledger, classifying accounts with opts'
// name_assets..name_expenses options (opts is nil-safe and falls back to the
// registered defaults). It does not scan the ledger; the index is built lazily
// on the first lookup. A nil ledger yields an empty index.
func NewIndex(ledger *ast.Ledger, opts *ast.OptionValues) *Index {
	return &Index{ledger: ledger, opts: opts}
}

func (ix *Index) build() {
	ix.opens = map[ast.Account]*ast.Open{}
	ix.closes = map[ast.Account]*ast.Close{}
	ix.commodities = map[string]*ast.Commodity{}
	ix.rootIndex = map[string]int{
		ix.opts.String("name_assets"):      rootAssets,
		ix.opts.String("name_liabilities"): rootLiabilities,
		ix.opts.String("name_equity"):      rootEquity,
		ix.opts.String("name_income"):      rootIncome,
		ix.opts.String("name_expenses"):    rootExpenses,
	}
	if ix.ledger == nil {
		return
	}
	for _, dir := range ix.ledger.All() {
		switch d := dir.(type) {
		case *ast.Open:
			if _, ok := ix.opens[d.Account]; !ok {
				ix.opens[d.Account] = d
			}
		case *ast.Close:
			if _, ok := ix.closes[d.Account]; !ok {
				ix.closes[d.Account] = d
			}
		case *ast.Commodity:
			if _, ok := ix.commodities[d.Currency]; !ok {
				ix.commodities[d.Currency] = d
			}
		}
	}
}

// OpenDate returns the date of the first Open directive for acct, and whether
// one exists.
func (ix *Index) OpenDate(acct ast.Account) (time.Time, bool) {
	ix.once.Do(ix.build)
	if o, ok := ix.opens[acct]; ok {
		return o.Date, true
	}
	return time.Time{}, false
}

// CloseDate returns the date of the first Close directive for acct, and whether
// one exists.
func (ix *Index) CloseDate(acct ast.Account) (time.Time, bool) {
	ix.once.Do(ix.build)
	if c, ok := ix.closes[acct]; ok {
		return c.Date, true
	}
	return time.Time{}, false
}

// HasAccount reports whether an Open directive exists for acct. It performs no
// date test (an account is "known" once opened, regardless of any later Close).
func (ix *Index) HasAccount(acct ast.Account) bool {
	ix.once.Do(ix.build)
	_, ok := ix.opens[acct]
	return ok
}

// OpenMeta returns the metadata of the first Open directive for acct as a
// [types.Dict], and whether such an Open exists. On a miss it returns an empty
// Dict and false.
func (ix *Index) OpenMeta(acct ast.Account) (types.Dict, bool) {
	ix.once.Do(ix.build)
	if o, ok := ix.opens[acct]; ok {
		return metaval.Dict(o.Meta), true
	}
	return types.NewDict(nil), false
}

// CurrencyMeta returns the metadata of the Commodity directive for currency as
// a [types.Dict], and whether such a directive exists. On a miss it returns an
// empty Dict and false.
func (ix *Index) CurrencyMeta(currency string) (types.Dict, bool) {
	ix.once.Do(ix.build)
	if c, ok := ix.commodities[currency]; ok {
		return metaval.Dict(c.Meta), true
	}
	return types.NewDict(nil), false
}

// Sign returns the beancount sign convention for acct's account type: +1 for
// Assets/Expenses, -1 for Liabilities/Equity/Income, and 0 for an unknown root.
func (ix *Index) Sign(acct ast.Account) int {
	ix.once.Do(ix.build)
	idx, ok := ix.classify(acct)
	if !ok {
		return 0
	}
	return rootSign[idx]
}

// SortKey returns a string that orders accounts by (type-index, name) with the
// type order Assets, Liabilities, Equity, Income, Expenses; accounts with an
// unknown root sort after all known types. The exact byte content is
// unspecified; only the induced ordering is part of the contract.
func (ix *Index) SortKey(acct ast.Account) string {
	ix.once.Do(ix.build)
	idx, ok := ix.classify(acct)
	if !ok {
		idx = rootCount
	}
	return fmt.Sprintf("%d:%s", idx, acct)
}

// classify returns acct's account-type index per the ledger's name_* options,
// and whether its root is a recognized account type.
func (ix *Index) classify(acct ast.Account) (int, bool) {
	idx, ok := ix.rootIndex[string(acct.Root())]
	return idx, ok
}
