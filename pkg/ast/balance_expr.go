package ast

import (
	"fmt"
	"strings"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/syntax"
)

// ParseAmountExpression parses a single beancount amount expression of
// the form `<arith-expr> <CURRENCY>` (e.g. "100 USD", "1,000 + 500 USD",
// "(100+200)*1.05 USD"). It is the public entry point for callers that
// want to consume amount text outside the directive grammar — most
// notably postproc plugins whose config or Custom directive bodies
// carry user-authored amount strings.
//
// On success the returned Diagnostics slice is empty; on failure the
// returned Amount is the zero value and Diagnostics carry stable codes.
// Diagnostic Spans are anchored at byte offsets within s (Start.Filename
// empty, Line and Column zero); the caller rebases them onto the
// enclosing CST/AST node before surfacing them on the ledger.
//
// Diagnostic codes:
//   - "amount-expr-parse"        underlying syntax parse failure
//   - "amount-expr-eval"         arithmetic evaluation failure (divide by zero, overflow)
//   - "amount-missing-currency"  no CURRENCY token after the expression
//   - "amount-trailing-input"    unconsumed tokens after the currency
func ParseAmountExpression(s string) (Amount, []Diagnostic) {
	if strings.TrimSpace(s) == "" {
		return Amount{}, []Diagnostic{{
			Code:     "amount-expr-parse",
			Span:     Span{Start: Position{Offset: 0}, End: Position{Offset: len(s)}},
			Message:  "empty amount expression",
			Severity: Error,
		}}
	}

	node, errs := syntax.ParseAmountExpression(s)
	exprNodes := node.FindAllNodes(syntax.ArithExprNode)
	currTok := node.FindToken(syntax.CURRENCY)

	diags := classifyStructuralErrors(s, errs, currTok)
	if len(diags) > 0 {
		return Amount{}, diags
	}

	// parser guarantees at least one ArithExprNode when no structural errors are present
	num, d := evalArithExpr(exprNodes[0])
	if d != nil {
		d.Code = "amount-expr-eval"
		return Amount{}, []Diagnostic{*d}
	}
	return Amount{Number: num, Currency: currTok.Raw}, nil
}

// ParseBalanceAmount parses a balance directive body of the form
// `<arith-expr> [~ <arith-expr>] <CURRENCY>`. Tolerance is non-nil iff
// the input contained a `~ <expr>` clause, in which case the tolerance
// shares Amount.Currency (it has no independent currency in beancount
// syntax).
//
// Adds the diagnostic code:
//   - "balance-tolerance-eval"   tolerance expression failed to evaluate
func ParseBalanceAmount(s string) (amount Amount, tolerance *apd.Decimal, diags []Diagnostic) {
	if strings.TrimSpace(s) == "" {
		return Amount{}, nil, []Diagnostic{{
			Code:     "amount-expr-parse",
			Span:     Span{Start: Position{Offset: 0}, End: Position{Offset: len(s)}},
			Message:  "empty amount expression",
			Severity: Error,
		}}
	}

	node, errs := syntax.ParseBalanceAmount(s)
	exprNodes := node.FindAllNodes(syntax.ArithExprNode)
	currTok := node.FindToken(syntax.CURRENCY)
	tildeTok := node.FindToken(syntax.TILDE)

	if diags := classifyStructuralErrors(s, errs, currTok); len(diags) > 0 {
		return Amount{}, nil, diags
	}

	// parser guarantees at least one ArithExprNode when no structural errors are present
	num, d := evalArithExpr(exprNodes[0])
	if d != nil {
		d.Code = "amount-expr-eval"
		return Amount{}, nil, []Diagnostic{*d}
	}

	amt := Amount{Number: num, Currency: currTok.Raw}

	if tildeTok != nil && len(exprNodes) >= 2 {
		tol, d := evalArithExpr(exprNodes[1])
		if d != nil {
			d.Code = "balance-tolerance-eval"
			return Amount{}, nil, []Diagnostic{*d}
		}
		return amt, &tol, nil
	}
	return amt, nil, nil
}

// classifyStructuralErrors maps the syntax parser's errors onto the
// stable diagnostic-code vocabulary. The classification picks at most
// one "structural" code (trailing-input wins over missing-currency,
// because tokens after the currency typically explain why a currency
// wasn't found in the expected position); any other parser error is
// surfaced as the generic "amount-expr-parse". When no parser error is
// present but the CURRENCY token is missing, "amount-missing-currency"
// is emitted alone.
//
// Suppression rules (mirroring lowerer.hasParserErrorIn):
//   - the synthetic "unexpected trailing input" marker is consumed,
//     not re-emitted as a parse error;
//   - the parser's own "unexpected token X after balance amount" message
//     IS the trailing-input case (the body parser consumes garbage
//     before the outer wrapper can spot it) — folded into trailing-input;
//   - the "expected CURRENCY" error is dropped whenever a parse error
//     earlier in the stream is the real cause (avoids double-reporting
//     `"100 + USD"` as both missing-currency and a parse error).
func classifyStructuralErrors(s string, errs []syntax.Error, currTok *syntax.Token) []Diagnostic {
	trailingOff, hasTrailing := -1, false
	for _, e := range errs {
		if e.Msg == "unexpected trailing input" || strings.HasPrefix(e.Msg, "unexpected token") {
			trailingOff, hasTrailing = e.Pos, true
			break
		}
	}

	var parseErrs []syntax.Error
	for _, e := range errs {
		if e.Msg == "unexpected trailing input" || strings.HasPrefix(e.Msg, "unexpected token") {
			continue
		}
		if strings.HasPrefix(e.Msg, "expected CURRENCY") {
			continue
		}
		if hasTrailing && e.Pos >= trailingOff {
			continue
		}
		parseErrs = append(parseErrs, e)
	}

	var diags []Diagnostic
	for _, e := range parseErrs {
		diags = append(diags, Diagnostic{
			Code:     "amount-expr-parse",
			Span:     Span{Start: Position{Offset: e.Pos}, End: Position{Offset: e.Pos}},
			Message:  e.Msg,
			Severity: Error,
		})
	}
	switch {
	case hasTrailing:
		diags = append(diags, Diagnostic{
			Code:     "amount-trailing-input",
			Span:     Span{Start: Position{Offset: trailingOff}, End: Position{Offset: len(s)}},
			Message:  "unexpected trailing input",
			Severity: Error,
		})
	case currTok == nil && len(parseErrs) == 0:
		diags = append(diags, Diagnostic{
			Code:     "amount-missing-currency",
			Span:     Span{Start: Position{Offset: 0}, End: Position{Offset: len(s)}},
			Message:  "amount expression missing currency",
			Severity: Error,
		})
	}
	return diags
}

// evalArithExpr evaluates an ArithExprNode into an apd.Decimal. The
// returned *Diagnostic is non-nil iff evaluation failed; its Code is
// empty so the caller can stamp the contextual code
// ("amount-expr-eval" vs "balance-tolerance-eval"). Spans are anchored
// at the source byte offsets of n's tokens.
func evalArithExpr(n *syntax.Node) (apd.Decimal, *Diagnostic) {
	var nodes []*syntax.Node
	var tokens []*syntax.Token
	for _, c := range n.Children {
		if c.Node != nil {
			nodes = append(nodes, c.Node)
		}
		if c.Token != nil {
			tokens = append(tokens, c.Token)
		}
	}

	// Case 1: single NUMBER token (primary)
	if len(nodes) == 0 && len(tokens) == 1 && tokens[0].Kind == syntax.NUMBER {
		d, err := parseNumberToken(tokens[0])
		if err != nil {
			return apd.Decimal{}, &Diagnostic{
				Span:     spanOfNode(n),
				Message:  err.Error(),
				Severity: Error,
			}
		}
		return d, nil
	}

	// Case 2: parenthesized (LPAREN expr RPAREN)
	if len(nodes) == 1 && len(tokens) == 2 && tokens[0].Kind == syntax.LPAREN {
		return evalArithExpr(nodes[0])
	}

	// Case 3: unary prefix (PLUS/MINUS + expr)
	if len(nodes) == 1 && len(tokens) == 1 {
		op := tokens[0]
		if op.Kind == syntax.PLUS || op.Kind == syntax.MINUS {
			val, d := evalArithExpr(nodes[0])
			if d != nil {
				return apd.Decimal{}, d
			}
			if op.Kind == syntax.MINUS {
				val.Negative = !val.Negative
			}
			return val, nil
		}
	}

	// Case 4: binary (expr op expr)
	if len(nodes) == 2 && len(tokens) == 1 {
		op := tokens[0]
		left, d := evalArithExpr(nodes[0])
		if d != nil {
			return apd.Decimal{}, d
		}
		right, d := evalArithExpr(nodes[1])
		if d != nil {
			return apd.Decimal{}, d
		}
		var result apd.Decimal
		var err error
		switch op.Kind {
		case syntax.PLUS:
			_, err = arithCtx.Add(&result, &left, &right)
		case syntax.MINUS:
			_, err = arithCtx.Sub(&result, &left, &right)
		case syntax.STAR:
			_, err = arithCtx.Mul(&result, &left, &right)
		case syntax.SLASH:
			var quo *apd.Decimal
			if quo, err = QuoNormalized(arithCtx, &left, &right); err == nil {
				result.Set(quo)
			}
		default:
			return apd.Decimal{}, &Diagnostic{
				Span:     spanOfNode(n),
				Message:  fmt.Sprintf("unexpected operator %s in expression", op.Kind),
				Severity: Error,
			}
		}
		if err != nil {
			return apd.Decimal{}, &Diagnostic{
				Span:     spanOfNode(n),
				Message:  fmt.Sprintf("arithmetic error: %v", err),
				Severity: Error,
			}
		}
		return result, nil
	}

	return apd.Decimal{}, &Diagnostic{
		Span:     spanOfNode(n),
		Message:  "malformed arithmetic expression",
		Severity: Error,
	}
}

// QuoNormalized divides dividend by divisor using ctx and rewrites an exact
// quotient to the exponent beancount/Python decimal produces: the General
// Decimal Arithmetic ideal exponent (dividend.Exponent - divisor.Exponent),
// without dropping fractional digits the value genuinely needs. apd's Quo pads
// exact quotients to ctx's precision (e.g. 10000.00/2 -> 5000.000...0, 34
// digits); leaving that inflated exponent in place is display noise and
// corrupts the per-currency tolerance pkg/validation/tolerance infers from the
// exponent.
//
// An inexact quotient (a non-terminating expansion like 10/3) has no shorter
// exact form, so it is returned at ctx's full precision unchanged. The returned
// Decimal is freshly allocated. The error is non-nil only if ctx rejects the
// divide, reduce, or quantize — for division this includes divide-by-zero;
// the trailing reduce/quantize cannot fail on a quotient ctx already produced
// within precision.
func QuoNormalized(ctx *apd.Context, dividend, divisor *apd.Decimal) (*apd.Decimal, error) {
	q := new(apd.Decimal)
	cond, err := ctx.Quo(q, dividend, divisor)
	if err != nil {
		return nil, err
	}
	if cond.Inexact() {
		return q, nil
	}
	idealExp := dividend.Exponent - divisor.Exponent
	if _, _, err := ctx.Reduce(q, q); err != nil {
		return nil, err
	}
	if q.Exponent > idealExp {
		if _, err := ctx.Quantize(q, q, idealExp); err != nil {
			return nil, err
		}
	}
	return q, nil
}

// parseNumberToken parses a NUMBER token into apd.Decimal. NUMBER tokens
// may contain commas (e.g., "1,234.56").
func parseNumberToken(t *syntax.Token) (apd.Decimal, error) {
	s := strings.ReplaceAll(t.Raw, ",", "")
	var d apd.Decimal
	_, _, err := d.SetString(s)
	if err != nil {
		return apd.Decimal{}, fmt.Errorf("invalid number %q: %w", t.Raw, err)
	}
	return d, nil
}

// spanOfNode is the offset-only counterpart to lowerer.spanFromNode:
// it covers the byte range of n's first to last token, leaving Line and
// Column at zero so callers can rebase the position.
func spanOfNode(n *syntax.Node) Span {
	var first, last *syntax.Token
	for t := range n.Tokens() {
		if first == nil {
			first = t
		}
		last = t
	}
	if first == nil {
		return Span{}
	}
	return Span{
		Start: Position{Offset: first.Pos},
		End:   Position{Offset: last.Pos + len(last.Raw)},
	}
}
