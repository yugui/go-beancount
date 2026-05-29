package table

import (
	"iter"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// pricePrecision matches pkg/inventory's quo context (IEEE-754 decimal128)
// so a per-unit price derived from a `@@` total here agrees with values the
// booking layer derives. BaseContext has precision 0 and cannot divide.
var pricePrecision = apd.BaseContext.WithPrecision(34)

// postingRow is the handle the postings table yields: a transaction and an
// index into its Postings. It is a struct rather than a bare tuple so the
// deferred `balance` column can be added by enriching this handle with a
// running-inventory field (computed by the Rows producer via
// [inventory.Reducer].Walk) plus a `balance` Column reading it — without
// changing [Table] or [Column].
type postingRow struct {
	txn *ast.Transaction
	idx int
}

func (r postingRow) posting() *ast.Posting { return &r.txn.Postings[r.idx] }

// Postings returns the default virtual table: one row per posting of every
// transaction in l, in the ledger's canonical order; non-transaction
// directives are skipped. The returned table is immutable and safe for
// concurrent read (see the package doc); it holds l by reference and never
// mutates it.
func Postings(l *ast.Ledger) *Table {
	return &Table{
		Name:    "postings",
		Columns: postingColumns,
		Rows: func() iter.Seq[Row] {
			return func(yield func(Row) bool) {
				for _, d := range l.All() {
					txn, ok := d.(*ast.Transaction)
					if !ok {
						continue
					}
					for i := range txn.Postings {
						if !yield(postingRow{txn: txn, idx: i}) {
							return
						}
					}
				}
			}
		},
	}
}

func postingCol(name string, t types.Type, fn func(postingRow) types.Value) Column {
	return Column{
		Name: name,
		Type: t,
		Accessor: func(r Row) types.Value {
			return fn(r.(postingRow))
		},
	}
}

var postingColumns = []Column{
	postingCol("type", types.String, func(postingRow) types.Value {
		return types.NewString("transaction")
	}),
	postingCol("date", types.Date, func(r postingRow) types.Value {
		return types.NewDate(r.txn.Date)
	}),
	postingCol("year", types.Int, func(r postingRow) types.Value {
		return types.NewInt(int64(r.txn.Date.Year()))
	}),
	postingCol("month", types.Int, func(r postingRow) types.Value {
		return types.NewInt(int64(r.txn.Date.Month()))
	}),
	postingCol("day", types.Int, func(r postingRow) types.Value {
		return types.NewInt(int64(r.txn.Date.Day()))
	}),
	postingCol("filename", types.String, func(r postingRow) types.Value {
		return spanFilename(r.posting().Span)
	}),
	postingCol("lineno", types.Int, func(r postingRow) types.Value {
		return spanLineno(r.posting().Span)
	}),
	postingCol("flag", types.String, func(r postingRow) types.Value {
		if f := r.posting().Flag; f != 0 {
			return flagString(f)
		}
		return flagString(r.txn.Flag)
	}),
	postingCol("payee", types.String, func(r postingRow) types.Value {
		return nullableString(r.txn.Payee)
	}),
	postingCol("narration", types.String, func(r postingRow) types.Value {
		return types.NewString(r.txn.Narration)
	}),
	postingCol("tags", types.SetType, func(r postingRow) types.Value {
		return types.NewSet(r.txn.Tags...)
	}),
	postingCol("links", types.SetType, func(r postingRow) types.Value {
		return types.NewSet(r.txn.Links...)
	}),
	postingCol("account", types.String, func(r postingRow) types.Value {
		return types.NewString(string(r.posting().Account))
	}),
	postingCol("number", types.Decimal, func(r postingRow) types.Value {
		if a := r.posting().Amount; a != nil {
			return types.NewDecimal(a.Number)
		}
		return types.Null(types.Decimal)
	}),
	postingCol("currency", types.String, func(r postingRow) types.Value {
		if a := r.posting().Amount; a != nil {
			return types.NewString(a.Currency)
		}
		return types.Null(types.String)
	}),
	postingCol("cost_number", types.Decimal, func(r postingRow) types.Value {
		p := r.posting()
		if p.Cost == nil {
			return types.Null(types.Decimal)
		}
		per, err := ast.PerUnitCost(p.Cost, p.Amount)
		if err != nil || per == nil {
			return types.Null(types.Decimal)
		}
		return types.NewDecimal(per.Number)
	}),
	postingCol("cost_currency", types.String, func(r postingRow) types.Value {
		if c := r.posting().Cost; c != nil {
			if cur := c.GetCurrency(); cur != "" {
				return types.NewString(cur)
			}
		}
		return types.Null(types.String)
	}),
	postingCol("cost_date", types.Date, func(r postingRow) types.Value {
		if c := r.posting().Cost; c != nil {
			if d, ok := c.GetDate(); ok {
				return types.NewDate(d)
			}
		}
		return types.Null(types.Date)
	}),
	postingCol("cost_label", types.String, func(r postingRow) types.Value {
		if c := r.posting().Cost; c != nil {
			return nullableString(c.GetLabel())
		}
		return types.Null(types.String)
	}),
	postingCol("position", types.Position, func(r postingRow) types.Value {
		p := r.posting()
		if p.Amount == nil {
			return types.Null(types.Position)
		}
		var lot *inventory.Lot
		if p.Cost != nil && p.Cost.IsBooked() {
			lot = inventory.LotFromCost(p.Cost.(*ast.Cost))
		}
		return types.NewPosition(inventory.Position{Units: *p.Amount, Cost: lot})
	}),
	postingCol("weight", types.Amount, func(r postingRow) types.Value {
		w, err := inventory.PostingWeight(r.posting())
		if err != nil || w == nil {
			return types.Null(types.Amount)
		}
		return types.NewAmount(*w)
	}),
	postingCol("price", types.Amount, func(r postingRow) types.Value {
		return perUnitPrice(r.posting())
	}),
	// meta is the POSTING's own metadata only — deliberately NOT merged
	// with the parent transaction's. Always a Dict, possibly empty (never
	// NULL); getitem handles missing keys.
	postingCol("meta", types.DictType, func(r postingRow) types.Value {
		return metaDict(r.posting().Meta)
	}),
}

// perUnitPrice returns the per-unit price from a posting's annotation, or a
// typed NULL when there is none. For a total (`@@`) annotation it divides the
// total by the absolute number of units (NULL if units are zero or absent),
// keeping the price currency.
func perUnitPrice(p *ast.Posting) types.Value {
	pr := p.Price
	if pr == nil {
		return types.Null(types.Amount)
	}
	if !pr.IsTotal {
		return types.NewAmount(pr.Amount)
	}
	if p.Amount == nil {
		return types.Null(types.Amount)
	}
	absUnits := new(apd.Decimal)
	if _, err := apd.BaseContext.Abs(absUnits, &p.Amount.Number); err != nil {
		return types.Null(types.Amount)
	}
	if absUnits.Sign() == 0 {
		return types.Null(types.Amount)
	}
	per := new(apd.Decimal)
	if _, err := pricePrecision.Quo(per, &pr.Amount.Number, absUnits); err != nil {
		return types.Null(types.Amount)
	}
	per.Reduce(per) // drop padding zeros from the division
	return types.NewAmount(ast.Amount{Number: *per, Currency: pr.Amount.Currency})
}
