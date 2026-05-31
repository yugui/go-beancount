package metaval_test

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/metaval"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func TestValueKinds(t *testing.T) {
	num := apd.New(123, -2) // 1.23
	amt := ast.Amount{Number: *apd.New(50, 0), Currency: "USD"}
	day := time.Date(2021, 7, 4, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		in   ast.MetaValue
		want types.Type
		// check verifies the converted value's payload; nil to skip.
		check func(t *testing.T, v types.Value)
	}{
		{
			name: "string",
			in:   ast.MetaValue{Kind: ast.MetaString, String: "hello"},
			want: types.String,
			check: func(t *testing.T, v types.Value) {
				if s, _ := types.AsString(v); s != "hello" {
					t.Errorf("AsString = %q; want %q", s, "hello")
				}
			},
		},
		{
			name: "account",
			in:   ast.MetaValue{Kind: ast.MetaAccount, String: "Assets:Cash"},
			want: types.String,
			check: func(t *testing.T, v types.Value) {
				if s, _ := types.AsString(v); s != "Assets:Cash" {
					t.Errorf("AsString = %q; want %q", s, "Assets:Cash")
				}
			},
		},
		{
			name: "currency",
			in:   ast.MetaValue{Kind: ast.MetaCurrency, String: "USD"},
			want: types.String,
		},
		{
			name: "tag",
			in:   ast.MetaValue{Kind: ast.MetaTag, String: "trip"},
			want: types.String,
		},
		{
			name: "link",
			in:   ast.MetaValue{Kind: ast.MetaLink, String: "ref-1"},
			want: types.String,
		},
		{
			name: "date",
			in:   ast.MetaValue{Kind: ast.MetaDate, Date: day},
			want: types.Date,
			check: func(t *testing.T, v types.Value) {
				if d, _ := types.AsDate(v); !d.Equal(day) {
					t.Errorf("AsDate = %v; want %v", d, day)
				}
			},
		},
		{
			name: "number",
			in:   ast.MetaValue{Kind: ast.MetaNumber, Number: *num},
			want: types.Decimal,
			check: func(t *testing.T, v types.Value) {
				d, _ := types.AsDecimal(v)
				if d.Cmp(num) != 0 {
					t.Errorf("AsDecimal = %s; want %s", d.Text('f'), num.Text('f'))
				}
			},
		},
		{
			name: "amount",
			in:   ast.MetaValue{Kind: ast.MetaAmount, Amount: amt},
			want: types.Amount,
			check: func(t *testing.T, v types.Value) {
				got, _ := types.AsAmount(v)
				if got.Currency != amt.Currency || got.Number.Cmp(&amt.Number) != 0 {
					t.Errorf("AsAmount = %s %s; want %s %s",
						got.Number.Text('f'), got.Currency, amt.Number.Text('f'), amt.Currency)
				}
			},
		},
		{
			name: "bool",
			in:   ast.MetaValue{Kind: ast.MetaBool, Bool: true},
			want: types.Bool,
			check: func(t *testing.T, v types.Value) {
				if b, _ := types.AsBool(v); !b {
					t.Errorf("AsBool = false; want true")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := metaval.Value(tc.in)
			if v.IsNull() {
				t.Fatalf("Value(%s) is NULL; want non-null %s", tc.name, tc.want)
			}
			if v.Type() != tc.want {
				t.Errorf("Value(%s).Type() = %s; want %s", tc.name, v.Type(), tc.want)
			}
			if tc.check != nil {
				tc.check(t, v)
			}
		})
	}
}

func TestValueUnknownKind(t *testing.T) {
	// A zero/unknown MetaValueKind falls through to a NULL String.
	v := metaval.Value(ast.MetaValue{Kind: ast.MetaValueKind(-1)})
	if !v.IsNull() {
		t.Errorf("Value(unknown).IsNull() = false; want true")
	}
	if v.Type() != types.String {
		t.Errorf("Value(unknown).Type() = %s; want String", v.Type())
	}
}

func TestDict(t *testing.T) {
	d := metaval.Dict(ast.Metadata{Props: map[string]ast.MetaValue{
		"who":  {Kind: ast.MetaString, String: "alice"},
		"open": {Kind: ast.MetaBool, Bool: true},
	}})
	if d.Len() != 2 {
		t.Fatalf("Dict.Len() = %d; want 2", d.Len())
	}
	if v, ok := d.Get("who"); !ok || v.Type() != types.String {
		t.Errorf("Dict[who] = %v (ok=%v); want String", v, ok)
	}
	if v, ok := d.Get("open"); !ok || v.Type() != types.Bool {
		t.Errorf("Dict[open] = %v (ok=%v); want Bool", v, ok)
	}
}

func TestDictEmpty(t *testing.T) {
	// Empty metadata yields an empty, non-NULL Dict.
	for _, m := range []ast.Metadata{{}, {Props: map[string]ast.MetaValue{}}} {
		d := metaval.Dict(m)
		if types.Value(d).IsNull() {
			t.Errorf("Dict(%+v) is NULL; want empty non-null Dict", m)
		}
		if d.Len() != 0 {
			t.Errorf("Dict(%+v).Len() = %d; want 0", m, d.Len())
		}
	}
}
