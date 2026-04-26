package checkdrained

import (
	"context"
	"sort"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// oneDay is the offset applied to a Close directive's date to compute
// the synthesized Balance's date. Upstream uses datetime.timedelta(days=1).
const oneDay = 24 * time.Hour

func init() {
	postproc.Register("beancount.plugins.check_drained", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/checkdrained", api.PluginFunc(apply))
}

// apply synthesizes zero-balance assertions one day after every
// close of a balance-sheet account, covering each currency the
// account has held. See the package godoc for the full behavior and
// upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// currencies tracks every currency observed per account. balances
	// tracks the (date, currency) tuples where the ledger already has
	// an explicit Balance assertion, so we do not emit a duplicate.
	currencies := map[ast.Account]map[string]struct{}{}
	balances := map[ast.Account]map[balanceKey]struct{}{}

	// Materialize once so we can re-walk to stream out directives in
	// the original order; api.Input.Directives guarantees re-runnable
	// iteration.
	var all []ast.Directive
	for _, d := range in.Directives {
		all = append(all, d)
		switch x := d.(type) {
		case *ast.Transaction:
			for i := range x.Postings {
				p := &x.Postings[i]
				if p.Amount == nil || !isBalanceSheet(p.Account) {
					continue
				}
				addCurrency(currencies, p.Account, p.Amount.Currency)
			}
		case *ast.Open:
			if !isBalanceSheet(x.Account) {
				continue
			}
			for _, c := range x.Currencies {
				addCurrency(currencies, x.Account, c)
			}
		case *ast.Balance:
			if !isBalanceSheet(x.Account) {
				continue
			}
			addBalance(balances, x.Account, x.Date, x.Amount.Currency)
		}
	}

	out := make([]ast.Directive, 0, len(all))
	for _, d := range all {
		if closed, ok := d.(*ast.Close); ok && isBalanceSheet(closed.Account) {
			out = append(out, synthesizeBalances(closed, currencies, balances)...)
		}
		out = append(out, d)
	}

	return api.Result{Directives: out}, nil
}

// balanceKey identifies an explicit (date, currency) Balance assertion
// already present on a given account.
type balanceKey struct {
	date     time.Time
	currency string
}

// isBalanceSheet reports whether a is an account under one of the
// hardcoded balance-sheet roots (Assets, Liabilities, Equity). See the
// package godoc for the rationale.
func isBalanceSheet(a ast.Account) bool {
	switch a.Root() {
	case ast.Assets, ast.Liabilities, ast.Equity:
		return true
	}
	return false
}

// addCurrency records that account a has been observed holding
// currency cur.
func addCurrency(m map[ast.Account]map[string]struct{}, a ast.Account, cur string) {
	s, ok := m[a]
	if !ok {
		s = map[string]struct{}{}
		m[a] = s
	}
	s[cur] = struct{}{}
}

// addBalance records that an explicit Balance directive exists for
// (account, date, currency).
func addBalance(m map[ast.Account]map[balanceKey]struct{}, a ast.Account, date time.Time, cur string) {
	s, ok := m[a]
	if !ok {
		s = map[balanceKey]struct{}{}
		m[a] = s
	}
	s[balanceKey{date: date, currency: cur}] = struct{}{}
}

// synthesizeBalances returns one zero-valued Balance per currency seen
// on closed.Account, skipping those already covered by a user-authored
// assertion on the same date. Output order is deterministic
// (alphabetical by currency) so test expectations and user-visible
// diffs are stable. existingBalances is the (account, date,
// currency) registry of user-authored Balance directives gathered
// during the first pass.
func synthesizeBalances(closed *ast.Close, currencies map[ast.Account]map[string]struct{}, existingBalances map[ast.Account]map[balanceKey]struct{}) []ast.Directive {
	curs := currencies[closed.Account]
	if len(curs) == 0 {
		return nil
	}
	sortedCurs := make([]string, 0, len(curs))
	for c := range curs {
		sortedCurs = append(sortedCurs, c)
	}
	sort.Strings(sortedCurs)

	existing := existingBalances[closed.Account]
	balDate := closed.Date.Add(oneDay)
	out := make([]ast.Directive, 0, len(sortedCurs))
	for _, cur := range sortedCurs {
		// Upstream's duplicate-skip check uses entry.date (the close
		// date), not entry.date + 1. We preserve that behavior
		// verbatim so ledgers that already balance on close-day still
		// suppress the synthesized one.
		if _, ok := existing[balanceKey{date: closed.Date, currency: cur}]; ok {
			continue
		}
		out = append(out, &ast.Balance{
			Date:    balDate,
			Account: closed.Account,
			Amount:  ast.Amount{Number: zeroDecimal(), Currency: cur},
			// Reuse the Close directive's metadata and span so that
			// balance-mismatch diagnostics point back at the close
			// line — matching upstream's comment: "We use the close
			// directive's meta so that balance errors direct the
			// user to the corresponding close directive."
			Span: closed.Span,
			Meta: closed.Meta,
		})
	}
	return out
}

// zeroDecimal returns a freshly allocated apd.Decimal representing 0.
// Each synthesized balance gets its own copy so downstream consumers
// can mutate one without aliasing another.
func zeroDecimal() apd.Decimal {
	var d apd.Decimal
	d.SetInt64(0)
	return d
}
