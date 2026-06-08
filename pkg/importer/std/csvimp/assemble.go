package csvimp

import (
	"fmt"

	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// assembler holds builder state for a single compile call.
type assembler struct {
	b    *csvbase.Builder
	s    *shape
	cols map[string]csvbase.Key[string]
}

// col returns a Key for the named column, sharing a single Key per name.
func (a *assembler) col(name string) csvbase.Key[string] {
	if k, ok := a.cols[name]; ok {
		return k
	}
	k := csvbase.Column(a.b, name)
	a.cols[name] = k
	return k
}

// colsFor returns Keys for each name, reusing Keys via col.
func (a *assembler) colsFor(names []string) []csvbase.Key[string] {
	keys := make([]csvbase.Key[string], len(names))
	for i, n := range names {
		keys[i] = a.col(n)
	}
	return keys
}

// compile builds a *csvbase.Driver from shape s under instance name. It
// supports the core feature set: date, amounts, currency, account,
// counter_account, payee, and narration columns. split, cost, and
// narration-template are not yet supported and return an error when present.
func compile(name string, s *shape) (*csvbase.Driver, error) {
	if s.split != nil || s.cost != nil || s.narrationTemplate != nil {
		return nil, fmt.Errorf("csvimp: compile: split/cost/narration-template not yet supported")
	}
	a := &assembler{b: csvbase.NewBuilder(), s: s, cols: map[string]csvbase.Key[string]{}}

	date := csvbase.ParseDate(a.b, a.col(s.dateCol), s.dateFormat, "")

	var sum csvbase.Key[*csvkit.Amount]
	for i, ac := range s.amounts {
		pa := csvbase.ParseAmount(a.b, a.col(ac.Col), csvbase.ParseAmountConfig{
			Format:        s.numberFormat,
			SplitCurrency: s.currencyFromAmount,
		})
		if ac.Negate {
			pa = csvbase.NegateAmount(a.b, pa)
		}
		if i == 0 {
			sum = pa
		} else {
			sum = csvbase.AddAmounts(a.b, sum, pa, "")
		}
	}
	curHint := csvbase.CurrencyHint(a.b, sum)

	// Currency: mapped col > amount hint > const default.
	var curIns []csvbase.Key[string]
	if s.currencyCol != "" {
		curIns = append(curIns, csvbase.MapValue(a.b, csvbase.Trim(a.b, a.col(s.currencyCol)), s.currencyMap, csvkit.Verbatim, ""))
	}
	curIns = append(curIns, curHint)
	if s.currencyDefault != "" {
		curIns = append(curIns, csvbase.Const(a.b, s.currencyDefault))
	}
	currency := csvbase.Coalesce(a.b, curIns...)

	// Account: Hints["account"] > mapped col > const default.
	hint := csvbase.Hint(a.b, "account")
	joined := csvbase.JoinKeys(a.b, s.accountSep, a.colsFor(s.accountCols)...)
	mapped := csvbase.MapValue(a.b, joined, s.accountMap, mapMode(s.accountMap), csvbase.DiagUnmappedAccount)
	account := csvbase.Else(a.b, hint, csvbase.Else(a.b, mapped, csvbase.Const(a.b, s.accountDefault)))

	// zero Key = unconfigured counter; EmitTransaction emits a single posting.
	var counter csvbase.Key[string]
	switch {
	case len(s.counterAccountCols) == 0 && s.counterAccountDefault == "":
	case len(s.counterAccountCols) == 0:
		counter = csvbase.Const(a.b, s.counterAccountDefault)
	default:
		cjoined := csvbase.JoinKeys(a.b, s.counterAccountSep, a.colsFor(s.counterAccountCols)...)
		cmapped := csvbase.MapValue(a.b, cjoined, s.counterAccountMap, mapMode(s.counterAccountMap), csvbase.DiagUnmappedCounterAccount)
		cwarned := csvbase.DiagAsWarning(a.b, cmapped, csvbase.DiagUnmappedCounterAccount)
		counter = csvbase.Else(a.b, cwarned, csvbase.Const(a.b, s.counterAccountDefault))
	}

	var payee csvbase.Key[string]
	if len(s.payeeCols) > 0 {
		payee = csvbase.MapValue(a.b, csvbase.JoinKeys(a.b, s.payeeSep, a.colsFor(s.payeeCols)...), s.payeeMap, csvkit.Verbatim, "")
	}

	var narration csvbase.Key[string]
	if len(s.narrationCols) > 0 {
		trimmed := make([]csvbase.Key[string], len(s.narrationCols))
		for i, n := range s.narrationCols {
			trimmed[i] = csvbase.Trim(a.b, a.col(n))
		}
		mappedCells := csvbase.MapEach(a.b, trimmed, s.narrationMap, csvkit.Verbatim, "")
		narration = csvbase.JoinKeys(a.b, s.narrationSep, mappedCells...)
	}

	pipeline := a.b.Emit(csvbase.EmitTransaction(csvbase.TxConfig{
		Date:      date,
		Amount:    sum,
		Currency:  currency,
		Account:   account,
		Counter:   counter,
		Payee:     payee,
		Narration: narration,
	}))

	var gate csvbase.Gate = csvbase.DefaultGate
	if s.compiledMatch != nil {
		gate = csvbase.AllGates(csvbase.DefaultGate, csvbase.PathMatch(s.compiledMatch))
	}

	return csvbase.New(name, csvbase.Config{
		Reader:  *s.reader(),
		Gate:    gate,
		Mapper:  pipeline,
		Filters: s.filters,
		RowHash: &csvbase.RowHash{Key: rowhashKey},
	})
}
