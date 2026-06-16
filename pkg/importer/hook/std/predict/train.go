package predict

import (
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Example is one supervised training instance: the Features of the known side
// of a historical transaction, labeled with the account on the counter side.
// Date is the transaction's date, available to the predictor for recency-aware
// tie-breaking; it is not used during extraction.
type Example struct {
	Features Features
	Label    ast.Account
	Date     time.Time
}

// ExtractExamples walks l in canonical chronological order and emits training
// Examples for each eligible transaction. A transaction is eligible iff it has
// exactly two postings, both carrying a non-nil Amount (a balanced 2-posting
// transaction, checked structurally rather than by net-zero verification).
//
// Orientation follows the import-source policy: when exactly one posting roots
// at Assets or Liabilities, that posting is the known side and the other its
// label (one Example). When both or neither side is source-like (a transfer, or
// an income/expense-only entry), both orientations are emitted. All other
// transaction shapes are skipped. Examples are ordered by Ledger.All(), and
// within a two-orientation transaction the posting[0]-as-known Example comes
// first. The result is nil when no transaction is eligible.
func ExtractExamples(l *ast.Ledger, tok Tokenizer, fw FieldWeights) []Example {
	var out []Example
	for _, d := range l.All() {
		txn, ok := d.(*ast.Transaction)
		if !ok || !eligible(txn) {
			continue
		}
		src0 := isSourceLike(txn.Postings[0].Account)
		src1 := isSourceLike(txn.Postings[1].Account)
		switch {
		case src0 && !src1:
			out = append(out, exampleOf(txn, 0, 1, tok, fw))
		case src1 && !src0:
			out = append(out, exampleOf(txn, 1, 0, tok, fw))
		default:
			out = append(out, exampleOf(txn, 0, 1, tok, fw), exampleOf(txn, 1, 0, tok, fw))
		}
	}
	return out
}

func eligible(txn *ast.Transaction) bool {
	return len(txn.Postings) == 2 &&
		txn.Postings[0].Amount != nil && txn.Postings[1].Amount != nil
}

func isSourceLike(a ast.Account) bool {
	r := a.Root()
	return r == ast.Assets || r == ast.Liabilities
}

func exampleOf(txn *ast.Transaction, knownIdx, labelIdx int, tok Tokenizer, fw FieldWeights) Example {
	return Example{
		Features: ExtractFeatures(txn, knownIdx, tok, fw),
		Label:    txn.Postings[labelIdx].Account,
		Date:     txn.Date,
	}
}

// OpenAccounts returns the set of accounts that are open — opened by an Open
// directive and not subsequently closed — as of the end of l, folding
// Open/Close in canonical chronological order. A reopen (Open after Close)
// re-adds the account. A nil or empty ledger yields an empty, non-nil map.
func OpenAccounts(l *ast.Ledger) map[ast.Account]bool {
	m := map[ast.Account]bool{}
	for _, d := range l.All() {
		switch v := d.(type) {
		case *ast.Open:
			m[v.Account] = true
		case *ast.Close:
			delete(m, v.Account)
		}
	}
	return m
}
