package csvimp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/importer/importerutil"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

const rowhashKey = "csvimp-rowhash"

func extractRows(ctx context.Context, in importer.Input, name string, s *shape) (importer.Output, error) {
	rc, err := in.Opener()
	if err != nil {
		return importer.Output{}, fmt.Errorf("csvimp: opening %q: %w", in.Path, err)
	}
	defer rc.Close()

	hdr, rows, err := s.reader().Records(rc)
	if err != nil {
		return importer.Output{}, fmt.Errorf("csvimp: reading header from %q: %w", in.Path, err)
	}
	idx := s.columns
	if idx == nil {
		idx = buildColumnIndex(hdr)
	}

	if diags, ok := checkMissingColumns(in.Path, name, s, idx); !ok {
		return importer.Output{Diagnostics: diags}, nil
	}

	var (
		directives []ast.Directive
		diags      []ast.Diagnostic
	)
	for rec, rerr := range rows {
		if err := ctx.Err(); err != nil {
			return importer.Output{Directives: directives, Diagnostics: diags}, err
		}
		if rerr != nil {
			return importer.Output{Directives: directives, Diagnostics: diags},
				fmt.Errorf("csvimp: parsing row in %q: %w", in.Path, rerr)
		}
		if len(rec.Fields) == 0 || allBlank(rec.Fields) {
			continue
		}
		if s.excluded(rec.Fields, idx) {
			continue
		}
		dir, diag := processRow(in.Path, rec.Line, name, s, idx, rec.Fields, in.Hints)
		if diag != nil {
			diags = append(diags, *diag)
		}
		if dir != nil {
			directives = append(directives, dir)
		}
	}
	return importer.Output{Directives: directives, Diagnostics: diags}, nil
}

// excluded reports whether any configured [[exclude]] filter drops the
// record. Filtered rows are skipped silently: they are statement noise
// (footnotes, totals), not data errors.
func (s *shape) excluded(fields []string, idx map[string]int) bool {
	if len(s.filters) == 0 {
		return false
	}
	get := func(col string) string { return fieldAt(fields, idx, col) }
	for _, f := range s.filters {
		if f.Skip(fields, get) {
			return true
		}
	}
	return false
}

// reader returns a csvkit.Reader configured from s.
func (s *shape) reader() *csvkit.Reader {
	return &csvkit.Reader{
		Delimiter:   s.delimiter,
		Encoding:    s.inputEncoding,
		LazyQuotes:  true,
		SkipLines:   s.skipLines,
		HeaderMatch: s.headerMatch,
		Columns:     s.columns,
	}
}

func checkMissingColumns(path, name string, s *shape, idx map[string]int) ([]ast.Diagnostic, bool) {
	var diags []ast.Diagnostic
	for _, col := range requiredColumns(s) {
		if _, ok := idx[col]; !ok {
			diags = append(diags, rowDiag(DiagMissingColumn, path, 0,
				fmt.Sprintf("required column %q not present in header (shape %q)", col, name)))
		}
	}
	return diags, len(diags) == 0
}

func allBlank(row []string) bool {
	for _, f := range row {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

func processRow(path string, line int, name string, s *shape, idx map[string]int, row []string, hints map[string]string) (ast.Directive, *ast.Diagnostic) {
	// hash the raw row before split augments it: synthetic columns must
	// not affect the idempotency key.
	hash := rowHash(name, row)

	if s.split != nil {
		row, idx = applySplit(s, row, idx)
	}

	dateRaw := fieldAt(row, idx, s.dateCol)
	parsedDate, err := time.Parse(s.dateFormat, strings.TrimSpace(dateRaw))
	if err != nil {
		d := rowDiag(DiagBadDate, path, line,
			fmt.Sprintf("cannot parse date %q with format %q: %v", dateRaw, s.dateFormat, err))
		return nil, &d
	}

	sum, status, badCol := sumAmounts(s, idx, row)
	switch status {
	case csvkit.AmountOK:
	case csvkit.AmountBad:
		d := rowDiag(DiagBadAmount, path, line,
			fmt.Sprintf("cannot parse amount column %q: %q", badCol, fieldAt(row, idx, badCol)))
		return nil, &d
	case csvkit.AmountAllBlank:
		d := rowDiag(DiagAllBlankAmount, path, line, "all amount columns blank")
		return nil, &d
	}

	currency := resolveCurrency(s, idx, row, sum.CurrencyHint)
	if currency == "" {
		d := rowDiag(DiagMissingCurrency, path, line,
			fmt.Sprintf("no currency: [currency].col=%q [currency].default=%q", s.currencyCol, s.currencyDefault))
		return nil, &d
	}

	account, diag := resolveAccount(s, idx, row, hints, path, line)
	if diag != nil {
		return nil, diag
	}

	counter, counterWarn, hasCounter := resolveCounterAccount(s, idx, row, path, line)

	payee := resolvePayee(s, idx, row)
	narration, ndiag := renderNarration(s, idx, row, path, line)
	if ndiag != nil {
		return nil, ndiag
	}

	primary := ast.Posting{
		Account: ast.Account(account),
		Amount:  &ast.Amount{Number: sum.Number, Currency: currency},
	}
	hasCost := false
	if s.cost != nil {
		cs, cdiag := buildCost(s, idx, row, path, line)
		if cdiag != nil {
			return nil, cdiag
		}
		if cs != nil {
			primary.Cost = cs
			hasCost = true
		}
	}

	postings := make([]ast.Posting, 0, 2)
	postings = append(postings, primary)
	if hasCounter {
		if hasCost {
			// elided cash leg: beancount balances it against the cost.
			postings = append(postings, ast.Posting{Account: ast.Account(counter)})
		} else {
			var neg apd.Decimal
			if _, err := apd.BaseContext.Neg(&neg, &sum.Number); err != nil {
				d := rowDiag(DiagBadAmount, path, line,
					fmt.Sprintf("cannot negate amount for counter posting: %v", err))
				return nil, &d
			}
			postings = append(postings, ast.Posting{
				Account: ast.Account(counter),
				Amount:  &ast.Amount{Number: neg, Currency: currency},
			})
		}
	}

	tx := &ast.Transaction{
		Date:      parsedDate,
		Flag:      '*',
		Payee:     payee,
		Narration: narration,
		Postings:  postings,
	}
	return importerutil.StampMetadata(tx, rowhashKey, hash), counterWarn
}

// fieldAt returns row[idx[col]] or "" when col is unknown or the row
// is shorter than the header. Short rows are tolerated: any missing
// trailing column reads as blank.
func fieldAt(row []string, idx map[string]int, col string) string {
	i, ok := idx[col]
	if !ok || i >= len(row) {
		return ""
	}
	return row[i]
}

// joinKey trims each cell named in cols, drops blanks, and joins the
// survivors with sep. Returns "" when every cell is blank or cols is
// empty.
func joinKey(cols []string, sep string, idx map[string]int, row []string) string {
	return csvkit.Join(cols, sep, func(c string) string { return fieldAt(row, idx, c) })
}

// sumAmounts returns the signed sum of the shape's amount columns under
// its NumberFormat, delegating to csvkit. A negate=true column is
// subtracted; blank or placeholder cells contribute nothing. When the
// shape extracts currency from the amount cell, the result's CurrencyHint
// carries it.
func sumAmounts(s *shape, idx map[string]int, row []string) (csvkit.Amount, csvkit.AmountStatus, string) {
	p := csvkit.AmountParser{Format: s.numberFormat, SplitCurrency: s.currencyFromAmount}
	return p.Sum(s.amounts, func(col string) string { return fieldAt(row, idx, col) })
}

// buildCost constructs the CostSpec for the primary posting from the row.
// Returns (nil, nil) when the cost-number cell is blank: the row simply
// carries no cost. A non-blank but unparseable number, a number with no
// resolvable cost currency, or an unparseable date yields DiagBadCost.
func buildCost(s *shape, idx map[string]int, row []string, path string, line int) (*ast.CostSpec, *ast.Diagnostic) {
	c := s.cost
	raw := fieldAt(row, idx, c.numberCol)
	num, blank, err := csvkit.ParseNumber(raw, s.numberFormat)
	if blank {
		return nil, nil
	}
	if err != nil {
		d := rowDiag(DiagBadCost, path, line,
			fmt.Sprintf("cannot parse cost column %q: %q", c.numberCol, raw))
		return nil, &d
	}

	cur := c.currencyDefault
	if c.currencyCol != "" {
		if v := strings.TrimSpace(fieldAt(row, idx, c.currencyCol)); v != "" {
			cur = v
		}
	}
	if cur == "" {
		d := rowDiag(DiagBadCost, path, line, "cost has no currency: [cost].currency blank and no default_currency")
		return nil, &d
	}

	cs := &ast.CostSpec{Currency: cur, Label: strings.TrimSpace(fieldAt(row, idx, c.labelCol))}
	n := num
	if c.isTotal {
		cs.Total = &n
	} else {
		cs.PerUnit = &n
	}
	if c.dateCol != "" {
		if dv := strings.TrimSpace(fieldAt(row, idx, c.dateCol)); dv != "" {
			t, err := time.Parse(c.dateFormat, dv)
			if err != nil {
				d := rowDiag(DiagBadCost, path, line,
					fmt.Sprintf("cannot parse cost date %q with format %q: %v", dv, c.dateFormat, err))
				return nil, &d
			}
			cs.Date = &t
		}
	}
	return cs, nil
}

// resolveAccount resolves a row's primary beancount account. Priority:
//
//  1. Hints["account"] when non-empty (CLI/caller override).
//  2. joined [account].col cells (trimmed, blank-skipped, separator-joined)
//     when non-empty: with [account.map] set, a strict lookup returns the
//     mapped value or DiagUnmappedAccount on miss; with no map, the joined
//     value is used verbatim.
//  3. [account].default when non-empty.
//  4. Otherwise: DiagMissingAccount.
func resolveAccount(s *shape, idx map[string]int, row []string, hints map[string]string, path string, line int) (string, *ast.Diagnostic) {
	if v, ok := hints["account"]; ok && v != "" {
		return v, nil
	}
	if len(s.accountCols) == 0 {
		return resolveAccountDefault(s, path, line)
	}
	key := joinKey(s.accountCols, s.accountSep, idx, row)
	if key == "" {
		return resolveAccountDefault(s, path, line)
	}
	return resolveAccountFromKey(s, key, path, line)
}

// resolveAccountDefault returns [account].default or DiagMissingAccount when
// no default is configured.
func resolveAccountDefault(s *shape, path string, line int) (string, *ast.Diagnostic) {
	if s.accountDefault == "" {
		d := rowDiag(DiagMissingAccount, path, line,
			`no account: Hints["account"] empty, [account].col blank/absent, and [account].default unset`)
		return "", &d
	}
	return s.accountDefault, nil
}

// resolveAccountFromKey resolves a non-empty joined key through
// [account.map]. With no map configured the key is returned verbatim;
// with a map configured a miss returns DiagUnmappedAccount.
func resolveAccountFromKey(s *shape, key, path string, line int) (string, *ast.Diagnostic) {
	if mapped, ok := csvkit.ResolveThroughMap(key, s.accountMap, mapMode(s.accountMap)); ok {
		return mapped, nil
	}
	d := rowDiag(DiagUnmappedAccount, path, line,
		fmt.Sprintf("account key %q from columns %v has no entry in [account.map]", key, s.accountCols))
	return "", &d
}

// mapMode selects strict resolution when a translation table is
// configured and pass-through resolution otherwise.
func mapMode(m map[string]string) csvkit.MapMode {
	if m == nil {
		return csvkit.Verbatim
	}
	return csvkit.Strict
}

// resolveCounterAccount resolves a row's counter (balancing) account
// when [counter_account] is configured. The third return reports
// whether the caller should emit a second posting. Hints["account"] is
// never consulted here — it overrides only the primary account.
//
// When [counter_account] is unconfigured (no col and no default),
// returns ("", nil, false): no second posting is emitted.
//
// When configured but the joined key is empty and no default is set,
// returns ("", nil, false): a single (unbalanced) posting remains. This
// soft fallback lets shapes describe categorisation that may be absent
// on some rows without dropping those rows.
//
// When configured with [counter_account.map] in strict mode, a
// non-empty key missing from the map returns
// ("", &DiagUnmappedCounterAccount with Warning severity, false): the
// row is kept as a single (unbalanced) posting, and the warning
// surfaces the configuration gap without dropping the transaction.
func resolveCounterAccount(s *shape, idx map[string]int, row []string, path string, line int) (string, *ast.Diagnostic, bool) {
	if len(s.counterAccountCols) == 0 && s.counterAccountDefault == "" {
		return "", nil, false
	}
	if len(s.counterAccountCols) == 0 {
		return s.counterAccountDefault, nil, true
	}
	key := joinKey(s.counterAccountCols, s.counterAccountSep, idx, row)
	if key == "" {
		if s.counterAccountDefault == "" {
			return "", nil, false
		}
		return s.counterAccountDefault, nil, true
	}
	if mapped, ok := csvkit.ResolveThroughMap(key, s.counterAccountMap, mapMode(s.counterAccountMap)); ok {
		return mapped, nil, true
	}
	d := rowWarn(DiagUnmappedCounterAccount, path, line,
		fmt.Sprintf("counter_account key %q from columns %v has no entry in [counter_account.map]", key, s.counterAccountCols))
	return "", &d, false
}

// resolveCurrency resolves a row's currency. Priority:
//
//  1. [currency].col cell when non-blank: when [currency.map] holds the
//     value, the mapped currency is returned; otherwise the trimmed cell
//     value is used verbatim (pass-through).
//  2. hint — a currency extracted from the amount cell (only when
//     [currency].from_amount is set; empty otherwise).
//  3. [currency].default.
//
// An explicit currency column outranks an amount-cell suffix. Returns ""
// when no source produces a value; the caller treats that as
// DiagMissingCurrency.
func resolveCurrency(s *shape, idx map[string]int, row []string, hint string) string {
	if s.currencyCol != "" {
		if v := strings.TrimSpace(fieldAt(row, idx, s.currencyCol)); v != "" {
			mapped, _ := csvkit.ResolveThroughMap(v, s.currencyMap, csvkit.Verbatim)
			return mapped
		}
	}
	if hint != "" {
		return hint
	}
	return s.currencyDefault
}

// resolvePayee resolves a row's payee. Returns "" when [payee].col is
// unset or all cells join to a blank value. Otherwise applies
// [payee.map] to the joined value (pass-through on miss) and returns
// the result.
func resolvePayee(s *shape, idx map[string]int, row []string) string {
	if len(s.payeeCols) == 0 {
		return ""
	}
	v := joinKey(s.payeeCols, s.payeeSep, idx, row)
	if v == "" {
		return ""
	}
	mapped, _ := csvkit.ResolveThroughMap(v, s.payeeMap, csvkit.Verbatim)
	return mapped
}

// buildNarration concatenates the trimmed values of [narration].col with
// [narration].separator. When [narration.map] is set it is applied per
// cell BEFORE concatenation: a hit replaces the cell, a miss passes the
// value through unchanged. A mapped value of "" drops that cell from the
// concatenation (useful for masking noisy columns).
func buildNarration(s *shape, idx map[string]int, row []string) string {
	if len(s.narrationCols) == 0 {
		return ""
	}
	parts := make([]string, 0, len(s.narrationCols))
	for _, col := range s.narrationCols {
		v := strings.TrimSpace(fieldAt(row, idx, col))
		if v == "" {
			continue
		}
		// per-cell translation; a mapped "" drops the cell
		v, _ = csvkit.ResolveThroughMap(v, s.narrationMap, csvkit.Verbatim)
		if v == "" {
			continue
		}
		parts = append(parts, v)
	}
	return strings.Join(parts, s.narrationSep)
}

// renderNarration produces a row's narration: from [narration].template
// when configured (a render failure yields DiagBadNarrationTemplate and the
// row is skipped), otherwise from the concatenation built by buildNarration.
func renderNarration(s *shape, idx map[string]int, row []string, path string, line int) (string, *ast.Diagnostic) {
	if s.narrationTemplate == nil {
		return buildNarration(s, idx, row), nil
	}
	out, err := s.narrationTemplate.Render(rowMap(idx, row))
	if err != nil {
		d := rowDiag(DiagBadNarrationTemplate, path, line,
			fmt.Sprintf("[narration].template: %v", err))
		return "", &d
	}
	return out, nil
}

// applySplit extracts the named capture groups of s.split from its source
// column and returns row and idx augmented with one synthetic column per
// group. On no match the inputs are returned unchanged, so a group-named
// field reads as blank. The originals are never mutated.
func applySplit(s *shape, row []string, idx map[string]int) ([]string, map[string]int) {
	groups, ok := csvkit.NamedSubmatches(s.split.re, fieldAt(row, idx, s.split.col))
	if !ok {
		return row, idx
	}
	aug := make([]string, len(row), len(row)+len(groups))
	copy(aug, row)
	aidx := make(map[string]int, len(idx)+len(groups))
	for k, v := range idx {
		aidx[k] = v
	}
	for name, val := range groups {
		aidx[name] = len(aug)
		aug = append(aug, val)
	}
	return aug, aidx
}

// rowMap projects a row into a name-keyed map for template rendering.
// Columns absent from the row (short rows) map to "".
func rowMap(idx map[string]int, row []string) map[string]string {
	m := make(map[string]string, len(idx))
	for name, i := range idx {
		if i < len(row) {
			m[name] = row[i]
		} else {
			m[name] = ""
		}
	}
	return m
}
