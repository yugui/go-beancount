package types_test

import (
	"regexp"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/types"
)

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
			{Account: "Assets:Broker", Amount: &ast.Amount{Number: dec(t, "10"), Currency: "STOCK"}},
			{Account: "Assets:Cash"},
		},
	}
}

func TestEntryIDShapeAndDeterminism(t *testing.T) {
	txn := sampleTxn(t)
	id1 := types.EntryID(txn)
	id2 := types.EntryID(sampleTxn(t))
	if !hex32.MatchString(id1) {
		t.Errorf("EntryID = %q, want 32 lowercase hex chars", id1)
	}
	if id1 != id2 {
		t.Errorf("EntryID not deterministic: %q != %q", id1, id2)
	}
}

func TestEntryIDMetaInsensitive(t *testing.T) {
	a := sampleTxn(t)
	b := sampleTxn(t)
	b.Meta = ast.Metadata{Props: map[string]ast.MetaValue{"k": {Kind: ast.MetaString, String: "v"}}}
	b.Span = ast.Span{Start: ast.Position{Filename: "other.beancount", Line: 99}}
	if types.EntryID(a) != types.EntryID(b) {
		t.Error("EntryID changed with meta/span; want meta- and span-insensitive")
	}
}

func TestEntryIDCollectionOrderInsensitive(t *testing.T) {
	a := sampleTxn(t)
	b := sampleTxn(t)
	b.Tags = []string{"q1", "trip"}
	b.Links = []string{"inv-1"}
	if types.EntryID(a) != types.EntryID(b) {
		t.Error("EntryID changed with tag reordering; want order-insensitive")
	}
}

func TestEntryIDDistinct(t *testing.T) {
	a := sampleTxn(t)
	b := sampleTxn(t)
	b.Narration = "sell"
	if types.EntryID(a) == types.EntryID(b) {
		t.Error("distinct directives share an EntryID")
	}

	open := &ast.Open{Date: a.Date, Account: "Assets:Broker"}
	closeDir := &ast.Close{Date: a.Date, Account: "Assets:Broker"}
	if types.EntryID(open) == types.EntryID(closeDir) {
		t.Error("Open and Close with same date/account share an EntryID; type must disambiguate")
	}
}
