package predict

import (
	"sort"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// Sign classifies the direction of a posting's amount: debit (positive),
// credit (negative), or zero (zero or absent). It is the domain signal the
// predictor matches on so that, e.g., a credit on the known account
// preferentially predicts an expense rather than an income counter account.
type Sign int8

const (
	// SignZero is the direction of a zero or absent amount.
	SignZero Sign = iota
	// SignDebit is the direction of a strictly positive amount.
	SignDebit
	// SignCredit is the direction of a strictly negative amount.
	SignCredit
)

// Term is one namespaced feature token with its accumulated weight. Token is a
// field-namespace prefix (payee: / narr: / meta.<k>: / acct: / sign:) followed
// by a raw tokenizer term. Weight is the sum of the per-occurrence field
// weights for that token within one transaction and is always > 0.
type Term struct {
	Token  string
	Weight float64
}

// Features is the deterministic, namespaced weighted-token view of a
// transaction seen from the vantage of one known posting.
//
// Terms is sorted by Token (byte order) with no duplicate Token; it is the bag
// the predictor vectorizes for TF-IDF / cosine similarity. The amount signal is
// carried out of band: AmountAbs (the absolute value of the known posting's
// amount, nil when absent), Currency (the known posting's currency, or ""), and
// Sign are not encoded as Terms — except the derived sign: token — so the
// magnitude never pollutes the text vector.
type Features struct {
	Terms     []Term
	AmountAbs *apd.Decimal
	Currency  string
	Sign      Sign
}

// FieldWeights assigns a per-field multiplier to the tokens each transaction
// field contributes during extraction. A zero weight suppresses that field
// entirely. Account scales every acct: ancestor-prefix token; Sign scales the
// single sign: token.
type FieldWeights struct {
	Payee     float64
	Narration float64
	Metadata  float64
	Account   float64
	Sign      float64
}

// DefaultFieldWeights returns the v1 default weighting: payee highest (the
// strongest counter-account signal in practice), narration medium, account and
// sign low-but-present (domain priors), metadata lowest (noisy, free-form).
func DefaultFieldWeights() FieldWeights {
	return FieldWeights{
		Payee:     3.0,
		Narration: 1.5,
		Metadata:  0.5,
		Account:   0.75,
		Sign:      0.5,
	}
}

// ExtractFeatures builds the Features view of txn from the posting at knownIdx
// (the account whose counterpart is to be predicted). It tokenizes Payee,
// Narration, and transaction-level MetaString metadata via tok, namespaces each
// token by field, accumulates per-field weights from fw, and folds in one acct:
// token per ancestor prefix of the known account plus a single sign: token
// derived from the known posting's amount. AmountAbs, Currency, and Sign are
// taken from the known posting.
//
// knownIdx must be a valid index into txn.Postings; ExtractFeatures panics
// otherwise. The result aliases no memory of txn and is deterministic for equal
// inputs.
func ExtractFeatures(txn *ast.Transaction, knownIdx int, tok Tokenizer, fw FieldWeights) Features {
	known := txn.Postings[knownIdx]
	acc := tokenAcc{}

	addText := func(prefix, text string, w float64) {
		if w == 0 || text == "" {
			return
		}
		for _, t := range tok.Tokenize(text) {
			acc.add(prefix+t, w)
		}
	}
	addText("payee:", txn.Payee, fw.Payee)
	addText("narr:", txn.Narration, fw.Narration)

	if fw.Metadata != 0 && len(txn.Meta.Props) > 0 {
		keys := make([]string, 0, len(txn.Meta.Props))
		for k := range txn.Meta.Props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v := txn.Meta.Props[k]; v.Kind == ast.MetaString {
				addText("meta."+k+":", v.String, fw.Metadata)
			}
		}
	}

	if fw.Account != 0 {
		for _, a := range ancestorPrefixes(known.Account) {
			acc.add("acct:"+string(a), fw.Account)
		}
	}

	s := signOf(known.Amount)
	if fw.Sign != 0 {
		acc.add("sign:"+s.token(), fw.Sign)
	}

	f := Features{Terms: acc.terms(), Sign: s}
	if known.Amount != nil {
		abs := new(apd.Decimal)
		_, _ = apd.BaseContext.Abs(abs, &known.Amount.Number)
		f.AmountAbs = abs
		f.Currency = known.Amount.Currency
	}
	return f
}

// tokenAcc accumulates namespaced token weights before they are flushed into a
// sorted, deduplicated []Term.
type tokenAcc map[string]float64

func (a tokenAcc) add(token string, w float64) { a[token] += w }

func (a tokenAcc) terms() []Term {
	if len(a) == 0 {
		return nil
	}
	out := make([]Term, 0, len(a))
	for tok, w := range a {
		out = append(out, Term{Token: tok, Weight: w})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Token < out[j].Token })
	return out
}

// ancestorPrefixes returns a and each of its ancestors down to the root, in
// root-first order: "Assets:Bank:Checking" → Assets, Assets:Bank, Assets:Bank:Checking.
func ancestorPrefixes(a ast.Account) []ast.Account {
	parts := a.Parts()
	if len(parts) == 0 {
		return nil
	}
	out := make([]ast.Account, 0, len(parts))
	cur := ""
	for i, p := range parts {
		if i == 0 {
			cur = p
		} else {
			cur += ":" + p
		}
		out = append(out, ast.Account(cur))
	}
	return out
}

func signOf(a *ast.Amount) Sign {
	if a == nil {
		return SignZero
	}
	switch a.Number.Sign() {
	case 1:
		return SignDebit
	case -1:
		return SignCredit
	default:
		return SignZero
	}
}

func (s Sign) token() string {
	switch s {
	case SignDebit:
		return "debit"
	case SignCredit:
		return "credit"
	default:
		return "zero"
	}
}
