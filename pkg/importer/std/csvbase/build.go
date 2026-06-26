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
// the given Name in a transaction's or posting's metadata.
type MetaField struct {
	Name  string
	Value Key[string]
}

// Amount combines a parsed amount with a currency into an *ast.Amount. A nil
// amt value yields nil — the auto-posting signal — regardless of cur. Otherwise
// the currency is cur's trimmed value when non-blank, else amt's CurrencyHint;
// when both are empty the step soft-fails with DiagMissingCurrency. A soft-failed
// amt or cur propagates (amt is checked first). cur may be a zero Key, in which
// case only the CurrencyHint is consulted.
func Amount(b *Builder, amt Key[*csvkit.Amount], cur Key[string]) Key[*ast.Amount] {
	return AddStep(b, func(c *MappingState) (*ast.Amount, *ast.Diagnostic, error) {
		v, d := Value(c, amt)
		if d != nil {
			return nil, d, nil
		}
		if v == nil {
			return nil, nil, nil
		}
		currency := ""
		if !isZeroKey(cur) {
			cv, cd := Value(c, cur)
			if cd != nil {
				return nil, cd, nil
			}
			currency = strings.TrimSpace(cv)
		}
		if currency == "" {
			currency = v.CurrencyHint
		}
		if currency == "" {
			info := c.Info()
			diag := ErrorDiag(DiagMissingCurrency, info.Path, info.Line, "no currency resolved")
			return nil, &diag, nil
		}
		return &ast.Amount{Number: v.Number, Currency: currency}, nil, nil
	})
}

// Price builds a posting price annotation (@ or @@) from amt and a currency. A
// nil amt value yields nil — no annotation. The currency is cur's trimmed value
// when non-blank, else amt's CurrencyHint; when both are empty the step
// soft-fails with DiagMissingCurrency. isTotal selects @@ (total) over @
// (per-unit). A soft-failed amt or cur propagates (amt first). cur may be a zero
// Key, in which case only the CurrencyHint is consulted.
func Price(b *Builder, amt Key[*csvkit.Amount], cur Key[string], isTotal bool) Key[*ast.PriceAnnotation] {
	return AddStep(b, func(c *MappingState) (*ast.PriceAnnotation, *ast.Diagnostic, error) {
		v, d := Value(c, amt)
		if d != nil {
			return nil, d, nil
		}
		if v == nil {
			return nil, nil, nil
		}
		currency := ""
		if !isZeroKey(cur) {
			cv, cd := Value(c, cur)
			if cd != nil {
				return nil, cd, nil
			}
			currency = strings.TrimSpace(cv)
		}
		if currency == "" {
			currency = v.CurrencyHint
		}
		if currency == "" {
			info := c.Info()
			diag := ErrorDiag(DiagMissingCurrency, info.Path, info.Line, "no currency resolved")
			return nil, &diag, nil
		}
		return &ast.PriceAnnotation{Amount: ast.Amount{Number: v.Number, Currency: currency}, IsTotal: isTotal}, nil, nil
	})
}

// RequireAmount soft-fails with code (default DiagAllBlankAmount) when in's value
// is nil, marking an absent amount where one is required (e.g. a primary posting
// leg). A non-nil value passes through unchanged; a soft-failed input propagates
// its existing diagnostic.
func RequireAmount(b *Builder, in Key[*csvkit.Amount], code string) Key[*csvkit.Amount] {
	if code == "" {
		code = DiagAllBlankAmount
	}
	return AddStep(b, func(c *MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
		v, d := Value(c, in)
		if d != nil {
			return nil, d, nil
		}
		if v == nil {
			info := c.Info()
			diag := ErrorDiag(code, info.Path, info.Line, "amount is absent")
			return nil, &diag, nil
		}
		return v, nil, nil
	})
}

// PostingSpec wires resolved keys into a single posting. Account is required (a
// zero Key panics in Posting); a blank account value soft-fails the posting with
// DiagMissingAccount. Amount, Cost, and Meta are optional: a zero Amount Key or a
// nil Amount value yields an auto-balanced posting (no amount); a zero Cost Key or
// a nil Cost value yields no cost. Flag selects the posting flag (0 = none).
type PostingSpec struct {
	Account Key[string]
	Amount  Key[*ast.Amount]
	Cost    Key[*ast.CostSpec]
	Price   Key[*ast.PriceAnnotation]
	Flag    byte
	Meta    []MetaField
}

// Posting assembles one ast.Posting from spec. It panics if Account is a zero
// Key. Sub-keys are read in the order account, amount, cost, price; the first
// soft-fail propagates and drops the posting (and thus its transaction). Meta
// fields are decorative: blank or soft-failed fields are skipped, never dropping
// the row.
func Posting(b *Builder, spec PostingSpec) Key[ast.Posting] {
	if isZeroKey(spec.Account) {
		panic("csvbase: Posting: Account key is zero")
	}
	return AddStep(b, func(c *MappingState) (ast.Posting, *ast.Diagnostic, error) {
		var zero ast.Posting
		account, d := Value(c, spec.Account)
		if d != nil {
			return zero, d, nil
		}
		if strings.TrimSpace(account) == "" {
			info := c.Info()
			diag := ErrorDiag(DiagMissingAccount, info.Path, info.Line, "no account resolved")
			return zero, &diag, nil
		}
		p := ast.Posting{Account: ast.Account(account), Flag: spec.Flag}
		if !isZeroKey(spec.Amount) {
			amt, d := Value(c, spec.Amount)
			if d != nil {
				return zero, d, nil
			}
			p.Amount = amt
		}
		if !isZeroKey(spec.Cost) {
			cost, d := Value(c, spec.Cost)
			if d != nil {
				return zero, d, nil
			}
			// avoid a non-nil CostHolder wrapping a nil *CostSpec
			if cost != nil {
				p.Cost = cost
			}
		}
		if !isZeroKey(spec.Price) {
			price, d := Value(c, spec.Price)
			if d != nil {
				return zero, d, nil
			}
			p.Price = price
		}
		if len(spec.Meta) > 0 {
			p.Meta = collectMeta(c, spec.Meta)
		}
		return p, nil, nil
	})
}

// Postings gathers the given posting keys into a slice, preserving order. The
// first soft-failed member propagates its diagnostic (dropping the transaction:
// a declared leg that could not be built makes the transaction invalid).
// Zero-Key members are skipped (optional legs). The result is a non-nil empty
// slice when no member contributes.
func Postings(b *Builder, ins ...Key[ast.Posting]) Key[[]ast.Posting] {
	return AddStep(b, func(c *MappingState) ([]ast.Posting, *ast.Diagnostic, error) {
		out := make([]ast.Posting, 0, len(ins))
		for _, k := range ins {
			if isZeroKey(k) {
				continue
			}
			p, d := Value(c, k)
			if d != nil {
				return nil, d, nil
			}
			out = append(out, p)
		}
		return out, nil, nil
	})
}

// DoubleEntry returns the posting list for a single-counterparty transaction:
// always primary, plus a balancing counter leg when counterAccount resolves to a
// non-blank value. A soft-failed primary propagates and drops the row. A
// soft-failed counterAccount is recorded as a warning via MappingState.Warn and
// yields a single-posting list (the row is kept); a blank counterAccount yields a
// single-posting list with no warning. The counter leg carries no amount when
// primary has a cost (cash-leg elision) or has no amount; otherwise it carries the
// negation of primary's amount.
func DoubleEntry(b *Builder, primary Key[ast.Posting], counterAccount Key[string]) Key[[]ast.Posting] {
	return AddStep(b, func(c *MappingState) ([]ast.Posting, *ast.Diagnostic, error) {
		p, d := Value(c, primary)
		if d != nil {
			return nil, d, nil
		}
		postings := make([]ast.Posting, 1, 2)
		postings[0] = p
		if isZeroKey(counterAccount) {
			return postings, nil, nil
		}
		acct, cd := Value(c, counterAccount)
		if cd != nil {
			c.Warn(*cd)
			return postings, nil, nil
		}
		if acct == "" {
			return postings, nil, nil
		}
		counter := ast.Posting{Account: ast.Account(acct)}
		if p.Cost == nil && p.Amount != nil {
			var neg apd.Decimal
			if _, err := apd.BaseContext.Neg(&neg, &p.Amount.Number); err != nil {
				info := c.Info()
				diag := ErrorDiag(DiagBadAmount, info.Path, info.Line,
					fmt.Sprintf("cannot negate amount for counter posting: %v", err))
				return nil, &diag, nil
			}
			counter.Amount = &ast.Amount{Number: neg, Currency: p.Amount.Currency}
		}
		postings = append(postings, counter)
		return postings, nil, nil
	})
}

// StringList collects the non-blank values of ins into a slice, preserving
// order. Soft-failed and blank inputs are skipped. It suits transaction tags and
// links. The result is nil when every input is blank or soft-failed.
func StringList(b *Builder, ins ...Key[string]) Key[[]string] {
	return AddStep(b, func(c *MappingState) ([]string, *ast.Diagnostic, error) {
		var out []string
		for _, k := range ins {
			if v, _ := Value(c, k); v != "" {
				out = append(out, v)
			}
		}
		return out, nil, nil
	})
}

// Meta builds an ast.Metadata from fields, stamping each non-blank value as an
// ast.MetaString under its Name. Soft-failed or blank fields are skipped
// (metadata is decorative and never drops the row). Props is nil when no field
// contributes.
func Meta(b *Builder, fields ...MetaField) Key[ast.Metadata] {
	return AddStep(b, func(c *MappingState) (ast.Metadata, *ast.Diagnostic, error) {
		return collectMeta(c, fields), nil, nil
	})
}

func collectMeta(c *MappingState, fields []MetaField) ast.Metadata {
	var meta ast.Metadata
	for _, mf := range fields {
		if isZeroKey(mf.Value) {
			continue
		}
		v, d := Value(c, mf.Value)
		if d != nil || v == "" {
			continue
		}
		if meta.Props == nil {
			meta.Props = make(map[string]ast.MetaValue)
		}
		meta.Props[mf.Name] = ast.MetaValue{Kind: ast.MetaString, String: v}
	}
	return meta
}

// TxnSpec wires resolved keys into a transaction. Date and Postings are required
// (a zero Key panics in Transaction). Flag defaults to '*' when 0. Payee,
// Narration, Tags, Links, and Meta are optional (zero Keys are ignored).
type TxnSpec struct {
	Date      Key[time.Time]
	Flag      byte
	Payee     Key[string]
	Narration Key[string]
	Tags      Key[[]string]
	Links     Key[[]string]
	Meta      Key[ast.Metadata]
	Postings  Key[[]ast.Posting]
}

// Transaction assembles a *ast.Transaction from spec. It panics if Date or
// Postings is a zero Key. A soft-failed Date, Narration, or Postings propagates
// and drops the row; an empty posting list soft-fails with DiagNoPostings. Payee,
// Tags, Links, and Meta soft-fails are swallowed (treated as absent).
func Transaction(b *Builder, spec TxnSpec) Key[*ast.Transaction] {
	if isZeroKey(spec.Date) {
		panic("csvbase: Transaction: Date key is zero")
	}
	if isZeroKey(spec.Postings) {
		panic("csvbase: Transaction: Postings key is zero")
	}
	flag := spec.Flag
	if flag == 0 {
		flag = '*'
	}
	return AddStep(b, func(c *MappingState) (*ast.Transaction, *ast.Diagnostic, error) {
		date, d := Value(c, spec.Date)
		if d != nil {
			return nil, d, nil
		}
		postings, d := Value(c, spec.Postings)
		if d != nil {
			return nil, d, nil
		}
		if len(postings) == 0 {
			info := c.Info()
			diag := ErrorDiag(DiagNoPostings, info.Path, info.Line, "transaction has no postings")
			return nil, &diag, nil
		}
		narration := ""
		if !isZeroKey(spec.Narration) {
			narration, d = Value(c, spec.Narration)
			if d != nil {
				return nil, d, nil
			}
		}
		payee := ""
		if !isZeroKey(spec.Payee) {
			payee, _ = Value(c, spec.Payee)
		}
		var tags, links []string
		if !isZeroKey(spec.Tags) {
			tags, _ = Value(c, spec.Tags)
		}
		if !isZeroKey(spec.Links) {
			links, _ = Value(c, spec.Links)
		}
		var meta ast.Metadata
		if !isZeroKey(spec.Meta) {
			meta, _ = Value(c, spec.Meta)
		}
		return &ast.Transaction{
			Date:      date,
			Flag:      flag,
			Payee:     payee,
			Narration: narration,
			Tags:      tags,
			Links:     links,
			Postings:  postings,
			Meta:      meta,
		}, nil, nil
	})
}

// EmitTx returns an EmitFunc that emits the transaction produced by k. It panics
// if k is a zero Key. A soft-failed transaction drops the row with its
// diagnostic; a nil value skips the row (no directive, no diagnostic); otherwise
// the transaction is emitted. Warnings recorded via MappingState.Warn during step
// evaluation are surfaced by the pipeline alongside the result.
func EmitTx(k Key[*ast.Transaction]) EmitFunc {
	if isZeroKey(k) {
		panic("csvbase: EmitTx: key is zero")
	}
	return func(_ context.Context, c *MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		tx, d := Value(c, k)
		if d != nil {
			return nil, []ast.Diagnostic{*d}, nil
		}
		if tx == nil {
			return nil, nil, nil
		}
		return []ast.Directive{tx}, nil, nil
	}
}

// BalanceSpec wires resolved keys into a balance assertion. Date, Account, and
// Amount are required (a zero Key for any of the three panics in Balance). Meta
// is optional.
type BalanceSpec struct {
	Date    Key[time.Time]
	Account Key[string]
	Amount  Key[*ast.Amount]
	Meta    []MetaField
}

// Balance assembles a *ast.Balance from spec. It panics if Date or Account is a
// zero Key. A blank account soft-fails with DiagMissingAccount; a nil amount
// soft-fails with DiagMissingAmount (a balance must assert an amount). A
// soft-failed sub-key propagates and drops the row.
func Balance(b *Builder, spec BalanceSpec) Key[*ast.Balance] {
	if isZeroKey(spec.Date) {
		panic("csvbase: Balance: Date key is zero")
	}
	if isZeroKey(spec.Account) {
		panic("csvbase: Balance: Account key is zero")
	}
	if isZeroKey(spec.Amount) {
		panic("csvbase: Balance: Amount key is zero")
	}
	return AddStep(b, func(c *MappingState) (*ast.Balance, *ast.Diagnostic, error) {
		date, d := Value(c, spec.Date)
		if d != nil {
			return nil, d, nil
		}
		account, d := Value(c, spec.Account)
		if d != nil {
			return nil, d, nil
		}
		if strings.TrimSpace(account) == "" {
			info := c.Info()
			diag := ErrorDiag(DiagMissingAccount, info.Path, info.Line, "no account resolved")
			return nil, &diag, nil
		}
		amt, d := Value(c, spec.Amount)
		if d != nil {
			return nil, d, nil
		}
		if amt == nil {
			info := c.Info()
			diag := ErrorDiag(DiagMissingAmount, info.Path, info.Line, "balance has no amount")
			return nil, &diag, nil
		}
		return &ast.Balance{
			Date:    date,
			Account: ast.Account(account),
			Amount:  *amt,
			Meta:    collectMeta(c, spec.Meta),
		}, nil, nil
	})
}

// AsDirective lifts a typed directive key into a Key[ast.Directive], so rows
// that produce different directive types (e.g. a transaction or a balance) can
// be unified — for instance selected by If — before EmitDirective. A zero T
// value lifts to a nil directive (not a non-nil interface wrapping a typed nil);
// a soft-fail propagates.
func AsDirective[T ast.Directive](b *Builder, k Key[T]) Key[ast.Directive] {
	return AddStep(b, func(c *MappingState) (ast.Directive, *ast.Diagnostic, error) {
		v, d := Value(c, k)
		if d != nil {
			return nil, d, nil
		}
		var zero T
		if any(v) == any(zero) {
			return nil, nil, nil
		}
		return v, nil, nil
	})
}

// EmitDirective returns an EmitFunc that emits the directive produced by k. It
// panics if k is a zero Key. A soft-failed directive drops the row with its
// diagnostic; a nil value skips the row; otherwise the directive is emitted.
// Warnings recorded via MappingState.Warn are surfaced by the pipeline alongside
// the result.
func EmitDirective(k Key[ast.Directive]) EmitFunc {
	if isZeroKey(k) {
		panic("csvbase: EmitDirective: key is zero")
	}
	return func(_ context.Context, c *MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		dir, d := Value(c, k)
		if d != nil {
			return nil, []ast.Diagnostic{*d}, nil
		}
		if dir == nil {
			return nil, nil, nil
		}
		return []ast.Directive{dir}, nil, nil
	}
}

// EmitDirectives returns an EmitFunc that emits, in order, the non-nil directive
// produced by each key. A nil-valued key contributes nothing — an emit slot that
// skips. If any key soft-fails, the whole row is dropped with the collected
// diagnostics and no directive is emitted: a partial directive set is never
// produced, since emitting only some of an intended group could leave the ledger
// inconsistent. With no keys, every row is skipped. Warnings recorded via
// MappingState.Warn are surfaced by the pipeline alongside the result. It panics
// if any key is a zero Key.
func EmitDirectives(ks ...Key[ast.Directive]) EmitFunc {
	for _, k := range ks {
		if isZeroKey(k) {
			panic("csvbase: EmitDirectives: key is zero")
		}
	}
	return func(_ context.Context, c *MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		var dirs []ast.Directive
		var diags []ast.Diagnostic
		for _, k := range ks {
			dir, d := Value(c, k)
			if d != nil {
				diags = append(diags, *d)
				continue
			}
			if dir == nil {
				continue
			}
			dirs = append(dirs, dir)
		}
		if len(diags) > 0 {
			return nil, diags, nil
		}
		return dirs, nil, nil
	}
}

// NilDirective returns a key whose value is always a nil directive — the neutral
// element for conditional emission. Selected by an If branch, it makes that
// branch contribute no directive (a skip) without a diagnostic.
func NilDirective(b *Builder) Key[ast.Directive] {
	return AddStep(b, func(*MappingState) (ast.Directive, *ast.Diagnostic, error) {
		return nil, nil, nil
	})
}
