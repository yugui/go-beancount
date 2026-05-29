package env_test

import (
	"errors"
	"testing"

	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// The registry is process-global with no reset hook, so every test uses
// function names unique to that test to avoid cross-test collisions.

func TestResolve_ExactPickAcrossOverloads(t *testing.T) {
	name := "TestResolve_ExactPickAcrossOverloads_f"
	env.Register(scalarFn(name, []types.Type{types.Int}, types.Int))
	env.Register(scalarFn(name, []types.Type{types.String}, types.String))

	got, err := env.Resolve(name, []types.Type{types.Int})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Out != types.Int {
		t.Errorf("resolved Out = %v, want Int", got.Out)
	}
}

func TestResolve_WideningPick(t *testing.T) {
	name := "TestResolve_WideningPick_g"
	env.Register(scalarFn(name, []types.Type{types.Decimal}, types.Decimal))

	got, err := env.Resolve(name, []types.Type{types.Int})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.In[0] != types.Decimal {
		t.Errorf("resolved In[0] = %v, want Decimal (Int widened)", got.In[0])
	}
}

func TestResolve_ExactBeatsWidening(t *testing.T) {
	name := "TestResolve_ExactBeatsWidening_h"
	env.Register(scalarFn(name, []types.Type{types.Int}, types.Int))
	env.Register(scalarFn(name, []types.Type{types.Decimal}, types.Decimal))

	got, err := env.Resolve(name, []types.Type{types.Int})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.In[0] != types.Int {
		t.Errorf("resolved In[0] = %v, want Int (exact beats Int→Decimal widening)", got.In[0])
	}
}

func TestResolve_AmbiguousIsError(t *testing.T) {
	name := "TestResolve_AmbiguousIsError_amb"
	// Both overloads need exactly one Int→Decimal widening to accept
	// (Int, Int), so neither dominates.
	env.Register(scalarFn(name, []types.Type{types.Decimal, types.Int}, types.Decimal))
	env.Register(scalarFn(name, []types.Type{types.Int, types.Decimal}, types.Decimal))

	_, err := env.Resolve(name, []types.Type{types.Int, types.Int})
	if !errors.Is(err, env.ErrAmbiguous) {
		t.Fatalf("Resolve error = %v, want wrapping ErrAmbiguous", err)
	}
}

func TestResolve_NoMatch(t *testing.T) {
	name := "TestResolve_NoMatch_f"
	env.Register(scalarFn(name, []types.Type{types.Int}, types.Int))

	cases := map[string]struct {
		name string
		args []types.Type
	}{
		"unknown name":       {"TestResolve_NoMatch_unregistered", []types.Type{types.Int}},
		"wrong arity":        {name, []types.Type{types.Int, types.Int}},
		"no viable widening": {name, []types.Type{types.String}},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			_, err := env.Resolve(tc.name, tc.args)
			if !errors.Is(err, env.ErrNoOverload) {
				t.Fatalf("Resolve error = %v, want wrapping ErrNoOverload", err)
			}
		})
	}
}
