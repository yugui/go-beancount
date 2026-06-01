package std_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// halfunits is a test-only Position→Amount scalar (units number halved) used to
// prove that the AT modifier of JOURNAL/BALANCES resolves through the normal
// function registry rather than a hardcoded cost/units/value set. Registered
// once at init since env.Register panics on a duplicate signature.
func init() {
	env.Register(api.Function{
		Name:   "halfunits",
		In:     []types.Type{types.Position},
		Out:    types.Amount,
		Flavor: api.ScalarFlavor,
		Scalar: api.Pure(func(args []types.Value) (types.Value, error) {
			p, ok := types.AsPosition(args[0])
			if !ok {
				return types.Null(types.Amount), nil
			}
			var half apd.Decimal
			if _, err := apd.BaseContext.WithPrecision(34).Quo(&half, &p.Units.Number, apd.New(2, 0)); err != nil {
				return nil, err
			}
			return types.NewAmount(ast.Amount{Number: half, Currency: p.Units.Currency}), nil
		}),
	})
}

// TestBalancesAtCustomFunction confirms AT applies an arbitrary registered
// Position→Amount function: BALANCES AT halfunits desugars to
// SUM(halfunits(position)), halving each account's units.
func TestBalancesAtCustomFunction(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l, "BALANCES AT halfunits")

	if len(res.Rows) != 2 {
		t.Fatalf("BALANCES AT halfunits produced %d account rows, want 2", len(res.Rows))
	}
	acct := column(t, res, "account")
	sum := len(res.Rows[0]) - 1
	if got := res.Rows[0][acct].Format(); got != "Assets:Cash" {
		t.Fatalf("first account = %q, want Assets:Cash (sorted)", got)
	}
	if got := res.Rows[0][sum].Type(); got != types.Inventory {
		t.Errorf("sum type = %s, want Inventory", got)
	}
	// Assets:Cash holds +1000 USD; halved and summed it must be 500 USD.
	if got := res.Rows[0][sum].Format(); !strings.Contains(got, "500") {
		t.Errorf("Assets:Cash half-units sum = %q, want to contain 500", got)
	}
}

// TestNestedAggregateThroughIsNull guards that the nested-aggregate check sees
// through an IS NULL wrapper: count(sum(x) IS NULL) must be rejected.
func TestNestedAggregateThroughIsNull(t *testing.T) {
	l := directiveLedger(t)
	_, err := query.Query(context.Background(),
		"SELECT count(sum(number) IS NULL) FROM postings", l)
	if err == nil {
		t.Fatal("nested aggregate through IS NULL was accepted, want error")
	}
}

func TestBetweenFilter(t *testing.T) {
	l := directiveLedger(t)

	in := mustQuery(t, l, "SELECT account WHERE number BETWEEN 0 AND 2000")
	if len(in.Rows) != 1 {
		t.Fatalf("BETWEEN matched %d rows, want 1 (only the +1000 posting)", len(in.Rows))
	}
	if got := in.Rows[0][0].Format(); got != "Assets:Cash" {
		t.Errorf("BETWEEN row = %q, want Assets:Cash", got)
	}

	out := mustQuery(t, l, "SELECT account WHERE number NOT BETWEEN 0 AND 2000")
	if len(out.Rows) != 1 {
		t.Fatalf("NOT BETWEEN matched %d rows, want 1 (the -1000 posting)", len(out.Rows))
	}
	if got := out.Rows[0][0].Format(); got != "Income:Salary" {
		t.Errorf("NOT BETWEEN row = %q, want Income:Salary", got)
	}
}

func TestIsNull(t *testing.T) {
	l := scalarLedger(t)

	isNull := mustQuery(t, l, "SELECT account WHERE cost_number IS NULL")
	if len(isNull.Rows) != 1 || isNull.Rows[0][0].Format() != "Assets:Cash" {
		t.Errorf("cost_number IS NULL = %v, want only Assets:Cash", isNull.Rows)
	}

	isNotNull := mustQuery(t, l, "SELECT account WHERE cost_number IS NOT NULL")
	if len(isNotNull.Rows) != 1 || isNotNull.Rows[0][0].Format() != "Assets:Brokerage:AAPL" {
		t.Errorf("cost_number IS NOT NULL = %v, want only Assets:Brokerage:AAPL", isNotNull.Rows)
	}
}

func TestNotIn(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l, "SELECT account WHERE account NOT IN ('Assets:Cash', 'Expenses:Other')")
	if len(res.Rows) != 1 || res.Rows[0][0].Format() != "Income:Salary" {
		t.Errorf("NOT IN = %v, want only Income:Salary", res.Rows)
	}
}

func TestJournalStatement(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l, "JOURNAL")

	wantCols := []string{"date", "flag", "payee", "narration", "account", "position", "balance"}
	if len(res.Columns) != len(wantCols) {
		t.Fatalf("JOURNAL produced %d columns, want %d", len(res.Columns), len(wantCols))
	}
	if len(res.Rows) != 2 {
		t.Errorf("JOURNAL produced %d rows, want 2 postings", len(res.Rows))
	}
	// The running-balance column must be an inventory.
	bal := res.Rows[len(res.Rows)-1][column(t, res, "balance")]
	if bal.Type() != types.Inventory {
		t.Errorf("JOURNAL balance column type = %s, want Inventory", bal.Type())
	}
}

func TestJournalAccountFilter(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l, `JOURNAL "Income"`)
	if len(res.Rows) != 1 {
		t.Fatalf("JOURNAL with account regex produced %d rows, want 1", len(res.Rows))
	}
	if got := res.Rows[0][column(t, res, "account")].Format(); got != "Income:Salary" {
		t.Errorf("JOURNAL account = %q, want Income:Salary", got)
	}
}

func TestJournalAtCost(t *testing.T) {
	l := scalarLedger(t)
	// AT cost wraps both position and balance; this must compile and run.
	res := mustQuery(t, l, "JOURNAL AT cost")
	if len(res.Rows) != 2 {
		t.Errorf("JOURNAL AT cost produced %d rows, want 2 postings", len(res.Rows))
	}
}

func TestBalancesStatement(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l, "BALANCES")

	if len(res.Rows) != 2 {
		t.Fatalf("BALANCES produced %d account rows, want 2", len(res.Rows))
	}
	acct := column(t, res, "account")
	// ACCOUNT_SORTKEY orders Assets before Income.
	if first := res.Rows[0][acct].Format(); first != "Assets:Cash" {
		t.Errorf("BALANCES first account = %q, want Assets:Cash (sorted)", first)
	}
	if second := res.Rows[1][acct].Format(); second != "Income:Salary" {
		t.Errorf("BALANCES second account = %q, want Income:Salary", second)
	}
}

func TestBalancesAtCostUsesSumAmount(t *testing.T) {
	l := scalarLedger(t)
	// SUM(cost(position)) sums Amount values, exercising the sum(Amount) overload.
	res := mustQuery(t, l, "BALANCES AT cost")
	if len(res.Rows) == 0 {
		t.Fatal("BALANCES AT cost produced no rows")
	}
	if last := res.Rows[len(res.Rows)-1]; last[len(last)-1].Type() != types.Inventory {
		t.Errorf("BALANCES AT cost sum type = %s, want Inventory", last[len(last)-1].Type())
	}
}
