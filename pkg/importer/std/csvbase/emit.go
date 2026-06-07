package csvbase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// MetaField stamps a Key's value (when non-empty) as an ast.MetaString under
// the given Name on the transaction's metadata.
type MetaField struct {
	Name  string
	Value Key[string]
}

// TxConfig wires resolved keys into a transaction. Date and Amount are
// required (a zero Key panics in EmitTransaction). Currency and Account are
// required at runtime (a soft-fail diagnostic or an empty value drops the
// row). Counter, Cost, Payee, Narration, Tags, Links, and Meta are optional
// (zero Keys are ignored).
type TxConfig struct {
	Date      Key[time.Time]
	Flag      byte // 0 selects '*'
	Payee     Key[string]
	Narration Key[string]
	Amount    Key[csvkit.Amount]
	Currency  Key[string]
	Account   Key[string]
	Counter   Key[string]
	Cost      Key[*ast.CostSpec]
	Tags      []Key[string]
	Links     []Key[string]
	Meta      []MetaField
	// MissingCurrencyCode overrides DiagMissingCurrency for the empty-value drop.
	MissingCurrencyCode string
	// MissingAccountCode overrides DiagMissingAccount for the empty-value drop.
	MissingAccountCode string
}

// EmitTransaction returns an EmitFunc that assembles one Transaction per row
// from the pre-resolved keys in cfg. It panics if Date or Amount is a zero Key
// (programmer error). Currency and Account soft-fails or empty values drop the
// row; Counter soft-fail is a warning that keeps the row with a single posting;
// all other required-field soft-fails drop the row.
func EmitTransaction(cfg TxConfig) EmitFunc {
	if isZeroKey(cfg.Date) {
		panic("csvbase: EmitTransaction: Date key is zero")
	}
	if isZeroKey(cfg.Amount) {
		panic("csvbase: EmitTransaction: Amount key is zero")
	}
	missingCurrencyCode := cfg.MissingCurrencyCode
	if missingCurrencyCode == "" {
		missingCurrencyCode = DiagMissingCurrency
	}
	missingAccountCode := cfg.MissingAccountCode
	if missingAccountCode == "" {
		missingAccountCode = DiagMissingAccount
	}
	flag := cfg.Flag
	if flag == 0 {
		flag = '*'
	}

	return func(_ context.Context, c *MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		info := c.Info()

		date, d := Value(c, cfg.Date)
		if d != nil {
			return nil, []ast.Diagnostic{*d}, nil
		}

		amt, d := Value(c, cfg.Amount)
		if d != nil {
			return nil, []ast.Diagnostic{*d}, nil
		}

		currency, d := Value(c, cfg.Currency)
		if d != nil {
			return nil, []ast.Diagnostic{*d}, nil
		}
		if strings.TrimSpace(currency) == "" {
			diag := ErrorDiag(missingCurrencyCode, info.Path, info.Line, "no currency resolved")
			return nil, []ast.Diagnostic{diag}, nil
		}

		account, d := Value(c, cfg.Account)
		if d != nil {
			return nil, []ast.Diagnostic{*d}, nil
		}
		if strings.TrimSpace(account) == "" {
			diag := ErrorDiag(missingAccountCode, info.Path, info.Line, "no account resolved")
			return nil, []ast.Diagnostic{diag}, nil
		}

		narration := ""
		if !isZeroKey(cfg.Narration) {
			narration, d = Value(c, cfg.Narration)
			if d != nil {
				return nil, []ast.Diagnostic{*d}, nil
			}
		}

		payee := ""
		if !isZeroKey(cfg.Payee) {
			payee, _ = Value(c, cfg.Payee)
		}

		var counterWarnings []ast.Diagnostic
		hasCounter := false
		counterAccount := ""
		if !isZeroKey(cfg.Counter) {
			counterAccount, d = Value(c, cfg.Counter)
			if d != nil {
				counterWarnings = append(counterWarnings, *d)
			} else if counterAccount != "" {
				hasCounter = true
			}
		}

		var costSpec *ast.CostSpec
		if !isZeroKey(cfg.Cost) {
			costSpec, d = Value(c, cfg.Cost)
			if d != nil {
				return nil, []ast.Diagnostic{*d}, nil
			}
		}

		primary := ast.Posting{
			Account: ast.Account(account),
			Amount:  &ast.Amount{Number: amt.Number, Currency: currency},
		}
		if costSpec != nil {
			primary.Cost = costSpec
		}

		postings := make([]ast.Posting, 1, 2)
		postings[0] = primary
		if hasCounter {
			if costSpec != nil {
				// elided cash leg
				postings = append(postings, ast.Posting{Account: ast.Account(counterAccount)})
			} else {
				var neg apd.Decimal
				if _, err := apd.BaseContext.Neg(&neg, &amt.Number); err != nil {
					diag := ErrorDiag(DiagBadAmount, info.Path, info.Line,
						fmt.Sprintf("cannot negate amount for counter posting: %v", err))
					return nil, []ast.Diagnostic{diag}, nil
				}
				postings = append(postings, ast.Posting{
					Account: ast.Account(counterAccount),
					Amount:  &ast.Amount{Number: neg, Currency: currency},
				})
			}
		}

		// tags and links
		var tags, links []string
		for _, k := range cfg.Tags {
			if isZeroKey(k) {
				continue
			}
			if v, _ := Value(c, k); v != "" {
				tags = append(tags, v)
			}
		}
		for _, k := range cfg.Links {
			if isZeroKey(k) {
				continue
			}
			if v, _ := Value(c, k); v != "" {
				links = append(links, v)
			}
		}

		// metadata
		var meta ast.Metadata
		for _, mf := range cfg.Meta {
			if isZeroKey(mf.Value) {
				continue
			}
			v, _ := Value(c, mf.Value)
			if v == "" {
				continue
			}
			if meta.Props == nil {
				meta.Props = make(map[string]ast.MetaValue)
			}
			meta.Props[mf.Name] = ast.MetaValue{Kind: ast.MetaString, String: v}
		}

		tx := &ast.Transaction{
			Date:      date,
			Flag:      flag,
			Payee:     payee,
			Narration: narration,
			Tags:      tags,
			Links:     links,
			Postings:  postings,
			Meta:      meta,
		}
		return []ast.Directive{tx}, counterWarnings, nil
	}
}
