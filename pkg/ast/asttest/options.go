// Package asttest provides test fixtures for ast values.
//
// It is intended only for use from `_test.go` files; importing it from
// production code is a layering error.
package asttest

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

// MustOptions builds an *ast.OptionValues from raw key/value pairs by
// inserting Option directives into a synthetic ledger and parsing them.
// It fails the test on any parse error. Useful in plugin tests where a
// typed Options snapshot is needed without going through [ast.Load],
// [ast.LoadFile], or [ast.LoadReader].
//
// Iteration order over raw is non-deterministic by Go map semantics.
func MustOptions(t testing.TB, raw map[string]string) *ast.OptionValues {
	t.Helper()
	ledger := &ast.Ledger{}
	for k, v := range raw {
		ledger.Insert(&ast.Option{Key: k, Value: v})
	}
	opts, errs := ast.ParseOptions(ledger)
	if len(errs) != 0 {
		t.Fatalf("asttest.MustOptions: parse errors: %v", errs)
	}
	return opts
}
