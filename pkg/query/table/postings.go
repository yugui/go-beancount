package table

import (
	"iter"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/metaval"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// pricePrecision matches pkg/inventory's quo context (IEEE-754 decimal128)
// so a per-unit price derived from a `@@` total here agrees with values the
// booking layer derives. BaseContext has precision 0 and cannot divide.
var pricePrecision = apd.BaseContext.WithPrecision(34)

// postingRow is the handle the postings table yields: a transaction and an
// index into its Postings.
type postingRow struct {
	txn *ast.Transaction
	idx int
}

func (r postingRow) posting() *ast.Posting { return &r.txn.Postings[r.idx] }

// RunningBalanceColumn is the name of the postings column whose value is the
// cumulative inventory of the rows a query selects. Its value is supplied by
// the executor over the predicate-passing rows in scan order (see
// pkg/query/exec), not by the table accessor; a direct table read has no
// running balance.
const RunningBalanceColumn = "balance"

// PostingsOver returns a virtual table with the given name: one row per posting
// of every transaction yielded by all, in the sequence order that all produces;
// non-transaction directives are skipped. all is called once per [Table.Rows]
// invocation, producing a fresh iterator each time. The returned table is
// immutable and safe for concurrent read (see the package doc).
//
// Use this constructor when the directive source is a scoped view (e.g. a
// [pkg/query/scope.View] result) rather than the full ledger. Callers that hold
// an [*ast.Ledger] should use [Postings].
func PostingsOver(name string, all func() iter.Seq2[int, ast.Directive]) *Table {
	return &Table{
		Name:    name,
		Columns: postingColumns,
		Rows: func() iter.Seq[Row] {
			return func(yield func(Row) bool) {
				for _, d := range all() {
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

// Postings returns the default virtual table: one row per posting of every
// transaction in l, in the ledger's canonical order; non-transaction
// directives are skipped. The returned table is immutable and safe for
// concurrent read (see the package doc); it holds l by reference and never
// mutates it.
func Postings(l *ast.Ledger) *Table {
	return PostingsOver("postings", l.All)
}

// postingCol builds a [Column] whose accessor receives the row handle already
// asserted to [postingRow].
func postingCol(name string, t types.Type, fn func(postingRow) types.Value) Column {
	return Column{
		Name: name,
		Type: t,
		Accessor: func(r Row) types.Value {
			return fn(r.(postingRow))
		},
	}
}

// postingPosition builds the inventory Position a booked posting contributes:
// its units plus the booked lot (nil for cash). ok is false when the posting
// has no amount.
func postingPosition(p *ast.Posting) (inventory.Position, bool) {
	if p.Amount == nil {
		return inventory.Position{}, false
	}
	var lot *inventory.Lot
	if p.Cost != nil && p.Cost.IsBooked() {
		lot = inventory.LotFromCost(p.Cost.(*ast.Cost))
	}
	return inventory.Position{Units: *p.Amount, Cost: lot}, true
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
		pos, ok := postingPosition(r.posting())
		if !ok {
			return types.Null(types.Position)
		}
		return types.NewPosition(pos)
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
	// posting meta only — not merged with parent txn
	postingCol("meta", types.DictType, func(r postingRow) types.Value {
		return metaval.Dict(r.posting().Meta)
	}),
	// parent transaction meta
	postingCol("entry_meta", types.DictType, func(r postingRow) types.Value {
		return metaval.Dict(r.txn.Meta)
	}),
	// txn meta merged with posting meta; posting wins
	postingCol("any_meta", types.DictType, func(r postingRow) types.Value {
		return mergedMeta(r.txn.Meta, r.posting().Meta)
	}),
	postingCol("id", types.String, func(r postingRow) types.Value {
		return types.NewString(entryID(r.txn))
	}),
	postingCol("location", types.String, func(r postingRow) types.Value {
		return spanLocation(r.posting().Span)
	}),
	postingCol("description", types.String, func(r postingRow) types.Value {
		return description(r.txn.Payee, r.txn.Narration)
	}),
	postingCol("other_accounts", types.SetType, func(r postingRow) types.Value {
		return types.NewSet(txnAccounts(r.txn, r.idx)...)
	}),
	postingCol("accounts", types.SetType, func(r postingRow) types.Value {
		return types.NewSet(txnAccounts(r.txn, -1)...)
	}),
	postingCol("posting_flag", types.String, func(r postingRow) types.Value {
		return flagString(r.posting().Flag)
	}),
	// balance's value is supplied by the executor over the selected rows (see
	// RunningBalanceColumn); this placeholder returns NULL for direct reads.
	postingCol(RunningBalanceColumn, types.Inventory, func(postingRow) types.Value {
		return types.Null(types.Inventory)
	}),
}

// txnAccounts returns the accounts of txn's postings, excluding the posting at
// exclude (pass -1 to include all). Exclusion is by position, so a sibling
// posting sharing the excluded posting's account is still returned.
func txnAccounts(txn *ast.Transaction, exclude int) []string {
	accts := make([]string, 0, len(txn.Postings))
	for i := range txn.Postings {
		if i == exclude {
			continue
		}
		accts = append(accts, string(txn.Postings[i].Account))
	}
	return accts
}

// mergedMeta builds a Dict from base overlaid by overlay; overlay wins on conflict.
func mergedMeta(base, overlay ast.Metadata) types.Value {
	out := make(map[string]types.Value, len(base.Props)+len(overlay.Props))
	for k, mv := range base.Props {
		out[k] = metaval.Value(mv)
	}
	for k, mv := range overlay.Props {
		out[k] = metaval.Value(mv)
	}
	return types.NewDict(out)
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
