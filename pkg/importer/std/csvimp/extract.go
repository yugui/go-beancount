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
			continue
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
			fmt.Sprintf("no currency: currency_col=%q default_currency=%q", s.currencyCol, s.defaultCur))
		return nil, &d
	}

	account := resolveAccount(s, hints)
	if account == "" {
		d := rowDiag(DiagMissingAccount, path, line,
			"no account: Hints[\"account\"] empty and shape.account empty")
		return nil, &d
	}

	payee := ""
	if s.payeeCol != "" {
		payee = strings.TrimSpace(fieldAt(row, idx, s.payeeCol))
	}
	narration := buildNarration(s, idx, row)

	tx := &ast.Transaction{
		Date:      parsedDate,
		Flag:      '*',
		Payee:     payee,
		Narration: narration,
		Postings: []ast.Posting{{
			Account: ast.Account(account),
			Amount:  &ast.Amount{Number: *sum, Currency: currency},
		}},
	}
	return importerutil.StampMetadata(tx, rowhashKey, hash), nil
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

func resolveCurrency(s *shape, idx map[string]int, row []string) string {
	if s.currencyCol != "" {
		if v := strings.TrimSpace(fieldAt(row, idx, s.currencyCol)); v != "" {
			return v
		}
	}
	return s.defaultCur
}

func resolveAccount(s *shape, hints map[string]string) string {
	if v, ok := hints["account"]; ok && v != "" {
		return v
	}
	return s.account
}

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
		parts = append(parts, v)
	}
	return strings.Join(parts, s.narrationSep)
}
