package csvimp

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/importer/importerutil"
)

const rowhashKey = "csvimp-rowhash"

func extractRows(ctx context.Context, in importer.Input, name string, s *shape) (importer.Output, error) {
	rc, err := in.Opener()
	if err != nil {
		return importer.Output{}, fmt.Errorf("csvimp: opening %q: %w", in.Path, err)
	}
	defer rc.Close()

	rdr, hdr, err := openCSVAtBody(rc, s)
	if err != nil {
		return importer.Output{}, fmt.Errorf("csvimp: reading header from %q: %w", in.Path, err)
	}
	idx := buildColumnIndex(hdr)

	if diags, ok := checkMissingColumns(in.Path, name, s, idx); !ok {
		return importer.Output{Diagnostics: diags}, nil
	}

	var (
		directives []ast.Directive
		diags      []ast.Diagnostic
		row        []string
	)
	for {
		if err := ctx.Err(); err != nil {
			return importer.Output{Directives: directives, Diagnostics: diags}, err
		}
		row, err = rdr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return importer.Output{Directives: directives, Diagnostics: diags},
				fmt.Errorf("csvimp: parsing row in %q: %w", in.Path, err)
		}
		if len(row) == 0 || allBlank(row) {
			continue
		}
		csvLine, _ := rdr.FieldPos(0)
		line := csvLine + s.skipLines
		dir, diag := processRow(in.Path, line, name, s, idx, row, in.Hints)
		if diag != nil {
			diags = append(diags, *diag)
		}
		if dir != nil {
			directives = append(directives, dir)
		}
	}
	return importer.Output{Directives: directives, Diagnostics: diags}, nil
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

// openCSVAtBody returns a csv.Reader positioned past the header and the
// parsed header row. On success the reader is ready to yield body rows.
func openCSVAtBody(rc io.Reader, s *shape) (*csv.Reader, []string, error) {
	if s.inputEncoding != nil {
		rc = s.inputEncoding.NewDecoder().Reader(rc)
	}
	br := bufio.NewReader(rc)
	if err := skipRawLines(br, s.skipLines); err != nil {
		return nil, nil, err
	}
	rdr := csv.NewReader(br)
	rdr.Comma = s.delimiter
	rdr.FieldsPerRecord = -1
	rdr.LazyQuotes = true
	hdr, err := rdr.Read()
	if err != nil {
		return nil, nil, err
	}
	return rdr, hdr, nil
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
	hash := rowHash(name, row)

	dateRaw := fieldAt(row, idx, s.dateCol)
	parsedDate, err := time.Parse(s.dateFormat, strings.TrimSpace(dateRaw))
	if err != nil {
		d := rowDiag(DiagBadDate, path, line,
			fmt.Sprintf("cannot parse date %q with format %q: %v", dateRaw, s.dateFormat, err))
		return nil, &d
	}

	sum, status, badCol := sumAmounts(s, idx, row)
	switch status {
	case amountStatusOK:
	case amountStatusBad:
		d := rowDiag(DiagBadAmount, path, line,
			fmt.Sprintf("cannot parse amount column %q: %q", badCol, fieldAt(row, idx, badCol)))
		return nil, &d
	case amountStatusAllBlank:
		d := rowDiag(DiagAllBlankAmount, path, line, "all amount columns blank")
		return nil, &d
	}

	currency := resolveCurrency(s, idx, row)
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
	narration := buildNarration(s, idx, row)

	postings := make([]ast.Posting, 0, 2)
	postings = append(postings, ast.Posting{
		Account: ast.Account(account),
		Amount:  &ast.Amount{Number: *sum, Currency: currency},
	})
	if hasCounter {
		var neg apd.Decimal
		if _, err := apd.BaseContext.Neg(&neg, sum); err != nil {
			d := rowDiag(DiagBadAmount, path, line,
				fmt.Sprintf("cannot negate amount for counter posting: %v", err))
			return nil, &d
		}
		postings = append(postings, ast.Posting{
			Account: ast.Account(counter),
			Amount:  &ast.Amount{Number: neg, Currency: currency},
		})
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
	if len(cols) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		v := strings.TrimSpace(fieldAt(row, idx, c))
		if v == "" {
			continue
		}
		parts = append(parts, v)
	}
	return strings.Join(parts, sep)
}

type amountStatus int

const (
	amountStatusOK amountStatus = iota
	amountStatusBad
	amountStatusAllBlank
)

// sumAmounts returns the signed sum of all amount columns in row. A
// negate=true column is subtracted. Blank cells contribute 0. Returns
// amountStatusAllBlank when every amount cell is blank, amountStatusBad
// (with the offending column name) when a non-blank cell fails decimal
// parsing.
func sumAmounts(s *shape, idx map[string]int, row []string) (*apd.Decimal, amountStatus, string) {
	sum := new(apd.Decimal)
	allAmountsBlank := true
	for _, a := range s.amounts {
		raw := strings.TrimSpace(fieldAt(row, idx, a.Col))
		if raw == "" {
			continue
		}
		allAmountsBlank = false
		var v apd.Decimal
		if _, _, err := apd.BaseContext.SetString(&v, raw); err != nil {
			return nil, amountStatusBad, a.Col
		}
		op := apd.BaseContext.Add
		if a.Negate {
			op = apd.BaseContext.Sub
		}
		if _, err := op(sum, sum, &v); err != nil {
			return nil, amountStatusBad, a.Col
		}
	}
	if allAmountsBlank {
		return nil, amountStatusAllBlank, ""
	}
	return sum, amountStatusOK, ""
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
	if s.accountMap == nil {
		return key, nil
	}
	if mapped, ok := s.accountMap[key]; ok {
		return mapped, nil
	}
	d := rowDiag(DiagUnmappedAccount, path, line,
		fmt.Sprintf("account key %q from columns %v has no entry in [account.map]", key, s.accountCols))
	return "", &d
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
	if s.counterAccountMap == nil {
		return key, nil, true
	}
	if mapped, ok := s.counterAccountMap[key]; ok {
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
//  2. [currency].default.
//
// Returns "" when neither the col cell nor the default produces a value;
// the caller treats that as DiagMissingCurrency.
func resolveCurrency(s *shape, idx map[string]int, row []string) string {
	if s.currencyCol == "" {
		return s.currencyDefault
	}
	v := strings.TrimSpace(fieldAt(row, idx, s.currencyCol))
	if v == "" {
		return s.currencyDefault
	}
	if mapped, ok := s.currencyMap[v]; ok {
		return mapped
	}
	return v
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
	if mapped, ok := s.payeeMap[v]; ok {
		return mapped
	}
	return v
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
		if mapped, ok := s.narrationMap[v]; ok {
			v = mapped
		}
		if v == "" {
			continue
		}
		parts = append(parts, v)
	}
	return strings.Join(parts, s.narrationSep)
}
