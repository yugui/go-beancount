package predict

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/hook"
	"github.com/yugui/go-beancount/pkg/importer/importerutil"
)

// DiagAbstain is emitted as a Warning when a fillable single-leg transaction is
// left unbalanced because the predictor had no basis or did not clear the
// confidence and margin thresholds. Severity: ast.Warning.
const DiagAbstain = "predict-abstain"

// Hook fills the counter account of single-leg transactions from a k-NN model
// of an existing ledger. Its state is frozen at construction by the predict
// Factory; Apply is safe for concurrent invocation on the same value.
type Hook struct {
	name          string
	tok           Tokenizer
	fw            FieldWeights
	pred          Predictor
	minConfidence float64
	minMargin     float64
}

// Name returns the instance name supplied to the Factory that produced this Hook.
func (h *Hook) Name() string { return h.name }

// Apply replaces each single-leg *ast.Transaction (one posting with an amount)
// with its two-leg form when the predictor's confidence and margin clear the
// configured thresholds, using the predicted counter account and the source
// posting's currency. Transactions that miss the thresholds, or for which the
// predictor has no basis, emit a [DiagAbstain] Warning and pass through
// unchanged. All other directives pass through unchanged. Apply does not mutate
// in.Directives.
func (h *Hook) Apply(ctx context.Context, in hook.HookInput) (hook.HookResult, error) {
	if err := ctx.Err(); err != nil {
		return hook.HookResult{}, err
	}

	hasFillable := false
	for _, d := range in.Directives {
		if _, ok := fillableTxn(d); ok {
			hasFillable = true
			break
		}
	}
	if !hasFillable {
		return hook.HookResult{Directives: in.Directives}, nil
	}

	out := make([]ast.Directive, len(in.Directives))
	var diags []ast.Diagnostic
	for i, d := range in.Directives {
		if i > 0 && i%64 == 0 { // amortize ctx.Err cost
			if err := ctx.Err(); err != nil {
				return hook.HookResult{Directives: out[:i], Diagnostics: diags}, err
			}
		}
		tx, ok := fillableTxn(d)
		if !ok {
			out[i] = d
			continue
		}
		q := ExtractFeatures(tx, 0, h.tok, h.fw)
		pred, ok := h.pred.Predict(q)
		if !ok || pred.Confidence < h.minConfidence || pred.Margin < h.minMargin {
			out[i] = d
			diags = append(diags, abstainDiag(tx, pred, ok))
			continue
		}
		// "" → counterpart inherits the source posting's currency.
		out[i] = importerutil.BalanceWith(tx, string(pred.Account), "")
	}
	return hook.HookResult{Directives: out, Diagnostics: diags}, nil
}

// fillableTxn reports whether d is a single-leg transaction whose sole posting
// carries an amount (the precondition for predicting and filling a counterpart).
func fillableTxn(d ast.Directive) (*ast.Transaction, bool) {
	tx, ok := d.(*ast.Transaction)
	if !ok || len(tx.Postings) != 1 || tx.Postings[0].Amount == nil {
		return nil, false
	}
	return tx, true
}

func abstainDiag(tx *ast.Transaction, pred Prediction, ok bool) ast.Diagnostic {
	msg := fmt.Sprintf("no similar prior transaction (payee=%q narration=%q)", tx.Payee, tx.Narration)
	if ok {
		msg = fmt.Sprintf("counter account left unfilled: best=%s confidence=%.2f margin=%.2f below thresholds (payee=%q narration=%q)",
			pred.Account, pred.Confidence, pred.Margin, tx.Payee, tx.Narration)
	}
	return ast.Diagnostic{
		Code:     DiagAbstain,
		Span:     tx.Span,
		Message:  msg,
		Severity: ast.Warning,
	}
}
