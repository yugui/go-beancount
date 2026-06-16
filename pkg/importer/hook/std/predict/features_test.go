package predict_test

import (
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/hook/std/predict"
)

var cmpOpts = cmp.Options{
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
}

// splitTok is a whitespace tokenizer used to isolate feature-extraction logic
// from the default tokenizer's normalization.
type splitTok struct{}

func (splitTok) Tokenize(s string) []string { return strings.Fields(s) }

func mustDecimal(s string) apd.Decimal {
	d, _, err := apd.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return *d
}

func decPtr(s string) *apd.Decimal {
	d := mustDecimal(s)
	return &d
}

func amt(num, cur string) *ast.Amount {
	return &ast.Amount{Number: mustDecimal(num), Currency: cur}
}

func TestExtractFeatures(t *testing.T) {
	txn := &ast.Transaction{
		Payee:     "Acme Store",
		Narration: "coffee beans",
		Tags:      []string{"trip"},  // must be ignored
		Links:     []string{"inv-1"}, // must be ignored
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"note": {Kind: ast.MetaString, String: "morning"},
			"ref":  {Kind: ast.MetaNumber, Number: mustDecimal("7")}, // non-string: ignored
		}},
		Postings: []ast.Posting{
			{Account: "Assets:Bank:Checking", Amount: amt("-5.00", "USD")},
			{Account: "Expenses:Coffee", Amount: amt("5.00", "USD")},
		},
	}

	got := predict.ExtractFeatures(txn, 0, splitTok{}, predict.DefaultFieldWeights())

	want := predict.Features{
		Terms: []predict.Term{
			{Token: "acct:Assets", Weight: 0.75},
			{Token: "acct:Assets:Bank", Weight: 0.75},
			{Token: "acct:Assets:Bank:Checking", Weight: 0.75},
			{Token: "meta.note:morning", Weight: 0.5},
			{Token: "narr:beans", Weight: 1.5},
			{Token: "narr:coffee", Weight: 1.5},
			{Token: "payee:Acme", Weight: 3.0},
			{Token: "payee:Store", Weight: 3.0},
			{Token: "sign:credit", Weight: 0.5},
		},
		AmountAbs: decPtr("5.00"),
		Currency:  "USD",
		Sign:      predict.SignCredit,
	}
	if diff := cmp.Diff(want, got, cmpOpts); diff != "" {
		t.Errorf("ExtractFeatures (-want +got):\n%s", diff)
	}
}

func TestExtractFeaturesRepeatedTokenSumsWeight(t *testing.T) {
	txn := &ast.Transaction{
		Payee:    "amazon amazon",
		Postings: []ast.Posting{{Account: "Assets:Cash", Amount: amt("1.00", "USD")}},
	}
	got := predict.ExtractFeatures(txn, 0, splitTok{}, predict.FieldWeights{Payee: 2.0})
	want := []predict.Term{{Token: "payee:amazon", Weight: 4.0}}
	if diff := cmp.Diff(want, got.Terms, cmpOpts); diff != "" {
		t.Errorf("repeated token weight (-want +got):\n%s", diff)
	}
}

func TestExtractFeaturesSign(t *testing.T) {
	cases := []struct {
		name    string
		amount  *ast.Amount
		want    predict.Sign
		signTok string
	}{
		{"debit", amt("9.99", "USD"), predict.SignDebit, "sign:debit"},
		{"credit", amt("-9.99", "USD"), predict.SignCredit, "sign:credit"},
		{"zero", amt("0", "USD"), predict.SignZero, "sign:zero"},
		{"absent", nil, predict.SignZero, "sign:zero"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			txn := &ast.Transaction{
				Postings: []ast.Posting{{Account: "Assets:Cash", Amount: c.amount}},
			}
			got := predict.ExtractFeatures(txn, 0, splitTok{}, predict.DefaultFieldWeights())
			if got.Sign != c.want {
				t.Errorf("Sign = %v, want %v", got.Sign, c.want)
			}
			hasToken := func(tok string) bool {
				for _, term := range got.Terms {
					if term.Token == tok {
						return true
					}
				}
				return false
			}
			if !hasToken(c.signTok) {
				t.Errorf("missing sign token %q", c.signTok)
			}
			if c.amount == nil && got.AmountAbs != nil {
				t.Errorf("AmountAbs = %v, want nil for absent amount", got.AmountAbs)
			}
		})
	}
}

func TestExtractFeaturesZeroWeightSuppresses(t *testing.T) {
	txn := &ast.Transaction{
		Payee:     "Acme",
		Narration: "x",
		Meta:      ast.Metadata{Props: map[string]ast.MetaValue{"n": {Kind: ast.MetaString, String: "y"}}},
		Postings:  []ast.Posting{{Account: "Assets:Cash", Amount: amt("-1", "USD")}},
	}
	// Only Payee enabled; metadata, account, sign, narration all suppressed.
	fw := predict.FieldWeights{Payee: 1.0}
	got := predict.ExtractFeatures(txn, 0, splitTok{}, fw)

	want := []predict.Term{{Token: "payee:Acme", Weight: 1.0}}
	if diff := cmp.Diff(want, got.Terms, cmpOpts); diff != "" {
		t.Errorf("zero-weight suppression (-want +got):\n%s", diff)
	}
	// Sign is carried out of band regardless of the Sign weight.
	if got.Sign != predict.SignCredit {
		t.Errorf("Sign = %v, want SignCredit", got.Sign)
	}
}

func TestExtractFeaturesRootOnlyAccount(t *testing.T) {
	txn := &ast.Transaction{
		Postings: []ast.Posting{{Account: ast.Assets, Amount: amt("1", "USD")}},
	}
	got := predict.ExtractFeatures(txn, 0, splitTok{}, predict.FieldWeights{Account: 1.0})
	want := []predict.Term{{Token: "acct:Assets", Weight: 1.0}}
	if diff := cmp.Diff(want, got.Terms, cmpOpts); diff != "" {
		t.Errorf("root-only acct (-want +got):\n%s", diff)
	}
}

func TestExtractFeaturesPanicsOnBadIndex(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("expected panic on out-of-range knownIdx")
		}
	}()
	txn := &ast.Transaction{Postings: []ast.Posting{{Account: "Assets:Cash", Amount: amt("1", "USD")}}}
	predict.ExtractFeatures(txn, 5, splitTok{}, predict.DefaultFieldWeights())
}

func TestExtractFeaturesDeterministic(t *testing.T) {
	txn := &ast.Transaction{
		Payee: "Acme",
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"z": {Kind: ast.MetaString, String: "z"},
			"a": {Kind: ast.MetaString, String: "a"},
			"m": {Kind: ast.MetaString, String: "m"},
		}},
		Postings: []ast.Posting{{Account: "Assets:Bank", Amount: amt("1", "USD")}},
	}
	first := predict.ExtractFeatures(txn, 0, splitTok{}, predict.DefaultFieldWeights())
	for i := 0; i < 20; i++ {
		got := predict.ExtractFeatures(txn, 0, splitTok{}, predict.DefaultFieldWeights())
		if diff := cmp.Diff(first, got, cmpOpts); diff != "" {
			t.Fatalf("ExtractFeatures nondeterministic on repeat (-first +got):\n%s", diff)
		}
	}
}

func TestExtractFeaturesDefaultTokenizerIntegration(t *testing.T) {
	txn := &ast.Transaction{
		Payee:    "スターバックス",
		Postings: []ast.Posting{{Account: "Assets:Cash", Amount: amt("-3", "USD")}},
	}
	got := predict.ExtractFeatures(txn, 0, predict.NewDefaultTokenizer(), predict.FieldWeights{Payee: 1.0})
	// Every payee token must be namespaced; the CJK run must produce bigrams.
	var hasBigram bool
	for _, term := range got.Terms {
		if !strings.HasPrefix(term.Token, "payee:") {
			t.Errorf("token %q lacks payee: namespace", term.Token)
		}
		if len([]rune(strings.TrimPrefix(term.Token, "payee:"))) == 2 {
			hasBigram = true
		}
	}
	if !hasBigram {
		t.Errorf("expected at least one CJK bigram token, got %v", got.Terms)
	}
}
