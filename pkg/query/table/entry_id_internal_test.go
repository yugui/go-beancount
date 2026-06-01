package table

import (
	"regexp"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// entryID is an unexported package-internal building block reused by the
// postings, entries, and transactions tables; its stability/uniqueness
// contract has independent value, so it is tested directly (CLAUDE.md
// "tests target observable behavior", building-block exception).

func mustDec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("apd.NewFromString(%q): %v", s, err)
	}
	return *d
}

var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

func sampleTxn(t *testing.T) *ast.Transaction {
	t.Helper()
	return &ast.Transaction{
		Date:      time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
		Flag:      '*',
		Payee:     "ACME",
		Narration: "buy",
		Tags:      []string{"trip", "q1"},
		Links:     []string{"inv-1"},
		Postings: []ast.Posting{
			{Account: "Assets:Broker", Amount: &ast.Amount{Number: mustDec(t, "10"), Currency: "STOCK"}},
			{Account: "Assets:Cash"},
		},
	}
}

func TestEntryIDShapeAndDeterminism(t *testing.T) {
	txn := sampleTxn(t)
	id1 := entryID(txn)
	id2 := entryID(sampleTxn(t))
	if !hex32.MatchString(id1) {
		t.Errorf("entryID = %q, want 32 lowercase hex chars", id1)
	}
	if id1 != id2 {
		t.Errorf("entryID not deterministic: %q != %q", id1, id2)
	}
}

func TestEntryIDMetaInsensitive(t *testing.T) {
	a := sampleTxn(t)
	b := sampleTxn(t)
	b.Meta = ast.Metadata{Props: map[string]ast.MetaValue{"k": {Kind: ast.MetaString, String: "v"}}}
	b.Span = ast.Span{Start: ast.Position{Filename: "other.beancount", Line: 99}}
	if entryID(a) != entryID(b) {
		t.Error("entryID changed with meta/span; want meta- and span-insensitive")
	}
}

func TestEntryIDCollectionOrderInsensitive(t *testing.T) {
	a := sampleTxn(t)
	b := sampleTxn(t)
	b.Tags = []string{"q1", "trip"}
	b.Links = []string{"inv-1"}
	if entryID(a) != entryID(b) {
		t.Error("entryID changed with tag reordering; want order-insensitive")
	}
}

func TestEntryIDDistinct(t *testing.T) {
	a := sampleTxn(t)
	b := sampleTxn(t)
	b.Narration = "sell"
	if entryID(a) == entryID(b) {
		t.Error("distinct directives share an entryID")
	}

	open := &ast.Open{Date: a.Date, Account: "Assets:Broker"}
	close := &ast.Close{Date: a.Date, Account: "Assets:Broker"}
	if entryID(open) == entryID(close) {
		t.Error("Open and Close with same date/account share an entryID; type must disambiguate")
	}
}
