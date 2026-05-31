package env_test

import (
	"errors"
	"testing"

	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// The registry is process-global with no reset hook, so every test uses
// function names unique to that test to avoid cross-test collisions.

// TestResolve_CaseInsensitive verifies name lookup ignores case.
func TestResolve_CaseInsensitive(t *testing.T) {
	name := "TestResolve_CaseInsensitive_f"
	env.Register(scalarFn(name, []types.Type{types.Int}, types.Int))
	if _, err := env.Resolve("TESTRESOLVE_CASEINSENSITIVE_F", []types.Type{types.Int}); err != nil {
		t.Errorf("Resolve(uppercased): %v, want match", err)
	}
}

// TestResolve_AnyIsLastResort verifies an Any slot loses to an exact match and
// only wins when nothing concrete fits.
func TestResolve_AnyIsLastResort(t *testing.T) {
	name := "TestResolve_AnyIsLastResort_f"
	env.Register(scalarFn(name, []types.Type{types.Int}, types.Int))
	env.Register(scalarFn(name, []types.Type{types.Any}, types.String))

	got, err := env.Resolve(name, []types.Type{types.Int})
	if err != nil {
		t.Fatalf("Resolve(Int): %v", err)
	}
	if got.In[0] != types.Int {
		t.Errorf("Resolve(Int) chose %v, want the Int overload", got.In)
	}

	got, err = env.Resolve(name, []types.Type{types.String})
	if err != nil {
		t.Fatalf("Resolve(String): %v", err)
	}
	if got.In[0] != types.Any {
		t.Errorf("Resolve(String) chose %v, want the Any overload", got.In)
	}
}

// TestResolve_WideningBeatsAny verifies an Int->Decimal widening outranks an
// Any slot, since the Any penalty dominates any widening cost.
func TestResolve_WideningBeatsAny(t *testing.T) {
	name := "TestResolve_WideningBeatsAny_f"
	env.Register(scalarFn(name, []types.Type{types.Decimal}, types.Decimal))
	env.Register(scalarFn(name, []types.Type{types.Any}, types.String))

	got, err := env.Resolve(name, []types.Type{types.Int})
	if err != nil {
		t.Fatalf("Resolve(Int): %v", err)
	}
	if got.In[0] != types.Decimal {
		t.Errorf("Resolve(Int) chose %v, want the Decimal (widened) overload", got.In)
	}
}

// TestResolve_NullLiteralOnlyMatchesAny verifies the mechanism behind cast
// accepting NULL: a NULL literal (types.Invalid) matches only an Any slot, and
// is rejected by a concrete one.
func TestResolve_NullLiteralOnlyMatchesAny(t *testing.T) {
	concrete := "TestResolve_NullLiteralOnlyMatchesAny_concrete"
	env.Register(scalarFn(concrete, []types.Type{types.Int}, types.Int))
	if _, err := env.Resolve(concrete, []types.Type{types.Invalid}); !errors.Is(err, env.ErrNoOverload) {
		t.Errorf("Resolve(NULL) on concrete = %v, want ErrNoOverload", err)
	}

	anyName := "TestResolve_NullLiteralOnlyMatchesAny_any"
	env.Register(scalarFn(anyName, []types.Type{types.Any}, types.String))
	if _, err := env.Resolve(anyName, []types.Type{types.Invalid}); err != nil {
		t.Errorf("Resolve(NULL) on Any = %v, want match", err)
	}
}

// TestResolve_FewerAnyWins verifies that, among Any candidates, the one with
// fewer Any slots is preferred.
func TestResolve_FewerAnyWins(t *testing.T) {
	name := "TestResolve_FewerAnyWins_f"
	env.Register(scalarFn(name, []types.Type{types.Any, types.Int}, types.Int))
	env.Register(scalarFn(name, []types.Type{types.Any, types.Any}, types.String))

	got, err := env.Resolve(name, []types.Type{types.String, types.Int})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.In[1] != types.Int {
		t.Errorf("chose %v, want the (Any, Int) overload with fewer Any slots", got.In)
	}
}
