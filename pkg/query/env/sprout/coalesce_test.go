package sprout_test

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"

	// activate the function library under test
	_ "github.com/yugui/go-beancount/pkg/query/env/sprout"
)

func mustResolve(t *testing.T, name string, in ...types.Type) api.Scalar {
	t.Helper()
	fn, err := env.Resolve(name, in)
	if err != nil {
		t.Fatalf("Resolve(%q, %v): %v", name, in, err)
	}
	if fn.Scalar == nil {
		t.Fatalf("Resolve(%q, %v) is not a scalar", name, in)
	}
	return fn.Scalar
}

// TestCoalesceFirstNonNull covers the core semantics across arities: the
// first non-NULL argument is returned and earlier NULLs are skipped.
func TestCoalesceFirstNonNull(t *testing.T) {
	for arity := 1; arity <= 5; arity++ {
		in := make([]types.Type, arity)
		for i := range in {
			in[i] = types.String
		}
		fn := mustResolve(t, "coalesce", in...)

		// Last argument is the only non-NULL one.
		args := make([]types.Value, arity)
		for i := range args {
			args[i] = types.Null(types.String)
		}
		args[arity-1] = types.NewString("found")

		got, err := fn(args)
		if err != nil {
			t.Fatalf("arity %d: %v", arity, err)
		}
		if s, _ := types.AsString(got); s != "found" {
			t.Errorf("arity %d: coalesce = %v, want %q", arity, got, "found")
		}
	}
}

// TestCoalesceEarlierWins verifies that the first non-NULL argument wins even
// when later arguments are also non-NULL.
func TestCoalesceEarlierWins(t *testing.T) {
	fn := mustResolve(t, "coalesce", types.Int, types.Int, types.Int)
	got, err := fn([]types.Value{
		types.Null(types.Int), types.NewInt(7), types.NewInt(9),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := types.AsInt(got); n != 7 {
		t.Errorf("coalesce = %v, want 7", got)
	}
}

// TestCoalesceAllNullTypedNull verifies that an all-NULL call returns a NULL
// carrying the overload's type, for every registered type.
func TestCoalesceAllNullTypedNull(t *testing.T) {
	for _, ty := range []types.Type{
		types.Int, types.Decimal, types.String, types.Amount,
		types.Bool, types.Date, types.SetType, types.DictType,
	} {
		fn := mustResolve(t, "coalesce", ty, ty)
		got, err := fn([]types.Value{types.Null(ty), types.Null(ty)})
		if err != nil {
			t.Fatalf("type %v: %v", ty, err)
		}
		if !got.IsNull() {
			t.Errorf("type %v: coalesce(NULL, NULL) = %v, want NULL", ty, got)
		}
		if got.Type() != ty {
			t.Errorf("type %v: NULL result type = %v, want %v", ty, got.Type(), ty)
		}
	}
}

// TestCoalesceQuery exercises coalesce end-to-end through the query compiler,
// proving the blank import activates it: payee is NULL on the transaction, so
// coalesce(payee, narration) falls through to the narration.
func TestCoalesceQuery(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Transaction{
			Date:      date(2021, 3, 15),
			Flag:      '*',
			Narration: "fallback",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "1"), Currency: "USD"}},
			},
		},
	})
	res, err := query.Query(context.Background(),
		"SELECT coalesce(payee, narration) AS c FROM postings LIMIT 1", l)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if s, _ := types.AsString(res.Rows[0][0]); s != "fallback" {
		t.Errorf("coalesce(payee, narration) = %v, want %q", res.Rows[0][0], "fallback")
	}
}

func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("apd.NewFromString(%q): %v", s, err)
	}
	return *d
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
