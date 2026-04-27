package implicitprices

import (
	"context"
	"fmt"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// codeImplicitPriceError is the diagnostic code emitted when an
// arithmetic operation underlying a synthesized price fails. Upstream's
// ImplicitPriceError namedtuple carries no machine-readable category;
// we pick a stable kebab-case code so downstream tooling can match on
// it without parsing the human-readable message.
const codeImplicitPriceError = "implicit-price-error"

// quoContext is the apd context used for total → per-unit division
// when synthesizing prices from total-form cost or total-form price
// annotations. The package-wide [apd.BaseContext] has Precision=0,
// which only works for exact operations; division (Quo) needs a
// positive precision. 34 digits matches IEEE-754 decimal128 — the
// same precision chosen by [pkg/inventory] for its cost resolution —
// and is well above any practical ledger requirement.
var quoContext = apd.BaseContext.WithPrecision(34)

// Dual registration: upstream's Python module path (with underscore)
// and this package's Go import path (no underscore, since Go package
// identifiers cannot contain underscores). See doc.go for the
// rationale.
func init() {
	postproc.Register("beancount.plugins.implicit_prices", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/implicitprices", api.PluginFunc(apply))
}

// apply walks every directive once, copying it through to the output,
// and synthesizes one Price directive per posting that carries a price
// or cost annotation. See the package godoc for the full behavior and
// upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// Materialize once so we can produce a stable output slice and
	// detect the empty-input no-op.
	var all []ast.Directive
	for _, d := range in.Directives {
		all = append(all, d)
	}
	if len(all) == 0 {
		return api.Result{}, nil
	}

	// dedup tracks (date, base, quote, number) tuples already emitted
	// in this pass so we do not duplicate synthesized prices the way
	// upstream does. The number is included because upstream's
	// new_price_entry_map keys on it; comments call this dedup
	// strategy tentative but it is preserved here for byte-compatible
	// behavior with upstream. The number is keyed by its literal
	// textual form via apd.Decimal.Text('G') (which preserves trailing
	// zeros), so `1` and `1.00` are treated as distinct entries —
	// matching upstream's `dict[Decimal]` semantics, where two Decimal
	// instances with the same value but different exponents are not
	// equal as dict keys.
	type dedupKey struct {
		date   string
		base   string
		quote  string
		number string
	}
	dedup := make(map[dedupKey]struct{})

	var diags []ast.Diagnostic
	// Pre-grow: most transactions yield at most one price per posting.
	out := make([]ast.Directive, 0, len(all))
	for _, d := range all {
		// Poll cancellation per directive so large ledgers observe
		// context cancellation promptly rather than only at entry.
		if err := ctx.Err(); err != nil {
			return api.Result{}, err
		}
		out = append(out, d)
		tx, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		for i := range tx.Postings {
			p := &tx.Postings[i]
			pe, perr := buildPrice(tx, p)
			if perr != nil {
				diags = append(diags, *perr)
				continue
			}
			if pe == nil {
				continue
			}
			k := dedupKey{
				date:   pe.Date.Format("2006-01-02"),
				base:   pe.Commodity,
				quote:  pe.Amount.Currency,
				number: pe.Amount.Number.Text('G'),
			}
			if _, seen := dedup[k]; seen {
				continue
			}
			dedup[k] = struct{}{}
			out = append(out, pe)
		}
	}

	res := api.Result{Directives: out}
	if len(diags) != 0 {
		res.Diagnostics = diags
	}
	return res, nil
}

// buildPrice computes the synthesized Price directive for a single
// posting, or returns (nil, nil) if the posting carries no eligible
// price/cost annotation. Errors are returned as *ast.Diagnostic so the
// caller can append them to Result.Diagnostics without aborting the walk.
//
// The annotation preference matches upstream: an explicit price
// annotation ([ast.Posting.Price]) wins; a cost annotation
// ([ast.Posting.Cost]) is the fallback. Postings without a units
// [ast.Amount] or with a zero units number cannot produce a price
// (the latter would divide by zero in the total-form branches) and
// are skipped silently.
func buildPrice(tx *ast.Transaction, p *ast.Posting) (*ast.Price, *ast.Diagnostic) {
	if p.Amount == nil || p.Amount.Currency == "" {
		return nil, nil
	}

	// Per-unit price annotation (`@ X CUR`) is copied verbatim. Total
	// price (`@@ X CUR`) is divided by |units| to obtain per-unit.
	if p.Price != nil {
		if p.Price.Amount.Currency == "" {
			return nil, nil
		}
		num, err := perUnitNumber(p.Price.Amount.Number, p.Amount.Number, p.Price.IsTotal, tx, "price")
		if err != nil {
			return nil, err
		}
		if num == nil {
			return nil, nil
		}
		return &ast.Price{
			Span:      tx.Span,
			Date:      tx.Date,
			Commodity: p.Amount.Currency,
			Amount:    ast.Amount{Number: *num, Currency: p.Price.Amount.Currency},
		}, nil
	}

	// Cost annotation: per-unit form ({X CUR}) wins, total form
	// ({{T CUR}}) is divided by |units|. Combined form
	// ({X # T CUR}) is collapsed to per + T/|units|, matching the
	// resolution used by [pkg/inventory.ResolveCost]; upstream's
	// price emission uses cost.number, which the booking layer
	// derives the same way.
	if p.Cost != nil {
		num, cur, err := costPerUnit(p.Cost, p.Amount.Number, tx)
		if err != nil {
			return nil, err
		}
		if num == nil {
			return nil, nil
		}
		return &ast.Price{
			Span:      tx.Span,
			Date:      tx.Date,
			Commodity: p.Amount.Currency,
			Amount:    ast.Amount{Number: *num, Currency: cur},
		}, nil
	}

	return nil, nil
}

// perUnitNumber returns the per-unit decimal for a price annotation.
// When isTotal is false the input number is returned unchanged; when
// true it is divided by |units|. A zero units value returns (nil, nil)
// to signal "skip silently"; arithmetic failures return an *ast.Diagnostic.
func perUnitNumber(number, units apd.Decimal, isTotal bool, tx *ast.Transaction, source string) (*apd.Decimal, *ast.Diagnostic) {
	if !isTotal {
		out := new(apd.Decimal)
		out.Set(&number)
		return out, nil
	}
	if units.Sign() == 0 {
		return nil, nil
	}
	absUnits := new(apd.Decimal)
	if _, err := apd.BaseContext.Abs(absUnits, &units); err != nil {
		return nil, &ast.Diagnostic{
			Code:     codeImplicitPriceError,
			Span:     spanForTx(tx),
			Message:  fmt.Sprintf("abs of units in %s annotation: %v", source, err),
			Severity: ast.Error,
		}
	}
	out := new(apd.Decimal)
	if _, err := quoContext.Quo(out, &number, absUnits); err != nil {
		return nil, &ast.Diagnostic{
			Code:     codeImplicitPriceError,
			Span:     spanForTx(tx),
			Message:  fmt.Sprintf("divide total %s by units: %v", source, err),
			Severity: ast.Error,
		}
	}
	return out, nil
}

// costPerUnit derives the per-unit price number for a posting's Cost
// annotation. It accepts all four CostSpec shapes (per-unit only,
// total only, combined, empty); returns (nil, "", nil) for the empty
// case and for postings whose units would force a division by zero.
func costPerUnit(c *ast.CostSpec, units apd.Decimal, tx *ast.Transaction) (*apd.Decimal, string, *ast.Diagnostic) {
	switch {
	case c.PerUnit != nil && c.Total != nil:
		// Combined form: per + T/|units|. The AST contract
		// (pkg/ast/directives.go) guarantees both components share the
		// same currency in the combined form, so a mismatch here would
		// be a parser bug, not a user error — we treat it as
		// unreachable rather than emit a diagnostic the user cannot
		// act on.
		if units.Sign() == 0 {
			return nil, "", nil
		}
		absUnits := new(apd.Decimal)
		if _, err := apd.BaseContext.Abs(absUnits, &units); err != nil {
			return nil, "", &ast.Diagnostic{
				Code:     codeImplicitPriceError,
				Span:     spanForTx(tx),
				Message:  "abs of units in combined cost: " + err.Error(),
				Severity: ast.Error,
			}
		}
		quo := new(apd.Decimal)
		totalNum := c.Total.Number
		if _, err := quoContext.Quo(quo, &totalNum, absUnits); err != nil {
			return nil, "", &ast.Diagnostic{
				Code:     codeImplicitPriceError,
				Span:     spanForTx(tx),
				Message:  "divide total cost by units: " + err.Error(),
				Severity: ast.Error,
			}
		}
		out := new(apd.Decimal)
		perNum := c.PerUnit.Number
		if _, err := apd.BaseContext.Add(out, &perNum, quo); err != nil {
			return nil, "", &ast.Diagnostic{
				Code:     codeImplicitPriceError,
				Span:     spanForTx(tx),
				Message:  "add per-unit and residual cost: " + err.Error(),
				Severity: ast.Error,
			}
		}
		return out, c.PerUnit.Currency, nil
	case c.Total != nil:
		if units.Sign() == 0 {
			return nil, "", nil
		}
		absUnits := new(apd.Decimal)
		if _, err := apd.BaseContext.Abs(absUnits, &units); err != nil {
			return nil, "", &ast.Diagnostic{
				Code:     codeImplicitPriceError,
				Span:     spanForTx(tx),
				Message:  "abs of units in total cost: " + err.Error(),
				Severity: ast.Error,
			}
		}
		out := new(apd.Decimal)
		totalNum := c.Total.Number
		if _, err := quoContext.Quo(out, &totalNum, absUnits); err != nil {
			return nil, "", &ast.Diagnostic{
				Code:     codeImplicitPriceError,
				Span:     spanForTx(tx),
				Message:  "divide total cost by units: " + err.Error(),
				Severity: ast.Error,
			}
		}
		return out, c.Total.Currency, nil
	case c.PerUnit != nil:
		out := new(apd.Decimal)
		out.Set(&c.PerUnit.Number)
		return out, c.PerUnit.Currency, nil
	default:
		// Empty cost spec ({} or {{}}): no concrete number to record.
		return nil, "", nil
	}
}

// spanForTx returns the span to anchor a diagnostic on. The
// triggering transaction is the most actionable location; we fall
// back to the zero span when its Span is also zero.
func spanForTx(tx *ast.Transaction) ast.Span {
	if tx == nil {
		return ast.Span{}
	}
	return tx.Span
}
