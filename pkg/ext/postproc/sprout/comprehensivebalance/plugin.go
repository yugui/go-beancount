package comprehensivebalance

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

const (
	customTypeName        = "comprehensive_balance"
	codeInvalidConfig     = "comprehensive-balance-invalid-config"
	codeDuplicateCurrency = "comprehensive-balance-duplicate-currency"
	codeParse             = "comprehensive-balance-parse"
)

func init() {
	postproc.Register("beansprout.plugins.comprehensive_balance", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/sprout/comprehensivebalance", api.PluginFunc(apply))
}

// assertion is one parsed body line: a currency, its number, and an
// optional tolerance.
type assertion struct {
	currency  string
	number    apd.Decimal
	tolerance *apd.Decimal
}

func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// Materialize so we can compute balances at each directive's date in
	// a single forward pass while emitting the new directive slice.
	var all []ast.Directive
	for _, d := range in.Directives {
		all = append(all, d)
	}
	if len(all) == 0 {
		return api.Result{}, nil
	}

	// balances[acct][cur] accumulates the running sum of postings
	// against acct in cur, processed in source order. This mirrors the
	// upstream realization pass: the assertion for a Custom is checked
	// against the balance after all entries up to and including
	// directives sharing its date.
	balances := make(map[ast.Account]map[string]*apd.Decimal)

	out := make([]ast.Directive, 0, len(all))
	var diags []ast.Diagnostic

	for _, d := range all {
		if err := ctx.Err(); err != nil {
			return api.Result{}, err
		}
		cust, ok := d.(*ast.Custom)
		if !ok || cust.TypeName != customTypeName {
			out = append(out, d)
			accumulate(balances, d)
			continue
		}

		balanceEntries, ds := processCustom(cust, balances, in.Directive)
		diags = append(diags, ds...)
		for _, b := range balanceEntries {
			out = append(out, b)
		}
	}

	res := api.Result{Directives: out}
	if len(diags) != 0 {
		res.Diagnostics = diags
	}
	return res, nil
}

// processCustom validates a single comprehensive_balance directive and
// returns the synthesized Balance directives plus any diagnostics. The
// balance map is read-only here; postings inside Custom directives are
// not folded into the running sum because Custom carries no postings.
func processCustom(
	cust *ast.Custom,
	balances map[ast.Account]map[string]*apd.Decimal,
	plug *ast.Plugin,
) ([]*ast.Balance, []ast.Diagnostic) {
	span := spanFor(cust, plug)

	if len(cust.Values) != 2 {
		return nil, []ast.Diagnostic{{
			Code:     codeInvalidConfig,
			Span:     span,
			Severity: ast.Error,
			Message: fmt.Sprintf(
				"comprehensive_balance requires exactly 2 parameters (account, body), got %d",
				len(cust.Values),
			),
		}}
	}
	acctVal := cust.Values[0]
	bodyVal := cust.Values[1]
	if acctVal.Kind != ast.MetaAccount || acctVal.String == "" {
		return nil, []ast.Diagnostic{{
			Code:     codeInvalidConfig,
			Span:     span,
			Severity: ast.Error,
			Message:  fmt.Sprintf("first parameter must be an account, got %s", acctVal.Kind),
		}}
	}
	if bodyVal.Kind != ast.MetaString {
		return nil, []ast.Diagnostic{{
			Code:     codeInvalidConfig,
			Span:     span,
			Severity: ast.Error,
			Message:  fmt.Sprintf("second parameter must be a string, got %s", bodyVal.Kind),
		}}
	}
	account := ast.Account(acctVal.String)

	assertions, diags := parseAssertions(bodyVal.String, cust, plug)
	if len(diags) > 0 {
		return nil, diags
	}

	declared := make(map[string]struct{}, len(assertions))
	for _, a := range assertions {
		declared[a.currency] = struct{}{}
	}

	var emitted []*ast.Balance

	// Zero-balance assertions for unlisted commodities with non-zero
	// balance. Sorted for deterministic output by currency.
	if held, ok := balances[account]; ok {
		extras := unlistedCurrencies(held, declared)
		for _, cur := range extras {
			var zero apd.Decimal
			emitted = append(emitted, &ast.Balance{
				Span:    cust.Span,
				Date:    cust.Date,
				Account: account,
				Amount:  ast.Amount{Number: zero, Currency: cur},
				Meta:    cust.Meta,
			})
		}
	}

	for _, a := range assertions {
		b := &ast.Balance{
			Span:    cust.Span,
			Date:    cust.Date,
			Account: account,
			Amount:  ast.Amount{Number: a.number, Currency: a.currency},
			Meta:    cust.Meta,
		}
		if a.tolerance != nil {
			b.Tolerance = a.tolerance
		}
		emitted = append(emitted, b)
	}
	return emitted, nil
}

// parseAssertions splits the multi-line body of a comprehensive_balance
// directive into assertions. Empty lines and lines beginning with ';'
// are ignored. Each remaining line is parsed via
// [ast.ParseBalanceAmount]; the returned diagnostics carry byte offsets
// relative to the line and are rebased onto the enclosing Custom's
// Span.
func parseAssertions(body string, cust *ast.Custom, plug *ast.Plugin) ([]assertion, []ast.Diagnostic) {
	span := spanFor(cust, plug)

	var assertions []assertion
	seen := make(map[string]struct{})
	var diags []ast.Diagnostic

	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		// Strip inline comments so ParseBalanceAmount sees only the
		// amount expression.
		expr := line
		if i := strings.IndexByte(expr, ';'); i >= 0 {
			expr = strings.TrimSpace(expr[:i])
		}

		amt, tol, parseDiags := ast.ParseBalanceAmount(expr)
		if len(parseDiags) > 0 {
			for _, pd := range parseDiags {
				diags = append(diags, ast.Diagnostic{
					Code:     codeParse,
					Span:     span,
					Severity: ast.Error,
					Message: fmt.Sprintf(
						"comprehensive_balance: %s (%s) in %q",
						pd.Message, pd.Code, expr,
					),
				})
			}
			continue
		}
		if _, dup := seen[amt.Currency]; dup {
			diags = append(diags, ast.Diagnostic{
				Code:     codeDuplicateCurrency,
				Span:     span,
				Severity: ast.Error,
				Message:  fmt.Sprintf("duplicate currency %q in comprehensive_balance body", amt.Currency),
			})
			continue
		}
		seen[amt.Currency] = struct{}{}
		assertions = append(assertions, assertion{
			currency:  amt.Currency,
			number:    amt.Number,
			tolerance: tol,
		})
	}
	return assertions, diags
}

// accumulate folds a single directive's postings into balances. Only
// [ast.Transaction] contributes; other directives do not move balances.
// Each posting's amount, when non-nil and with non-empty currency, is
// added to balances[Account][Currency] under [apd.BaseContext].
func accumulate(balances map[ast.Account]map[string]*apd.Decimal, d ast.Directive) {
	tx, ok := d.(*ast.Transaction)
	if !ok {
		return
	}
	for i := range tx.Postings {
		p := &tx.Postings[i]
		if p.Amount == nil || p.Amount.Currency == "" {
			continue
		}
		perAcct, ok := balances[p.Account]
		if !ok {
			perAcct = make(map[string]*apd.Decimal)
			balances[p.Account] = perAcct
		}
		sum, ok := perAcct[p.Amount.Currency]
		if !ok {
			sum = new(apd.Decimal)
			perAcct[p.Amount.Currency] = sum
		}
		num := p.Amount.Number
		_, _ = apd.BaseContext.Add(sum, sum, &num)
	}
}

// unlistedCurrencies returns the sorted list of currencies in held that
// are absent from declared and whose balance is non-zero.
func unlistedCurrencies(held map[string]*apd.Decimal, declared map[string]struct{}) []string {
	var out []string
	for cur, num := range held {
		if _, ok := declared[cur]; ok {
			continue
		}
		if num.Sign() == 0 {
			continue
		}
		out = append(out, cur)
	}
	// Insertion order is non-deterministic across map iterations; sort
	// for stable output.
	sort.Strings(out)
	return out
}

// spanFor returns the Custom's own span, falling back to the triggering
// plugin directive's span when the Custom's is zero.
func spanFor(cust *ast.Custom, plug *ast.Plugin) ast.Span {
	var zero ast.Span
	if cust.Span != zero {
		return cust.Span
	}
	if plug != nil {
		return plug.Span
	}
	return zero
}
