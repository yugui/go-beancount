package predict_test

import (
	"fmt"
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
	d := mustDate(date)
	f.Date = d
	return predict.Example{Features: f, Label: ast.Account(label), Date: d}
}

func featDated(date, amountAbs, currency string, toks ...string) predict.Features {
	f := feat(amountAbs, currency, toks...)
	f.Date = mustDate(date)
	return f
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

// TestPredictRecencyBreaksConventionTie reproduces the production failure:
// the same payee accumulates equal numbers of "old" and "new" counter-account
// neighbors with cosine 1.0, which used to collapse the vote-based margin to 0.
// With the default recency-decayed vote, the newer convention wins and the
// margin is strictly positive.
func TestPredictRecencyBreaksConventionTie(t *testing.T) {
	cases := []struct {
		name      string
		oldLabel  string
		newLabel  string
		wantLabel string
	}{
		{"new_wins", "Expenses:Card", "Expenses:Coffee", "Expenses:Coffee"},
		{"reversed", "Expenses:Coffee", "Expenses:Card", "Expenses:Card"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var examples []predict.Example
			for i := 0; i < 5; i++ {
				examples = append(examples, example(c.oldLabel, fmt.Sprintf("2023-01-%02d", i+1),
					feat("", "", "payee:starbucks")))
			}
			for i := 0; i < 5; i++ {
				examples = append(examples, example(c.newLabel, fmt.Sprintf("2024-01-%02d", i+1),
					feat("", "", "payee:starbucks")))
			}
			query := featDated("2024-02-01", "", "", "payee:starbucks")

			got := mustPredict(t, predict.NewKNNPredictor(examples), query)
			if got.Account != ast.Account(c.wantLabel) {
				t.Errorf("Account = %q, want %q", got.Account, c.wantLabel)
			}
			if got.Margin <= 0 {
				t.Errorf("Margin = %v, want > 0 (recency decay should break the tie)", got.Margin)
			}
			if got.Confidence < 0.99 {
				t.Errorf("Confidence = %v, want ~1.0 (raw cosine is undecayed)", got.Confidence)
			}
		})
	}
}

// TestPredictRecencyDisabledRestoresLegacyTie verifies that turning the decay
// off (halfLife <= 0) restores the prior margin-collapse behavior, so the
// option is a true superset of the v1 logic.
func TestPredictRecencyDisabledRestoresLegacyTie(t *testing.T) {
	var examples []predict.Example
	for i := 0; i < 5; i++ {
		examples = append(examples, example("Expenses:Card", fmt.Sprintf("2023-01-%02d", i+1),
			feat("", "", "payee:starbucks")))
	}
	for i := 0; i < 5; i++ {
		examples = append(examples, example("Expenses:Coffee", fmt.Sprintf("2024-01-%02d", i+1),
			feat("", "", "payee:starbucks")))
	}
	query := featDated("2024-02-01", "", "", "payee:starbucks")

	got := mustPredict(t, predict.NewKNNPredictor(examples, predict.WithRecencyHalfLife(0)), query)
	if got.Margin != 0 {
		t.Errorf("Margin = %v, want 0 (decay disabled, legacy vote tie)", got.Margin)
	}
}

// TestPredictRecencyZeroQueryDateIsBackCompat verifies that a query with a
// zero Date (no recency information) behaves identically to the v1 predictor,
// so test fixtures and callers that do not supply a date are unaffected.
func TestPredictRecencyZeroQueryDateIsBackCompat(t *testing.T) {
	var examples []predict.Example
	for i := 0; i < 5; i++ {
		examples = append(examples, example("Expenses:Card", fmt.Sprintf("2023-01-%02d", i+1),
			feat("", "", "payee:starbucks")))
	}
	for i := 0; i < 5; i++ {
		examples = append(examples, example("Expenses:Coffee", fmt.Sprintf("2024-01-%02d", i+1),
			feat("", "", "payee:starbucks")))
	}
	query := feat("", "", "payee:starbucks") // no Date

	got := mustPredict(t, predict.NewKNNPredictor(examples), query)
	if got.Margin != 0 {
		t.Errorf("Margin = %v, want 0 (zero query Date disables decay)", got.Margin)
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
