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

	// invariant: seen[acct] = currencies observed for acct in directives
	// preceding the current iteration position, source order.
	seen := make(map[ast.Account]map[string]struct{})

	var out []ast.Directive
	var diags []ast.Diagnostic
	any := false

	for _, d := range in.Directives {
		if err := ctx.Err(); err != nil {
			return api.Result{}, err
		}
		any = true
		switch v := d.(type) {
		case *ast.Transaction:
			for i := range v.Postings {
				p := &v.Postings[i]
				if p.Amount == nil || p.Amount.Currency == "" {
					continue
				}
				markSeen(seen, p.Account, p.Amount.Currency)
			}
			out = append(out, d)
		case *ast.Balance:
			markSeen(seen, v.Account, v.Amount.Currency)
			out = append(out, d)
		case *ast.Custom:
			if v.TypeName != customTypeName {
				out = append(out, d)
				continue
			}
			balances, ds := processCustom(v, seen, in.Directive)
			diags = append(diags, ds...)
			for _, b := range balances {
				out = append(out, b)
			}
		default:
			out = append(out, d)
		}
	}
	if !any {
		return api.Result{}, nil
	}

	res := api.Result{Directives: out}
	if len(diags) != 0 {
		res.Diagnostics = diags
	}
	return res, nil
}

func markSeen(seen map[ast.Account]map[string]struct{}, account ast.Account, currency string) {
	if currency == "" {
		return
	}
	s, ok := seen[account]
	if !ok {
		s = make(map[string]struct{})
		seen[account] = s
	}
	s[currency] = struct{}{}
}

// processCustom validates a single comprehensive_balance directive and
// returns the synthesized Balance directives plus any diagnostics. The
// universe is the union of:
//   - currencies previously observed on the target account (seen[account])
//   - currencies declared in the Custom's body
//
// For each currency in the universe, exactly one *ast.Balance is emitted
// at the Custom's date. Declared currencies carry the user's amount and
// tolerance; non-declared currencies carry amount = 0 with nil tolerance
// so the downstream balance plugin asserts they are absent.
func processCustom(
	cust *ast.Custom,
	seen map[ast.Account]map[string]struct{},
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

	listed := make(map[string]*assertion, len(assertions))
	for i := range assertions {
		listed[assertions[i].currency] = &assertions[i]
	}

	// universe = seen[account] ∪ listed
	universe := make(map[string]struct{}, len(seen[account])+len(listed))
	for cur := range seen[account] {
		universe[cur] = struct{}{}
	}
	for cur := range listed {
		universe[cur] = struct{}{}
	}

	currencies := make([]string, 0, len(universe))
	for cur := range universe {
		currencies = append(currencies, cur)
	}
	sort.Strings(currencies)

	emitted := make([]*ast.Balance, 0, len(currencies))
	for _, cur := range currencies {
		b := &ast.Balance{
			Span:    cust.Span,
			Date:    cust.Date,
			Account: account,
			Meta:    cust.Meta,
		}
		if a, ok := listed[cur]; ok {
			b.Amount = ast.Amount{Number: a.number, Currency: cur}
			if a.tolerance != nil {
				b.Tolerance = a.tolerance
			}
		} else {
			var zero apd.Decimal
			b.Amount = ast.Amount{Number: zero, Currency: cur}
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
