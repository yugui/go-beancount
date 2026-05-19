package hook

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"unsafe"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
)

// sameSliceData reports whether a and b share the same backing array.
// Two empty (or nil) slices are considered to share backing regardless of
// their pointer values, since an empty slice has no meaningful data pointer.
func sameSliceData(a, b []ast.Directive) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	return unsafe.SliceData(a) == unsafe.SliceData(b)
}

// fakeRegistry implements Registry without touching global state.
type fakeRegistry struct {
	hooks []Hook
}

func (r *fakeRegistry) Lookup(name string) (Hook, bool) {
	for _, h := range r.hooks {
		if h.Name() == name {
			return h, true
		}
	}
	return nil, false
}

func (r *fakeRegistry) Names() []string {
	// reverse of registration order — ensures tests cannot pass by accident
	// if Chain were to sort or use registry order instead of caller-supplied order.
	names := make([]string, len(r.hooks))
	for i, h := range r.hooks {
		names[len(r.hooks)-1-i] = h.Name()
	}
	return names
}

func TestChain_EmptyNames(t *testing.T) {
	reg := &fakeRegistry{}
	directives := []ast.Directive{&ast.Transaction{}}

	cases := []struct {
		name       string
		names      []string
		directives []ast.Directive
		wantNil    bool
	}{
		{"nil names, non-empty directives", nil, directives, false},
		{"empty names, non-empty directives", []string{}, directives, false},
		{"nil names, nil directives", nil, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Chain(context.Background(), reg, tc.names, HookInput{Directives: tc.directives})
			if err != nil {
				t.Fatalf("Chain returned error: %v", err)
			}
			if result.Diagnostics != nil {
				t.Errorf("Diagnostics = %v, want nil", result.Diagnostics)
			}
			if tc.wantNil {
				if result.Directives != nil {
					t.Errorf("Directives = %v, want nil", result.Directives)
				}
			} else {
				if !sameSliceData(result.Directives, tc.directives) {
					t.Error("Chain with empty names did not return the same backing array")
				}
			}
		})
	}
}

func TestChain_NilDirectivesNormalisedForNonEmptyNames(t *testing.T) {
	var gotDirectives []ast.Directive
	reg := &fakeRegistry{
		hooks: []Hook{
			&fakeHook{
				name: "a",
				applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
					gotDirectives = in.Directives
					return HookResult{Directives: in.Directives}, nil
				},
			},
		},
	}

	_, err := Chain(context.Background(), reg, []string{"a"}, HookInput{Directives: nil})
	if err != nil {
		t.Fatalf("Chain returned error: %v", err)
	}
	if gotDirectives == nil {
		t.Error("Chain did not normalise nil Directives to a non-nil empty slice before invoking the hook")
	}
	if len(gotDirectives) != 0 {
		t.Errorf("normalised Directives len = %d, want 0", len(gotDirectives))
	}
}

func TestChain_SingleHook(t *testing.T) {
	input := []ast.Directive{&ast.Transaction{}}
	output := []ast.Directive{&ast.Transaction{}}

	reg := &fakeRegistry{
		hooks: []Hook{
			&fakeHook{
				name: "a",
				applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
					return HookResult{Directives: output}, nil
				},
			},
		},
	}

	result, err := Chain(context.Background(), reg, []string{"a"}, HookInput{Directives: input})
	if err != nil {
		t.Fatalf("Chain returned error: %v", err)
	}
	if !sameSliceData(result.Directives, output) {
		t.Error("Chain single hook: result Directives does not share backing array with hook output")
	}
}

func TestChain_MultipleHooksOrderPreserved(t *testing.T) {
	// "b" runs first, "a" runs second — caller-supplied order, not sorted.
	var callOrder []string

	reg := &fakeRegistry{
		hooks: []Hook{
			&fakeHook{
				name: "a",
				applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
					callOrder = append(callOrder, "a")
					return HookResult{Directives: in.Directives}, nil
				},
			},
			&fakeHook{
				name: "b",
				applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
					callOrder = append(callOrder, "b")
					return HookResult{Directives: in.Directives}, nil
				},
			},
		},
	}

	_, err := Chain(context.Background(), reg, []string{"b", "a"}, HookInput{Directives: []ast.Directive{}})
	if err != nil {
		t.Fatalf("Chain returned error: %v", err)
	}
	want := []string{"b", "a"}
	if diff := cmp.Diff(want, callOrder); diff != "" {
		t.Errorf("call order mismatch (-want +got):\n%s", diff)
	}
}

func TestChain_MissingRungHaltsWithDiag(t *testing.T) {
	directives := []ast.Directive{&ast.Transaction{}}

	reg := &fakeRegistry{
		hooks: []Hook{
			&fakeHook{name: "ok"},
		},
	}

	t.Run("MissingLast", func(t *testing.T) {
		result, err := Chain(context.Background(), reg, []string{"ok", "missing"}, HookInput{Directives: directives})
		if err != nil {
			t.Fatalf("Chain returned error on missing rung: %v", err)
		}
		wantDiags := []ast.Diagnostic{{
			Code:     DiagHookNotRegistered,
			Message:  `hook "missing" is not registered`,
			Severity: ast.Error,
		}}
		if diff := cmp.Diff(wantDiags, result.Diagnostics); diff != "" {
			t.Errorf("Diagnostics mismatch (-want +got):\n%s", diff)
		}
		if !sameSliceData(result.Directives, directives) {
			t.Error("MissingRung: Directives backing array changed unexpectedly")
		}
	})

	t.Run("MissingFirst_SubsequentHookNotCalled", func(t *testing.T) {
		afterCalled := false
		regWithAfter := &fakeRegistry{
			hooks: []Hook{
				&fakeHook{name: "ok"},
				&fakeHook{
					name: "after",
					applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
						afterCalled = true
						return HookResult{Directives: in.Directives}, nil
					},
				},
			},
		}
		result, err := Chain(context.Background(), regWithAfter, []string{"missing", "after"}, HookInput{Directives: directives})
		if err != nil {
			t.Fatalf("Chain returned error on missing rung: %v", err)
		}
		if afterCalled {
			t.Error("hook after missing rung was called; Chain should have halted")
		}
		wantDiags := []ast.Diagnostic{{
			Code:     DiagHookNotRegistered,
			Message:  `hook "missing" is not registered`,
			Severity: ast.Error,
		}}
		if diff := cmp.Diff(wantDiags, result.Diagnostics); diff != "" {
			t.Errorf("Diagnostics mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestChain_ApplyErrorHaltsAndPreservesDiagnostics(t *testing.T) {
	applyErr := errors.New("hook system failure")
	errDiag := ast.Diagnostic{Code: "partial", Severity: ast.Warning, Message: "hook partial"}
	priorDiag := ast.Diagnostic{Code: "prior", Severity: ast.Warning, Message: "from first hook"}

	before := []ast.Directive{&ast.Transaction{}}
	after := []ast.Directive{&ast.Transaction{}}

	reg := &fakeRegistry{
		hooks: []Hook{
			&fakeHook{
				name: "first",
				applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
					return HookResult{
						Directives:  after,
						Diagnostics: []ast.Diagnostic{priorDiag},
					}, nil
				},
			},
			&fakeHook{
				name: "failing",
				applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
					return HookResult{
						Diagnostics: []ast.Diagnostic{errDiag},
					}, applyErr
				},
			},
		},
	}

	result, err := Chain(context.Background(), reg, []string{"first", "failing"}, HookInput{Directives: before})
	if !errors.Is(err, applyErr) {
		t.Errorf("Chain error = %v, want %v", err, applyErr)
	}
	if !sameSliceData(result.Directives, after) {
		t.Error("ApplyError: Directives must be from the prior rung's output")
	}
	want := []ast.Diagnostic{priorDiag, errDiag}
	if diff := cmp.Diff(want, result.Diagnostics); diff != "" {
		t.Errorf("Diagnostics mismatch (-want +got):\n%s", diff)
	}
}

func TestChain_CtxCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	priorDiag := ast.Diagnostic{Code: "d1", Severity: ast.Warning, Message: "before cancel"}
	directives := []ast.Directive{&ast.Transaction{}}

	reg := &fakeRegistry{
		hooks: []Hook{
			&fakeHook{
				name: "first",
				applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
					cancel()
					return HookResult{
						Directives:  directives,
						Diagnostics: []ast.Diagnostic{priorDiag},
					}, nil
				},
			},
			&fakeHook{
				name: "second",
				applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
					t.Error("second hook called after context cancellation")
					return HookResult{Directives: in.Directives}, nil
				},
			},
		},
	}

	result, err := Chain(ctx, reg, []string{"first", "second"}, HookInput{Directives: directives})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Chain error = %v, want context.Canceled", err)
	}
	if diff := cmp.Diff([]ast.Diagnostic{priorDiag}, result.Diagnostics); diff != "" {
		t.Errorf("Diagnostics mismatch (-want +got):\n%s", diff)
	}
	if !sameSliceData(result.Directives, directives) {
		t.Error("CtxCancellation: Directives backing array changed unexpectedly")
	}
}

func TestChain_DiagnosticsOrderFromSuccessiveRungs(t *testing.T) {
	d1 := ast.Diagnostic{Code: "d1", Severity: ast.Error, Message: "rung one"}
	d2 := ast.Diagnostic{Code: "d2", Severity: ast.Error, Message: "rung two"}
	d3 := ast.Diagnostic{Code: "d3", Severity: ast.Error, Message: "rung three"}

	reg := &fakeRegistry{
		hooks: []Hook{
			&fakeHook{
				name: "a",
				applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
					return HookResult{Directives: in.Directives, Diagnostics: []ast.Diagnostic{d1}}, nil
				},
			},
			&fakeHook{
				name: "b",
				applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
					return HookResult{Directives: in.Directives, Diagnostics: []ast.Diagnostic{d2, d3}}, nil
				},
			},
		},
	}

	result, err := Chain(context.Background(), reg, []string{"a", "b"}, HookInput{Directives: []ast.Directive{}})
	if err != nil {
		t.Fatalf("Chain returned error: %v", err)
	}
	want := []ast.Diagnostic{d1, d2, d3}
	if diff := cmp.Diff(want, result.Diagnostics); diff != "" {
		t.Errorf("Diagnostics mismatch (-want +got):\n%s", diff)
	}
}

func TestChain_NilDiagnosticsWhenNoneEmitted(t *testing.T) {
	reg := &fakeRegistry{
		hooks: []Hook{
			&fakeHook{name: "a"},
			&fakeHook{name: "b"},
		},
	}

	result, err := Chain(context.Background(), reg, []string{"a", "b"}, HookInput{Directives: []ast.Directive{}})
	if err != nil {
		t.Fatalf("Chain returned error: %v", err)
	}
	if result.Diagnostics != nil {
		t.Errorf("Diagnostics = %v, want nil when no rung emits diagnostics", result.Diagnostics)
	}
}

func TestChain_HintsAndOptionsPassedThroughUnchanged(t *testing.T) {
	hints := map[string]string{"account": "Assets:Cash"}
	hintsID := reflect.ValueOf(hints).Pointer()
	opts := &ast.OptionValues{}
	var capturedHints []uintptr
	var capturedOpts []*ast.OptionValues

	reg := &fakeRegistry{
		hooks: []Hook{
			&fakeHook{
				name: "a",
				applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
					capturedHints = append(capturedHints, reflect.ValueOf(in.Hints).Pointer())
					capturedOpts = append(capturedOpts, in.Options)
					return HookResult{Directives: in.Directives}, nil
				},
			},
			&fakeHook{
				name: "b",
				applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
					capturedHints = append(capturedHints, reflect.ValueOf(in.Hints).Pointer())
					capturedOpts = append(capturedOpts, in.Options)
					return HookResult{Directives: in.Directives}, nil
				},
			},
		},
	}

	_, err := Chain(context.Background(), reg, []string{"a", "b"}, HookInput{
		Directives: []ast.Directive{},
		Hints:      hints,
		Options:    opts,
	})
	if err != nil {
		t.Fatalf("Chain returned error: %v", err)
	}
	for i, id := range capturedHints {
		if id != hintsID {
			t.Errorf("rung %d received a different Hints map pointer", i)
		}
	}
	for i, o := range capturedOpts {
		if o != opts {
			t.Errorf("rung %d received a different Options pointer", i)
		}
	}
}
