package asttest_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast/asttest"
)

func TestMustOptions_RoundTripsThroughAccessors(t *testing.T) {
	opts := asttest.MustOptions(t, map[string]string{
		"inferred_tolerance_multiplier": "0.25",
		"infer_tolerance_from_cost":     "TRUE",
	})
	if got := opts.Decimal("inferred_tolerance_multiplier").String(); got != "0.25" {
		t.Errorf("Decimal(inferred_tolerance_multiplier) = %q, want %q", got, "0.25")
	}
	if got := opts.Bool("infer_tolerance_from_cost"); got != true {
		t.Errorf("Bool(infer_tolerance_from_cost) = %v, want true", got)
	}
}

func TestMustOptions_EmptyReturnsDefaults(t *testing.T) {
	opts := asttest.MustOptions(t, nil)
	if got := opts.Decimal("inferred_tolerance_multiplier").String(); got != "0.5" {
		t.Errorf("Decimal default = %q, want %q", got, "0.5")
	}
}

func TestMustOptions_ParseErrorFatals(t *testing.T) {
	fake := &fatalRecorder{TB: t}
	asttest.MustOptions(fake, map[string]string{
		"inferred_tolerance_multiplier": "not-a-decimal",
	})
	if !fake.fataled {
		t.Errorf("MustOptions did not call Fatalf on a malformed option")
	}
}

// fatalRecorder intercepts Fatalf to observe whether MustOptions calls it.
// Smoke-test scope: confirms Fatalf is invoked but does not simulate
// runtime.Goexit, so MustOptions continues after the intercepted call.
// Direct test on fatalRecorder (unexported) avoids a subprocess or t.Run
// indirection that would obscure the single observable property under test.
type fatalRecorder struct {
	testing.TB
	fataled bool
}

func (f *fatalRecorder) Fatalf(string, ...any) { f.fataled = true }
func (f *fatalRecorder) Helper()               {}
