package query_test

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// The lean engine ships no built-in functions (that is Step 6); these tests
// register the few real-named functions they exercise once at package init.
// The registry is global and panics on a duplicate signature, so each
// overload is registered exactly once here.
func init() {
	env.Register(api.Function{
		Name:       "count",
		In:         []types.Type{types.String},
		Out:        types.Int,
		Flavor:     api.AggregatorFlavor,
		Aggregator: func() api.Accumulator { return &countAcc{} },
	})
	env.Register(api.Function{
		Name:       "count",
		In:         []types.Type{types.Decimal},
		Out:        types.Int,
		Flavor:     api.AggregatorFlavor,
		Aggregator: func() api.Accumulator { return &countAcc{} },
	})
	env.Register(api.Function{
		Name:       "sum",
		In:         []types.Type{types.Int},
		Out:        types.Int,
		Flavor:     api.AggregatorFlavor,
		Aggregator: func() api.Accumulator { return &sumIntAcc{} },
	})
	env.Register(api.Function{
		Name:       "sum",
		In:         []types.Type{types.Decimal},
		Out:        types.Decimal,
		Flavor:     api.AggregatorFlavor,
		Aggregator: func() api.Accumulator { return &sumDecimalAcc{sum: apd.New(0, 0)} },
	})
	env.Register(api.Function{
		Name:       "sum",
		In:         []types.Type{types.Position},
		Out:        types.Inventory,
		Flavor:     api.AggregatorFlavor,
		Aggregator: func() api.Accumulator { return &sumPositionAcc{inv: inventory.NewInventory()} },
	})
	env.Register(api.Function{
		Name:   "getitem",
		In:     []types.Type{types.DictType, types.String},
		Out:    types.String,
		Flavor: api.ScalarFlavor,
		Scalar: api.Pure(getitemScalar),
	})
	env.Register(api.Function{
		Name:   "getitem",
		In:     []types.Type{types.DictType, types.String, types.String},
		Out:    types.String,
		Flavor: api.ScalarFlavor,
		Scalar: api.Pure(getitemScalar),
	})
}

type countAcc struct{ n int64 }

func (a *countAcc) Add(args []types.Value) error {
	if !args[0].IsNull() {
		a.n++
	}
	return nil
}
func (a *countAcc) Merge(o api.Accumulator) error { a.n += o.(*countAcc).n; return nil }
func (a *countAcc) Result() (types.Value, error)  { return types.NewInt(a.n), nil }

type sumIntAcc struct{ sum int64 }

func (a *sumIntAcc) Add(args []types.Value) error {
	if n, ok := types.AsInt(args[0]); ok {
		a.sum += n
	}
	return nil
}
func (a *sumIntAcc) Merge(o api.Accumulator) error { a.sum += o.(*sumIntAcc).sum; return nil }
func (a *sumIntAcc) Result() (types.Value, error)  { return types.NewInt(a.sum), nil }

type sumDecimalAcc struct{ sum *apd.Decimal }

func (a *sumDecimalAcc) Add(args []types.Value) error {
	if d, ok := types.AsDecimal(args[0]); ok {
		_, err := apd.BaseContext.Add(a.sum, a.sum, &d)
		return err
	}
	return nil
}
func (a *sumDecimalAcc) Merge(o api.Accumulator) error {
	_, err := apd.BaseContext.Add(a.sum, a.sum, o.(*sumDecimalAcc).sum)
	return err
}
func (a *sumDecimalAcc) Result() (types.Value, error) { return types.NewDecimal(*a.sum), nil }

type sumPositionAcc struct{ inv *inventory.Inventory }

func (a *sumPositionAcc) Add(args []types.Value) error {
	p, ok := types.AsPosition(args[0])
	if !ok {
		return nil
	}
	return a.inv.Add(p)
}
func (a *sumPositionAcc) Merge(o api.Accumulator) error {
	for p := range o.(*sumPositionAcc).inv.All() {
		if err := a.inv.Add(p); err != nil {
			return err
		}
	}
	return nil
}
func (a *sumPositionAcc) Result() (types.Value, error) { return types.NewInventory(a.inv), nil }

// getitemScalar reads a key from a dict; a missing key or NULL dict yields a
// typed NULL string. The optional third argument is returned when the dict is
// present but the key is absent.
func getitemScalar(args []types.Value) (types.Value, error) {
	d, ok := types.AsDict(args[0])
	if !ok {
		return types.Null(types.String), nil
	}
	key, ok := types.AsString(args[1])
	if !ok {
		return types.Null(types.String), nil
	}
	if v, found := d.Get(key); found {
		return v, nil
	}
	if len(args) == 3 {
		return args[2], nil
	}
	return types.Null(types.String), nil
}

func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("apd.NewFromString(%q): %v", s, err)
	}
	return *d
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// sampleLedger builds a small ledger of three transactions with postings
// across two accounts, tags, links, and posting metadata, used by most
// tests.
func sampleLedger(t *testing.T) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Transaction{
			Date:      date(2020, 1, 15),
			Flag:      '*',
			Payee:     "Grocery",
			Narration: "weekly shop",
			Tags:      []string{"food", "weekly"},
			Links:     []string{"r1"},
			Postings: []ast.Posting{
				{
					Account: "Expenses:Food",
					Amount:  &ast.Amount{Number: dec(t, "20"), Currency: "USD"},
					Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"category": {Kind: ast.MetaString, String: "groceries"}}},
				},
				{
					Account: "Assets:Cash",
					Amount:  &ast.Amount{Number: dec(t, "-20"), Currency: "USD"},
				},
			},
		},
		&ast.Transaction{
			Date:      date(2021, 6, 1),
			Flag:      '*',
			Payee:     "Cafe",
			Narration: "coffee",
			Tags:      []string{"food"},
			Postings: []ast.Posting{
				{
					Account: "Expenses:Food",
					Amount:  &ast.Amount{Number: dec(t, "5"), Currency: "USD"},
				},
				{
					Account: "Assets:Cash",
					Amount:  &ast.Amount{Number: dec(t, "-5"), Currency: "USD"},
				},
			},
		},
		&ast.Transaction{
			Date:      date(2022, 3, 10),
			Flag:      '*',
			Payee:     "Salary",
			Narration: "monthly pay",
			Postings: []ast.Posting{
				{
					Account: "Assets:Cash",
					Amount:  &ast.Amount{Number: dec(t, "100"), Currency: "USD"},
				},
				{
					Account: "Income:Salary",
					Amount:  &ast.Amount{Number: dec(t, "-100"), Currency: "USD"},
				},
			},
		},
	})
	return l
}

func emptyLedger() *ast.Ledger { return &ast.Ledger{} }
