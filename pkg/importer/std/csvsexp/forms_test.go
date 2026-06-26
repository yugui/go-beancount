package csvsexp

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
)

// extractProgram compiles program and extracts csv through it, failing on any
// compile or extract error.
func extractProgram(t *testing.T, program, csv string) importer.Output {
	t.Helper()
	imp, err := importerFromProgram(t, "forms", program)
	if err != nil {
		t.Fatalf("newImporter: %v", err)
	}
	in := importer.Input{Path: "/in.csv", Opener: func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader([]byte(csv))), nil
	}}
	out, err := imp.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return out
}

func firstTxn(t *testing.T, out importer.Output) *ast.Transaction {
	t.Helper()
	if len(out.Directives) == 0 {
		t.Fatal("no directives extracted")
	}
	tx, ok := out.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("directive is %T, want *ast.Transaction", out.Directives[0])
	}
	return tx
}

// TestTransactionForm_ThreePostings exercises a transaction with more legs than
// the primary+counter shape, including a final auto-balanced posting.
func TestTransactionForm_ThreePostings(t *testing.T) {
	const prog = `(csv-import
  (let* ((d (parse-date (column "Date") "2006-01-02"))
         (amt (parse-amount (column "Amount"))))
    (emit
      (transaction :date d
        :postings (postings
          (posting :account (const "Assets:Bank")
                   :amount (amount amt :currency (const "USD")))
          (posting :account (const "Expenses:Fee")
                   :amount (amount (parse-amount (column "Fee")) :currency (const "USD")))
          (posting :account (const "Income:Source")))))))`
	out := extractProgram(t, prog, "Date,Amount,Fee\n2024-01-01,100,5\n")
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	tx := firstTxn(t, out)
	if len(tx.Postings) != 3 {
		t.Fatalf("got %d postings, want 3", len(tx.Postings))
	}
	if string(tx.Postings[0].Account) != "Assets:Bank" || tx.Postings[0].Amount.Currency != "USD" {
		t.Errorf("posting 0 = %+v", tx.Postings[0])
	}
	if tx.Postings[1].Amount.Number.String() != "5" {
		t.Errorf("posting 1 amount = %v, want 5", tx.Postings[1].Amount.Number)
	}
	if tx.Postings[2].Amount != nil {
		t.Errorf("posting 2 amount = %v, want nil (auto posting)", tx.Postings[2].Amount)
	}
}

// TestPostingForm_Metadata stamps posting-level metadata.
func TestPostingForm_Metadata(t *testing.T) {
	const prog = `(csv-import
  (let* ((d (parse-date (column "Date") "2006-01-02"))
         (amt (parse-amount (column "Amount"))))
    (emit
      (transaction :date d
        :postings (postings
          (posting :account (const "Assets:Bank")
                   :amount (amount amt :currency (const "USD"))
                   :flag "!"
                   :meta (("note" (column "Note")))))))))`
	out := extractProgram(t, prog, "Date,Amount,Note\n2024-01-01,100,hello\n")
	tx := firstTxn(t, out)
	p := tx.Postings[0]
	if p.Flag != '!' {
		t.Errorf("posting flag = %q, want '!'", p.Flag)
	}
	if p.Meta.Props["note"].String != "hello" {
		t.Errorf("posting meta note = %q, want hello", p.Meta.Props["note"].String)
	}
}

// TestDoubleEntryForm reproduces the primary+counter shape with a negated leg.
func TestDoubleEntryForm(t *testing.T) {
	const prog = `(csv-import
  (let* ((d (parse-date (column "Date") "2006-01-02"))
         (amt (parse-amount (column "Amount"))))
    (emit
      (transaction :date d
        :postings (double-entry
          (posting :account (const "Assets:Bank")
                   :amount (amount amt :currency (const "USD")))
          (const "Expenses:Food"))))))`
	out := extractProgram(t, prog, "Date,Amount\n2024-01-01,100\n")
	tx := firstTxn(t, out)
	if len(tx.Postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(tx.Postings))
	}
	if string(tx.Postings[1].Account) != "Expenses:Food" || tx.Postings[1].Amount.Number.String() != "-100" {
		t.Errorf("counter posting = %+v, want Expenses:Food -100", tx.Postings[1])
	}
}

// TestTransactionForm_TagsLinksMeta wires transaction-level decorations.
func TestTransactionForm_TagsLinksMeta(t *testing.T) {
	const prog = `(csv-import
  (let* ((d (parse-date (column "Date") "2006-01-02"))
         (amt (parse-amount (column "Amount"))))
    (emit
      (transaction :date d
        :payee (const "ACME")
        :tags (tags (const "trip"))
        :links (links (const "inv-1"))
        :meta (meta ("ref" (const "R9")))
        :postings (postings
          (posting :account (const "Assets:Bank")
                   :amount (amount amt :currency (const "USD"))))))))`
	out := extractProgram(t, prog, "Date,Amount\n2024-01-01,100\n")
	tx := firstTxn(t, out)
	if tx.Payee != "ACME" {
		t.Errorf("payee = %q", tx.Payee)
	}
	if len(tx.Tags) != 1 || tx.Tags[0] != "trip" {
		t.Errorf("tags = %v, want [trip]", tx.Tags)
	}
	if len(tx.Links) != 1 || tx.Links[0] != "inv-1" {
		t.Errorf("links = %v, want [inv-1]", tx.Links)
	}
	if tx.Meta.Props["ref"].String != "R9" {
		t.Errorf("meta ref = %q, want R9", tx.Meta.Props["ref"].String)
	}
}

// TestIfOverPosting selects between posting legs at runtime.
func TestIfOverPosting(t *testing.T) {
	const prog = `(csv-import
  (let* ((d (parse-date (column "Date") "2006-01-02"))
         (amt (parse-amount (column "Amount"))))
    (emit
      (transaction :date d
        :postings (postings
          (posting :account (const "Assets:Bank")
                   :amount (amount amt :currency (const "USD")))
          (if (negative? amt)
              (posting :account (const "Income:Refund"))
              (posting :account (const "Expenses:Spend"))))))))`
	out := extractProgram(t, prog, "Date,Amount\n2024-01-01,-100\n")
	tx := firstTxn(t, out)
	if string(tx.Postings[1].Account) != "Income:Refund" {
		t.Errorf("posting 1 account = %q, want Income:Refund", tx.Postings[1].Account)
	}
}

// TestPostingForm_Price builds a securities-sale entry: cash, a stock leg with a
// per-unit price annotation, and an auto-balanced fee leg.
func TestPostingForm_Price(t *testing.T) {
	const prog = `(csv-import
  (let* ((d    (parse-date (column "Date") "2006-01-02"))
         (qty  (negate-amount (parse-amount (column "Qty"))))
         (unit (parse-amount (column "Unit")))
         (cash (parse-amount (column "Proceeds")))
         (jpy  (const "JPY")))
    (emit
      (transaction :date d :narration (const "sale")
        :postings (postings
          (posting :account (const "Assets:Cash") :amount (amount cash :currency jpy))
          (posting :account (const "Assets:Stock") :amount (amount qty :currency (const "ACME"))
                   :price (price unit :currency jpy))
          (posting :account (const "Expenses:Fees")))))))`
	out := extractProgram(t, prog, "Date,Qty,Unit,Proceeds\n2024-01-01,2,12090,24043\n")
	tx := firstTxn(t, out)
	if len(tx.Postings) != 3 {
		t.Fatalf("got %d postings, want 3", len(tx.Postings))
	}
	stock := tx.Postings[1]
	if stock.Price == nil {
		t.Fatal("stock price = nil, want annotation")
	}
	if stock.Price.Amount.Number.String() != "12090" || stock.Price.Amount.Currency != "JPY" {
		t.Errorf("price = %+v, want 12090 JPY", stock.Price.Amount)
	}
	if stock.Amount.Number.String() != "-2" || stock.Amount.Currency != "ACME" {
		t.Errorf("stock amount = %+v, want -2 ACME", stock.Amount)
	}
	if tx.Postings[2].Amount != nil {
		t.Errorf("fee leg amount = %v, want nil (auto)", tx.Postings[2].Amount)
	}
}

// TestEmitHeterogeneous routes each row to a balance assertion or a transaction
// by type, unified through (directive ...) and a single (emit ...) terminal.
func TestEmitHeterogeneous(t *testing.T) {
	const prog = `(csv-import
  (let* ((d    (parse-date (column "Date") "2006-01-02"))
         (kind (column "Kind"))
         (amt  (parse-amount (column "Amount")))
         (jpy  (const "JPY")))
    (emit
      (if (equal? kind (const "BAL"))
          (directive (balance :date d :account (const "Assets:Cash")
                              :amount (amount amt :currency jpy)))
          (directive (transaction :date d :narration (column "Memo")
                       :postings (postings
                         (posting :account (const "Assets:Cash") :amount (amount amt :currency jpy))
                         (posting :account (const "Income:Misc")))))))))`
	out := extractProgram(t, prog, "Date,Kind,Amount,Memo\n2024-01-01,BAL,1000,\n2024-01-02,TXN,50,pay\n")
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 2 {
		t.Fatalf("got %d directives, want 2", len(out.Directives))
	}
	bal, ok := out.Directives[0].(*ast.Balance)
	if !ok {
		t.Fatalf("directive 0 = %T, want *ast.Balance", out.Directives[0])
	}
	if string(bal.Account) != "Assets:Cash" || bal.Amount.Number.String() != "1000" || bal.Amount.Currency != "JPY" {
		t.Errorf("balance = %+v", bal)
	}
	tx, ok := out.Directives[1].(*ast.Transaction)
	if !ok {
		t.Fatalf("directive 1 = %T, want *ast.Transaction", out.Directives[1])
	}
	if len(tx.Postings) != 2 || tx.Narration != "pay" {
		t.Errorf("txn = %+v", tx)
	}
}

func TestForms_CompileErrors(t *testing.T) {
	cases := []struct {
		name    string
		program string
		want    string
	}{
		{
			"posting without account",
			`(csv-import (emit (transaction :date (parse-date (column "D") "2006-01-02") :postings (postings (posting :amount (amount (parse-amount (column "A")) :currency (const "USD")))))))`,
			"posting requires :account",
		},
		{
			"transaction without postings",
			`(csv-import (emit (transaction :date (parse-date (column "D") "2006-01-02"))))`,
			"transaction requires :postings",
		},
		{
			"emit of non-directive",
			`(csv-import (emit (const "x")))`,
			"expected a directive, transaction, or balance",
		},
		{
			"emit as expression",
			`(csv-import (let* ((x (emit (transaction :date (parse-date (column "D") "2006-01-02") :postings (postings))))) (emit-transaction :date (parse-date (column "D") "2006-01-02") :amount (parse-amount (column "A")) :currency (const "USD") :account (const "Assets:X"))))`,
			"emit is only valid as the csv-import body",
		},
		{
			"posting amount expects amount-value-key",
			`(csv-import (emit (transaction :date (parse-date (column "D") "2006-01-02") :postings (postings (posting :account (const "Assets:X") :amount (parse-amount (column "A")))))))`,
			"expected amount-value-key",
		},
		{
			"balance without amount",
			`(csv-import (emit (balance :date (parse-date (column "D") "2006-01-02") :account (const "Assets:X"))))`,
			"balance requires :amount",
		},
		{
			"directive of non-directive",
			`(csv-import (emit (directive (const "x"))))`,
			"expected a directive, transaction, or balance",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := importerFromProgram(t, "err", tc.program)
			if err == nil {
				t.Fatalf("want compile error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}
