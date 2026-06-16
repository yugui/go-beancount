package predict_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/hook"
	"github.com/yugui/go-beancount/pkg/importer/hook/std/predict"
	"github.com/yugui/go-beancount/pkg/loader"
	"github.com/yugui/go-beancount/pkg/printer"
)

// TestLeaveOneOutAccuracy is the accuracy harness: it holds out each training
// example, predicts it from the rest under the default thresholds, and asserts
// coverage and accuracy floors. It doubles as a regression guard and a place to
// retune thresholds/weights against a representative fixture.
func TestLeaveOneOutAccuracy(t *testing.T) {
	led, err := loader.LoadFile(context.Background(), "testdata/eval.beancount")
	if err != nil {
		t.Fatalf("load eval ledger: %v", err)
	}
	tok := predict.NewDefaultTokenizer()
	examples := predict.ExtractExamples(led, tok, predict.DefaultFieldWeights())
	if len(examples) < 15 {
		t.Fatalf("too few examples for a meaningful eval: %d", len(examples))
	}

	const minConfidence, minMargin = 0.30, 0.10
	var predicted, correct int
	for i := range examples {
		rest := make([]predict.Example, 0, len(examples)-1)
		rest = append(rest, examples[:i]...)
		rest = append(rest, examples[i+1:]...)

		pred, ok := predict.NewKNNPredictor(rest).Predict(examples[i].Features)
		if !ok || pred.Confidence < minConfidence || pred.Margin < minMargin {
			continue
		}
		predicted++
		if pred.Account == examples[i].Label {
			correct++
		}
	}

	total := len(examples)
	coverage := float64(predicted) / float64(total)
	accuracy := 0.0
	if predicted > 0 {
		accuracy = float64(correct) / float64(predicted)
	}
	t.Logf("leave-one-out: total=%d predicted=%d correct=%d coverage=%.2f accuracy=%.2f",
		total, predicted, correct, coverage, accuracy)

	if coverage < 0.70 {
		t.Errorf("coverage = %.2f, want >= 0.70", coverage)
	}
	if accuracy < 0.90 {
		t.Errorf("accuracy = %.2f, want >= 0.90", accuracy)
	}
}

// TestApplyByteDeterministic guards the "re-import yields identical output"
// property: the same batch rendered twice must be byte-for-byte identical.
func TestApplyByteDeterministic(t *testing.T) {
	h := newPredictHook(t, ledgerConfig)
	batch := func() []ast.Directive {
		return []ast.Directive{
			singleLeg("Starbucks", "Morning coffee", "-4.50"),
			singleLeg("Whole Foods", "weekly groceries", "-50.00"),
			singleLeg("Xyzzy", "unknown", "-7.00"),
		}
	}
	render := func() string {
		res, err := h.Apply(context.Background(), hook.HookInput{Directives: batch()})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, res.Directives); err != nil {
			t.Fatalf("Fprint: %v", err)
		}
		return buf.String()
	}
	first := render()
	for i := 0; i < 5; i++ {
		if got := render(); got != first {
			t.Fatalf("non-deterministic output:\n--- first ---\n%s\n--- got ---\n%s", first, got)
		}
	}
}
