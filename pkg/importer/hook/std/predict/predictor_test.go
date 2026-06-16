package predict_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/hook/std/predict"
)

func feat(amountAbs, currency string, toks ...string) predict.Features {
	terms := make([]predict.Term, len(toks))
	for i, tk := range toks {
		terms[i] = predict.Term{Token: tk, Weight: 1}
	}
	f := predict.Features{Terms: terms}
	if amountAbs != "" {
		f.AmountAbs = decPtr(amountAbs)
		f.Currency = currency
	}
	return f
}

func example(label, date string, f predict.Features) predict.Example {
	return predict.Example{Features: f, Label: ast.Account(label), Date: mustDate(date)}
}

func mustPredict(t *testing.T, p predict.Predictor, q predict.Features) predict.Prediction {
	t.Helper()
	pred, ok := p.Predict(q)
	if !ok {
		t.Fatalf("Predict abstained, want a prediction")
	}
	return pred
}

func TestPredictTopMatch(t *testing.T) {
	p := predict.NewKNNPredictor([]predict.Example{
		example("Expenses:Coffee", "2024-01-01", feat("", "", "payee:starbucks", "payee:coffee")),
		example("Expenses:Groceries", "2024-01-02", feat("", "", "payee:wholefoods", "payee:grocery")),
	})
	got, ok := p.Predict(feat("", "", "payee:starbucks", "payee:coffee"))
	if !ok {
		t.Fatalf("Predict ok=false, want a prediction")
	}
	if got.Account != "Expenses:Coffee" {
		t.Errorf("Account = %q, want Expenses:Coffee", got.Account)
	}
	if got.Confidence <= 0.99 {
		t.Errorf("Confidence = %v, want ~1.0 for an identical match", got.Confidence)
	}
	if !got.Evidence.Date.Equal(mustDate("2024-01-01")) {
		t.Errorf("Evidence.Date = %v, want 2024-01-01", got.Evidence.Date)
	}
}

func TestPredictEmptyCorpus(t *testing.T) {
	p := predict.NewKNNPredictor(nil)
	if _, ok := p.Predict(feat("", "", "payee:x")); ok {
		t.Errorf("Predict ok=true on empty corpus, want false")
	}
}

func TestPredictNoSharedVocab(t *testing.T) {
	p := predict.NewKNNPredictor([]predict.Example{
		example("Expenses:Coffee", "2024-01-01", feat("", "", "payee:starbucks")),
	})
	if _, ok := p.Predict(feat("", "", "payee:unseen")); ok {
		t.Errorf("Predict ok=true with no shared vocabulary, want false")
	}
}

func TestPredictExactAmountBonus(t *testing.T) {
	examples := []predict.Example{
		example("Expenses:Coffee", "2024-01-01", feat("3.00", "USD", "payee:cafe")),
		example("Expenses:Tea", "2024-01-01", feat("5.00", "USD", "payee:cafe")),
	}
	query := feat("5.00", "USD", "payee:cafe")

	// With the default bonus, the exact 5.00 USD match tips the tie to Tea.
	got := mustPredict(t, predict.NewKNNPredictor(examples), query)
	if got.Account != "Expenses:Tea" {
		t.Errorf("with bonus Account = %q, want Expenses:Tea", got.Account)
	}

	// With the bonus disabled the text tie breaks deterministically (lexical).
	got0 := mustPredict(t, predict.NewKNNPredictor(examples, predict.WithExactAmountBonus(0)), query)
	if got0.Account != "Expenses:Coffee" {
		t.Errorf("no bonus Account = %q, want Expenses:Coffee (lexical tie-break)", got0.Account)
	}
}

func TestPredictMinSupport(t *testing.T) {
	// Solo is a single near-perfect match; Multi has two diluted matches.
	examples := []predict.Example{
		example("Expenses:Solo", "2024-01-01", feat("", "", "payee:y")),
		example("Expenses:Multi", "2024-01-02", feat("", "", "payee:y", "a", "b", "c", "d")),
		example("Expenses:Multi", "2024-01-03", feat("", "", "payee:y", "a", "b", "c", "d")),
	}
	query := feat("", "", "payee:y")

	got := mustPredict(t, predict.NewKNNPredictor(examples), query)
	if got.Account != "Expenses:Solo" {
		t.Errorf("minSupport=1 Account = %q, want Expenses:Solo", got.Account)
	}

	got2 := mustPredict(t, predict.NewKNNPredictor(examples, predict.WithMinSupport(2)), query)
	if got2.Account != "Expenses:Multi" {
		t.Errorf("minSupport=2 Account = %q, want Expenses:Multi (Solo excluded)", got2.Account)
	}
}

func TestPredictMargin(t *testing.T) {
	// Single account in contention → margin 1.0.
	solo := predict.NewKNNPredictor([]predict.Example{
		example("Expenses:Only", "2024-01-01", feat("", "", "payee:z")),
	})
	got := mustPredict(t, solo, feat("", "", "payee:z"))
	if got.Margin != 1.0 {
		t.Errorf("single-account Margin = %v, want 1.0", got.Margin)
	}

	// Two equally-similar accounts → margin 0.
	tie := predict.NewKNNPredictor([]predict.Example{
		example("Expenses:A", "2024-01-01", feat("", "", "payee:z")),
		example("Expenses:B", "2024-01-01", feat("", "", "payee:z")),
	}, predict.WithExactAmountBonus(0))
	gotTie := mustPredict(t, tie, feat("", "", "payee:z"))
	if gotTie.Margin != 0 {
		t.Errorf("tie Margin = %v, want 0", gotTie.Margin)
	}
}

func TestPredictDeterministic(t *testing.T) {
	examples := []predict.Example{
		example("Expenses:A", "2024-01-01", feat("", "", "payee:z")),
		example("Expenses:B", "2024-01-01", feat("", "", "payee:z")),
		example("Expenses:C", "2024-01-02", feat("", "", "payee:z", "payee:w")),
	}
	p := predict.NewKNNPredictor(examples)
	first := mustPredict(t, p, feat("", "", "payee:z"))
	for i := 0; i < 50; i++ {
		got := mustPredict(t, p, feat("", "", "payee:z"))
		if diff := cmp.Diff(first, got, cmpOpts); diff != "" {
			t.Fatalf("Predict nondeterministic on repeat (-first +got):\n%s", diff)
		}
	}
}
