package table_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/table"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func TestPostingsIDSharedAcrossTxn(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(stockTxn(t))
	tb := table.Postings(l)
	rows := collectRows(tb)

	s0, _ := types.AsString(valueOf(t, tb, rows[0], "id"))
	s1, _ := types.AsString(valueOf(t, tb, rows[1], "id"))
	if s0 != s1 {
		t.Errorf("postings.id differs across one txn's postings: %q != %q", s0, s1)
	}
	if len(s0) != 32 {
		t.Errorf("postings.id = %q, want 32 hex chars", s0)
	}
}

func TestEntriesIDNonNull(t *testing.T) {
	tb := table.Entries(entriesLedger(t))
	for i, r := range collectRows(tb) {
		v := valueOf(t, tb, r, "id")
		s, ok := types.AsString(v)
		if !ok || len(s) != 32 {
			t.Errorf("entries.id[%d] = %v, want a 32-hex String (never NULL)", i, v.Format())
		}
	}
}

// TestPostingsEntriesIDAgree confirms a posting's id equals its parent
// transaction's entries id.
func TestPostingsEntriesIDAgree(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(stockTxn(t))

	pt := table.Postings(l)
	et := table.Entries(l)
	ps, _ := types.AsString(valueOf(t, pt, collectRows(pt)[0], "id"))
	es, _ := types.AsString(valueOf(t, et, collectRows(et)[0], "id"))
	if ps != es {
		t.Errorf("postings.id %q != entries.id %q for the same transaction", ps, es)
	}
}
