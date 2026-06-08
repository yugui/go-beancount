package csvimp

import (
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

const rowhashKey = "csvimp-rowhash"

// mapMode selects strict resolution when a translation table is configured and
// pass-through resolution otherwise.
func mapMode(m map[string]string) csvkit.MapMode {
	if m == nil {
		return csvkit.Verbatim
	}
	return csvkit.Strict
}

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
// supports the full feature set: date, amounts, currency, account,
// counter_account, payee, narration (columns or template), split, and cost.
func compile(name string, s *shape) (*csvbase.Driver, error) {
	a := &assembler{b: csvbase.NewBuilder(), s: s, cols: map[string]csvbase.Key[string]{}}

	if s.split != nil {
		groups := csvbase.SplitColumns(a.b, a.col(s.split.col), s.split.re)
		maps.Copy(a.cols, groups)
	}

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
	switch {
	case s.narrationTemplate != nil:
		data := csvbase.Row(a.b)
		if s.split != nil {
			over := make(map[string]csvbase.Key[string], len(s.split.groups))
			for colName := range s.split.groups {
				over[colName] = a.col(colName)
			}
			data = csvbase.Merge(a.b, data, over)
		}
		narration = csvbase.Template(a.b, s.narrationTemplate, data)
	case len(s.narrationCols) > 0:
		trimmed := make([]csvbase.Key[string], len(s.narrationCols))
		for i, n := range s.narrationCols {
			trimmed[i] = csvbase.Trim(a.b, a.col(n))
		}
		mappedCells := csvbase.MapEach(a.b, trimmed, s.narrationMap, csvkit.Verbatim, "")
		narration = csvbase.JoinKeys(a.b, s.narrationSep, mappedCells...)
	}

	var cost csvbase.Key[*ast.CostSpec]
	if s.cost != nil {
		cost = a.costKey()
	}

	pipeline := a.b.Emit(csvbase.EmitTransaction(csvbase.TxConfig{
		Date:      date,
		Amount:    sum,
		Currency:  currency,
		Account:   account,
		Counter:   counter,
		Payee:     payee,
		Narration: narration,
		Cost:      cost,
	}))

	var gate csvbase.Gate = csvbase.DefaultGate
	if s.compiledMatch != nil {
		gate = csvbase.AllGates(csvbase.DefaultGate, csvbase.PathMatch(s.compiledMatch))
	}

	return csvbase.New(name, csvbase.Config{
		Reader: csvkit.Reader{
			Delimiter:   s.delimiter,
			Encoding:    s.inputEncoding,
			LazyQuotes:  true,
			SkipLines:   s.skipLines,
			HeaderMatch: s.headerMatch,
			Columns:     s.columns,
		},
		Gate:    gate,
		Mapper:  pipeline,
		Filters: s.filters,
		RowHash: &csvbase.RowHash{Key: rowhashKey},
	})
}

// costKey builds the Key producing a per-row *ast.CostSpec from s.cost. A blank
// cost-number cell yields nil (no cost); an unparseable number, an unresolved
// cost currency, or an unparseable date soft-fails with DiagBadCost.
func (a *assembler) costKey() csvbase.Key[*ast.CostSpec] {
	c := a.s.cost
	numKey := a.col(c.numberCol)
	hasCur := c.currencyCol != ""
	hasDate := c.dateCol != ""
	hasLabel := c.labelCol != ""
	var curKey, dateKey, labelKey csvbase.Key[string]
	if hasCur {
		curKey = a.col(c.currencyCol)
	}
	if hasDate {
		dateKey = a.col(c.dateCol)
	}
	if hasLabel {
		labelKey = a.col(c.labelCol)
	}

	return csvbase.AddStep(a.b, func(ms *csvbase.MappingState) (*ast.CostSpec, *ast.Diagnostic, error) {
		info := ms.Info()
		raw, d := csvbase.Value(ms, numKey)
		if d != nil {
			return nil, d, nil
		}
		num, blank, err := csvkit.ParseNumber(raw, a.s.numberFormat)
		if blank {
			return nil, nil, nil
		}
		if err != nil {
			diag := csvbase.ErrorDiag(csvbase.DiagBadCost, info.Path, info.Line,
				fmt.Sprintf("cannot parse cost column %q: %q", c.numberCol, raw))
			return nil, &diag, nil
		}
		cur := c.currencyDefault
		if hasCur {
			v, _ := csvbase.Value(ms, curKey)
			if t := strings.TrimSpace(v); t != "" {
				cur = t
			}
		}
		if cur == "" {
			diag := csvbase.ErrorDiag(csvbase.DiagBadCost, info.Path, info.Line,
				"cost has no currency: [cost].currency blank and no default_currency")
			return nil, &diag, nil
		}
		label := ""
		if hasLabel {
			v, _ := csvbase.Value(ms, labelKey)
			label = strings.TrimSpace(v)
		}
		cs := &ast.CostSpec{Currency: cur, Label: label}
		n := num
		if c.isTotal {
			cs.Total = &n
		} else {
			cs.PerUnit = &n
		}
		if hasDate {
			if v, _ := csvbase.Value(ms, dateKey); strings.TrimSpace(v) != "" {
				dv := strings.TrimSpace(v)
				t, err := time.Parse(c.dateFormat, dv)
				if err != nil {
					diag := csvbase.ErrorDiag(csvbase.DiagBadCost, info.Path, info.Line,
						fmt.Sprintf("cannot parse cost date %q with format %q: %v", dv, c.dateFormat, err))
					return nil, &diag, nil
				}
				cs.Date = &t
			}
		}
		return cs, nil, nil
	})
}
