package fiscalincomeexpense

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

const (
	customTypeName    = "fiscal_income_expense"
	codeInvalidConfig = "fiscal-income-expense-invalid-config"
	codeParse         = "fiscal-income-expense-parse"
	codeMismatch      = "fiscal-income-expense-mismatch"
)

// half is the multiplier applied to the per-currency exponent scale to
// produce the inferred tolerance, matching beancount convention.
// apd.New(5, -1) constructs 0.5 exactly with no parsing.
var half = apd.New(5, -1)

func init() {
	postproc.Register("beansprout.plugins.fiscal_income_expense", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/sprout/fiscalincomeexpense", api.PluginFunc(apply))
}

func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// One materialization pass: each Custom needs to consult the full
	// directive sequence (date-filtered) to compute its actual balance.
	var all []ast.Directive
	for _, d := range in.Directives {
		all = append(all, d)
	}
	if len(all) == 0 {
		return api.Result{}, nil
	}

	out := make([]ast.Directive, 0, len(all))
	var diags []ast.Diagnostic

	for _, d := range all {
		if err := ctx.Err(); err != nil {
			return api.Result{}, err
		}
		cust, ok := d.(*ast.Custom)
		if !ok || cust.TypeName != customTypeName {
			out = append(out, d)
			continue
		}
		// Drop the Custom from the output; surface any diagnostics
		// produced while validating its assertion.
		diags = append(diags, validate(cust, all, in.Directive)...)
	}

	res := api.Result{Directives: out}
	if len(diags) != 0 {
		res.Diagnostics = diags
	}
	return res, nil
}

// fiscalCheck holds the inputs needed to compute the balance and emit a
// mismatch diagnostic.
type fiscalCheck struct {
	account   ast.Account
	begin     time.Time
	end       time.Time
	expected  apd.Decimal
	currency  string
	tolerance apd.Decimal
}

// validate parses cust's parameters and, on success, runs the balance
// check. The returned slice carries either configuration/parse
// diagnostics or, on success and on failure of the check, at most one
// mismatch diagnostic.
func validate(cust *ast.Custom, all []ast.Directive, plug *ast.Plugin) []ast.Diagnostic {
	span := spanFor(cust, plug)

	check, diags := parseCheck(cust, span)
	if diags != nil {
		return diags
	}

	actual := computeNetChange(all, check.account, check.begin, check.end, check.currency)

	diff := new(apd.Decimal)
	expected := check.expected
	if _, err := apd.BaseContext.Sub(diff, &actual, &expected); err != nil {
		return []ast.Diagnostic{{
			Code:     codeMismatch,
			Span:     span,
			Severity: ast.Error,
			Message:  fmt.Sprintf("fiscal_income_expense: arithmetic error subtracting expected from actual: %v", err),
		}}
	}
	absDiff := new(apd.Decimal)
	if _, err := apd.BaseContext.Abs(absDiff, diff); err != nil {
		return []ast.Diagnostic{{
			Code:     codeMismatch,
			Span:     span,
			Severity: ast.Error,
			Message:  fmt.Sprintf("fiscal_income_expense: arithmetic error taking |diff|: %v", err),
		}}
	}
	if absDiff.Cmp(&check.tolerance) > 0 {
		return []ast.Diagnostic{{
			Code:     codeMismatch,
			Span:     span,
			Severity: ast.Error,
			Message: fmt.Sprintf(
				"fiscal_income_expense: balance check failed for %s (%s to %s): expected %s %s (tolerance %s), got %s %s (difference %s)",
				check.account,
				check.begin.Format("2006-01-02"),
				check.end.Format("2006-01-02"),
				expected.String(), check.currency,
				check.tolerance.String(),
				actual.String(), check.currency,
				diff.String(),
			),
		}}
	}
	return nil
}

// parseCheck unpacks a Custom directive's Values into a fiscalCheck.
// Returns (zero, diagnostics) on any structural or parse error.
func parseCheck(cust *ast.Custom, span ast.Span) (fiscalCheck, []ast.Diagnostic) {
	end := cust.Date

	if len(cust.Values) < 2 || len(cust.Values) > 3 {
		return fiscalCheck{}, []ast.Diagnostic{{
			Code:     codeInvalidConfig,
			Span:     span,
			Severity: ast.Error,
			Message: fmt.Sprintf(
				"fiscal_income_expense requires 2 or 3 parameters (account, [begin_date,] expected), got %d",
				len(cust.Values),
			),
		}}
	}

	acctVal := cust.Values[0]
	if acctVal.Kind != ast.MetaAccount || acctVal.String == "" {
		return fiscalCheck{}, []ast.Diagnostic{{
			Code:     codeInvalidConfig,
			Span:     span,
			Severity: ast.Error,
			Message:  fmt.Sprintf("first parameter must be an account, got %s", acctVal.Kind),
		}}
	}
	account := ast.Account(acctVal.String)

	var begin time.Time
	var expectedVal ast.MetaValue
	if len(cust.Values) == 3 {
		dateVal := cust.Values[1]
		if dateVal.Kind != ast.MetaDate {
			return fiscalCheck{}, []ast.Diagnostic{{
				Code:     codeInvalidConfig,
				Span:     span,
				Severity: ast.Error,
				Message:  fmt.Sprintf("second parameter must be a date, got %s", dateVal.Kind),
			}}
		}
		begin = dateVal.Date
		expectedVal = cust.Values[2]
	} else {
		begin = time.Date(end.Year(), 1, 1, 0, 0, 0, 0, end.Location())
		expectedVal = cust.Values[1]
	}

	if begin.After(end) {
		return fiscalCheck{}, []ast.Diagnostic{{
			Code:     codeInvalidConfig,
			Span:     span,
			Severity: ast.Error,
			Message: fmt.Sprintf(
				"begin date %s must be on or before end date %s",
				begin.Format("2006-01-02"), end.Format("2006-01-02"),
			),
		}}
	}

	expected, currency, tolerance, ds := parseExpected(expectedVal, span)
	if ds != nil {
		return fiscalCheck{}, ds
	}
	return fiscalCheck{
		account:   account,
		begin:     begin,
		end:       end,
		expected:  expected,
		currency:  currency,
		tolerance: tolerance,
	}, nil
}

// parseExpected turns the expected-amount MetaValue into a decimal,
// currency, and tolerance. When the value is a typed [ast.MetaAmount]
// the tolerance is inferred from the decimal exponent. When it is a
// [ast.MetaString] the body is parsed via [ast.ParseBalanceAmount];
// tolerance comes from the explicit `~` clause if present, otherwise
// inferred the same way as the typed form.
func parseExpected(v ast.MetaValue, span ast.Span) (apd.Decimal, string, apd.Decimal, []ast.Diagnostic) {
	switch v.Kind {
	case ast.MetaAmount:
		num := v.Amount.Number
		return num, v.Amount.Currency, inferTolerance(&num), nil
	case ast.MetaString:
		amt, tol, parseDiags := ast.ParseBalanceAmount(strings.TrimSpace(v.String))
		if len(parseDiags) > 0 {
			var diags []ast.Diagnostic
			for _, pd := range parseDiags {
				diags = append(diags, ast.Diagnostic{
					Code:     codeParse,
					Span:     span,
					Severity: ast.Error,
					Message: fmt.Sprintf(
						"fiscal_income_expense: %s (%s) in %q",
						pd.Message, pd.Code, v.String,
					),
				})
			}
			return apd.Decimal{}, "", apd.Decimal{}, diags
		}
		num := amt.Number
		var tolerance apd.Decimal
		if tol != nil {
			tolerance = *tol
		} else {
			tolerance = inferTolerance(&num)
		}
		return num, amt.Currency, tolerance, nil
	default:
		return apd.Decimal{}, "", apd.Decimal{}, []ast.Diagnostic{{
			Code:     codeInvalidConfig,
			Span:     span,
			Severity: ast.Error,
			Message:  fmt.Sprintf("expected amount must be an amount or string, got %s", v.Kind),
		}}
	}
}

// inferTolerance returns the conventional beancount tolerance for a
// decimal of the given exponent: 10^exponent * 0.5 for negative
// exponents (sub-unit precision), 0.5 for whole numbers.
func inferTolerance(d *apd.Decimal) apd.Decimal {
	if d.Exponent >= 0 {
		return *half
	}
	// scale = 10^exponent, expressed exactly as a 1-coefficient decimal
	// at that exponent. multiply by 0.5 under BaseContext.
	scale := apd.New(1, d.Exponent)
	var out apd.Decimal
	if _, err := apd.BaseContext.Mul(&out, scale, half); err != nil {
		// Unreachable: multiplying 10^exp * 0.5 produces a finite
		// decimal within BaseContext's bounds for any realistic
		// monetary exponent.
		return *half
	}
	return out
}

// computeNetChange sums every Transaction posting against account or
// any sub-account, in the given currency, whose date falls in
// [begin, end] inclusive.
func computeNetChange(all []ast.Directive, account ast.Account, begin, end time.Time, currency string) apd.Decimal {
	var sum apd.Decimal
	for _, d := range all {
		tx, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		if tx.Date.Before(begin) || tx.Date.After(end) {
			continue
		}
		for i := range tx.Postings {
			p := &tx.Postings[i]
			if p.Amount == nil {
				continue
			}
			if p.Amount.Currency != currency {
				continue
			}
			if !account.Covers(p.Account) {
				continue
			}
			num := p.Amount.Number
			_, _ = apd.BaseContext.Add(&sum, &sum, &num)
		}
	}
	return sum
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
