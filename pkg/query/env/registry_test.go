package env_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// The registry is process-global with no reset hook, so every test uses
// function names unique to that test to avoid cross-test collisions.

func scalarFn(name string, in []types.Type, out types.Type) api.Function {
	return api.Function{
		Name:   name,
		In:     in,
		Out:    out,
		Flavor: api.ScalarFlavor,
		Scalar: api.Pure(func([]types.Value) (types.Value, error) { return types.Null(out), nil }),
	}
}

func aggregatorFn(name string, in []types.Type, out types.Type) api.Function {
	return api.Function{
		Name:       name,
		In:         in,
		Out:        out,
		Flavor:     api.AggregatorFlavor,
		Aggregator: func() api.Accumulator { return nil },
	}
}

func mustPanic(t *testing.T, call func()) (msg string) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic, got none")
		}
		msg = fmt.Sprint(r)
	}()
	call()
	return ""
}

func TestRegister_DuplicateSignaturePanics(t *testing.T) {
	in := []types.Type{types.Int}
	env.Register(scalarFn("TestRegister_DuplicateSignaturePanics_f", in, types.Int))

	mustPanic(t, func() {
		env.Register(scalarFn("TestRegister_DuplicateSignaturePanics_f", in, types.Int))
	})
}

func TestRegister_DistinctSignaturesCoexist(t *testing.T) {
	name := "TestRegister_DistinctSignaturesCoexist_f"
	env.Register(scalarFn(name, []types.Type{types.Int}, types.Int))
	env.Register(scalarFn(name, []types.Type{types.String}, types.String))

	got, err := env.Resolve(name, []types.Type{types.String})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Out != types.String {
		t.Errorf("resolved Out = %v, want String", got.Out)
	}
}

func TestRegister_CaseInsensitiveName(t *testing.T) {
	env.Register(scalarFn("TestRegister_CaseInsensitive_MixedCase", []types.Type{types.Int}, types.Int))

	if _, err := env.Resolve("testregister_caseinsensitive_mixedcase", []types.Type{types.Int}); err != nil {
		t.Errorf("lowercased Resolve: %v", err)
	}
	if _, err := env.Resolve("TESTREGISTER_CASEINSENSITIVE_MIXEDCASE", []types.Type{types.Int}); err != nil {
		t.Errorf("uppercased Resolve: %v", err)
	}

	// A duplicate under a differently-cased spelling still collides.
	mustPanic(t, func() {
		env.Register(scalarFn("TESTREGISTER_CASEINSENSITIVE_MIXEDCASE", []types.Type{types.Int}, types.Int))
	})
}

func TestRegister_MalformedDescriptorPanics(t *testing.T) {
	cases := map[string]api.Function{
		"scalar without impl": {
			Name: "TestRegister_Malformed_a", In: []types.Type{types.Int}, Out: types.Int,
			Flavor: api.ScalarFlavor,
		},
		"scalar with aggregator": {
			Name: "TestRegister_Malformed_b", In: []types.Type{types.Int}, Out: types.Int,
			Flavor:     api.ScalarFlavor,
			Scalar:     api.Pure(func([]types.Value) (types.Value, error) { return nil, nil }),
			Aggregator: func() api.Accumulator { return nil },
		},
		"aggregator without impl": {
			Name: "TestRegister_Malformed_c", In: []types.Type{types.Int}, Out: types.Int,
			Flavor: api.AggregatorFlavor,
		},
		"aggregator with scalar": {
			Name: "TestRegister_Malformed_d", In: []types.Type{types.Int}, Out: types.Int,
			Flavor:     api.AggregatorFlavor,
			Scalar:     api.Pure(func([]types.Value) (types.Value, error) { return nil, nil }),
			Aggregator: func() api.Accumulator { return nil },
		},
		"unknown flavor": {
			Name: "TestRegister_Malformed_e", In: []types.Type{types.Int}, Out: types.Int,
			Flavor: api.Flavor(99),
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			mustPanic(t, func() { env.Register(fn) })
		})
	}
}

func TestRegister_AggregatorRoundTrips(t *testing.T) {
	name := "TestRegister_AggregatorRoundTrips_agg"
	env.Register(aggregatorFn(name, []types.Type{types.Int}, types.Int))

	got, err := env.Resolve(name, []types.Type{types.Int})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Flavor != api.AggregatorFlavor || got.Aggregator == nil {
		t.Errorf("resolved overload is not a usable aggregator: %+v", got)
	}
}

// Run with -race to assert the registry's mutex discipline holds under
// concurrent Register and Resolve over distinct names.
func TestRegistry_ConcurrentRegisterAndResolve(t *testing.T) {
	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		name := fmt.Sprintf("TestRegistry_Concurrent_f%d", i)
		go func() {
			defer wg.Done()
			env.Register(scalarFn(name, []types.Type{types.Int}, types.Int))
		}()
		go func() {
			defer wg.Done()
			// May or may not observe the registration depending on
			// scheduling; we only assert the access is race-free.
			_, _ = env.Resolve(name, []types.Type{types.Int})
		}()
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		name := fmt.Sprintf("TestRegistry_Concurrent_f%d", i)
		if _, err := env.Resolve(name, []types.Type{types.Int}); err != nil {
			t.Errorf("Resolve(%s) after concurrent register: %v", name, err)
		}
	}
}
