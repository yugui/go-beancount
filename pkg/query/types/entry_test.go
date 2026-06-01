package types_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func TestEntryFormatIsJSON(t *testing.T) {
	open := &ast.Open{
		Span:       ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 3}},
		Date:       time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Cash",
		Currencies: []string{"USD"},
	}
	v := types.NewEntry(open)

	var got map[string]any
	if err := json.Unmarshal([]byte(v.Format()), &got); err != nil {
		t.Fatalf("Format() is not valid JSON: %v\n%s", err, v.Format())
	}
	if got["type"] != "open" || got["account"] != "Assets:Cash" || got["date"] != "2020-01-01" {
		t.Errorf("unexpected tree: %v", got)
	}
	if v.String() != v.Format() {
		t.Errorf("String() %q != Format() %q", v.String(), v.Format())
	}
	if tree, ok := types.MarshalTree(v).(map[string]any); !ok || tree["type"] != "open" {
		t.Errorf("MarshalTree = %#v, want a directive map", types.MarshalTree(v))
	}
}

// TestEntryCompareIdentity locks the (span, id) identity order: the same
// directive compares equal, distinct directives are unequal and totally
// ordered, and Entry sorts after every lower-ordinal kind.
func TestEntryCompareIdentity(t *testing.T) {
	a := &ast.Open{Span: ast.Span{Start: ast.Position{Filename: "a", Line: 1}}, Date: date2020, Account: "Assets:Cash"}
	b := &ast.Close{Span: ast.Span{Start: ast.Position{Filename: "a", Line: 9}}, Date: date2020, Account: "Assets:Cash"}

	if c := types.NewEntry(a).Compare(types.NewEntry(a)); c != 0 {
		t.Errorf("same directive Compare = %d, want 0", c)
	}
	if c := types.NewEntry(a).Compare(types.NewEntry(b)); c == 0 {
		t.Errorf("distinct directives compare equal; want nonzero")
	}
	// antisymmetry
	if x, y := types.NewEntry(a).Compare(types.NewEntry(b)), types.NewEntry(b).Compare(types.NewEntry(a)); x != -y {
		t.Errorf("not antisymmetric: %d vs %d", x, y)
	}
	// a non-null entry sorts before NULL(Entry)
	if c := types.NewEntry(a).Compare(types.Null(types.Entry)); c != -1 {
		t.Errorf("entry vs NULL = %d, want -1", c)
	}
}

var date2020 = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
