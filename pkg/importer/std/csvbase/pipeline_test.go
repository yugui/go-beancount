package csvbase_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
)

// TestInsertionOrderEvaluation verifies that a later step can read the value
// produced by an earlier step via Value.
func TestInsertionOrderEvaluation(t *testing.T) {
	b := csvbase.NewBuilder()
	kFirst := csvbase.AddStep(b, func(*csvbase.Cells) (int, *ast.Diagnostic, error) {
		return 10, nil, nil
	})
	var secondSaw int
	kSecond := csvbase.AddStep(b, func(c *csvbase.Cells) (int, *ast.Diagnostic, error) {
		v, _ := csvbase.Value(c, kFirst)
		secondSaw = v
		return v * 2, nil, nil
	})

	var finalVal int
	p := b.Emit(func(_ context.Context, c *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
		finalVal, _ = csvbase.Value(c, kSecond)
		return nil, nil, nil
	})

	rec := csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if secondSaw != 10 {
		t.Errorf("second step saw first step value = %d, want 10", secondSaw)
	}
	if finalVal != 20 {
		t.Errorf("second step result = %d, want 20", finalVal)
	}
}

// TestSoftFailPropagation verifies soft-fail: step A soft-fails with a
// diagnostic, step B reads (zero, diag) via Value and propagates it, and the
// diagnostic surfaces in Map's returned diags.
func TestSoftFailPropagation(t *testing.T) {
	softDiag := csvbase.ErrorDiag("test-soft", "/f.csv", 3, "soft failure")

	b := csvbase.NewBuilder()
	kA := csvbase.AddStep(b, func(*csvbase.Cells) (string, *ast.Diagnostic, error) {
		return "", &softDiag, nil
	})

	// Step B propagates A's diagnostic as its own soft-fail.
	kB := csvbase.AddStep(b, func(c *csvbase.Cells) (string, *ast.Diagnostic, error) {
		v, d := csvbase.Value(c, kA)
		if v != "" {
			unexpected := csvbase.ErrorDiag("unexpected-value", "", 0, "expected zero")
			return "", &unexpected, nil
		}
		return "", d, nil
	})

	p := b.Emit(func(_ context.Context, c *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
		_, d := csvbase.Value(c, kB)
		if d != nil {
			return nil, []ast.Diagnostic{*d}, nil
		}
		return nil, nil, nil
	})

	rec := csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
	dirs, diags, err := p.Map(context.Background(), rec)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("got %d directives, want 0 (row dropped)", len(dirs))
	}
	if len(diags) != 1 || diags[0].Code != "test-soft" {
		t.Errorf("diags = %+v, want one test-soft diagnostic", diags)
	}
}

// TestHardError verifies that a step returning a non-nil error causes Map to
// return that error immediately without invoking emit.
func TestHardError(t *testing.T) {
	boom := errors.New("step boom")
	emitCalled := false

	b := csvbase.NewBuilder()
	csvbase.AddStep(b, func(*csvbase.Cells) (string, *ast.Diagnostic, error) {
		return "", nil, boom
	})
	p := b.Emit(func(_ context.Context, _ *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
		emitCalled = true
		return nil, nil, nil
	})

	rec := csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
	dirs, diags, err := p.Map(context.Background(), rec)
	if err == nil {
		t.Fatal("Map: expected non-nil error from hard-failing step, got nil")
	}
	if !errors.Is(err, boom) {
		t.Errorf("Map error = %v, want %v", err, boom)
	}
	if dirs != nil || diags != nil {
		t.Errorf("Map: expected nil directives and diagnostics on hard error, got dirs=%v diags=%v", dirs, diags)
	}
	if emitCalled {
		t.Error("emit was called despite a hard error in a step; must not be called")
	}
}

// TestEmitDispositions verifies all four emit dispositions are forwarded
// verbatim from Map.
func TestEmitDispositions(t *testing.T) {
	newPipeline := func(ef csvbase.EmitFunc) *csvbase.Pipeline {
		return csvbase.NewBuilder().Emit(ef)
	}
	rec := csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
	ctx := context.Background()

	t.Run("emit", func(t *testing.T) {
		p := newPipeline(func(_ context.Context, _ *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
			return []ast.Directive{&ast.Note{Comment: "ok"}}, nil, nil
		})
		dirs, diags, err := p.Map(ctx, rec)
		if err != nil {
			t.Errorf("emit disposition: unexpected error: %v", err)
		}
		if len(diags) != 0 {
			t.Errorf("emit disposition: got %d diags, want 0", len(diags))
		}
		if len(dirs) != 1 {
			t.Errorf("emit disposition: got %d directives, want 1", len(dirs))
		}
	})

	t.Run("skip", func(t *testing.T) {
		p := newPipeline(func(_ context.Context, _ *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
			return nil, nil, nil
		})
		dirs, diags, err := p.Map(ctx, rec)
		if err != nil {
			t.Errorf("skip disposition: unexpected error: %v", err)
		}
		if diags != nil {
			t.Errorf("skip disposition: got diags %v, want nil", diags)
		}
		if dirs != nil {
			t.Errorf("skip disposition: got dirs %v, want nil", dirs)
		}
	})

	t.Run("drop-with-diag", func(t *testing.T) {
		dg := csvbase.ErrorDiag("drop-code", "", 0, "dropped")
		p := newPipeline(func(_ context.Context, _ *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
			return nil, []ast.Diagnostic{dg}, nil
		})
		dirs, diags, err := p.Map(ctx, rec)
		if err != nil {
			t.Errorf("drop-with-diag disposition: unexpected error: %v", err)
		}
		if len(dirs) != 0 {
			t.Errorf("drop-with-diag disposition: got %d directives, want 0", len(dirs))
		}
		if len(diags) != 1 {
			t.Errorf("drop-with-diag disposition: got %d diags, want 1", len(diags))
		} else if diags[0].Code != "drop-code" {
			t.Errorf("drop-with-diag disposition: diag code = %q, want %q", diags[0].Code, "drop-code")
		}
	})

	t.Run("emit+warn", func(t *testing.T) {
		dg := csvbase.WarnDiag("warn-code", "", 0, "warning")
		p := newPipeline(func(_ context.Context, _ *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
			return []ast.Directive{&ast.Note{Comment: "kept"}}, []ast.Diagnostic{dg}, nil
		})
		dirs, diags, err := p.Map(ctx, rec)
		if err != nil {
			t.Errorf("emit+warn disposition: unexpected error: %v", err)
		}
		if len(dirs) != 1 {
			t.Errorf("emit+warn disposition: got %d directives, want 1", len(dirs))
		}
		if len(diags) != 1 {
			t.Errorf("emit+warn disposition: got %d diags, want 1", len(diags))
		} else if diags[0].Severity != ast.Warning {
			t.Errorf("emit+warn disposition: severity = %v, want Warning", diags[0].Severity)
		}
	})
}

// TestRequired verifies dedup, insertion order, and that the returned slice is
// a copy.
func TestRequired(t *testing.T) {
	b := csvbase.NewBuilder()
	b.Require("C", "A", "B")
	b.Require("A", "D") // A is a dup; D is new
	p := b.Emit(func(_ context.Context, _ *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
		return nil, nil, nil
	})

	want := []string{"C", "A", "B", "D"}
	got := p.Required()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Required() mismatch (-want +got):\n%s", diff)
	}

	// Mutating the returned slice must not affect subsequent calls.
	got[0] = "MUTATED"
	got2 := p.Required()
	if got2[0] != "C" {
		t.Errorf("Required() not a copy: got2[0] = %q, want %q", got2[0], "C")
	}

	// Keys from AddStep that did NOT call Require are absent.
	b2 := csvbase.NewBuilder()
	csvbase.AddStep(b2, func(*csvbase.Cells) (string, *ast.Diagnostic, error) { return "", nil, nil })
	p2 := b2.Emit(func(_ context.Context, _ *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
		return nil, nil, nil
	})
	if r := p2.Required(); len(r) != 0 {
		t.Errorf("Required() without Require calls = %v, want []", r)
	}
}

// TestEmitFreezes verifies that mutations to the Builder after Emit do not
// affect the Pipeline.
func TestEmitFreezes(t *testing.T) {
	b := csvbase.NewBuilder()
	b.Require("X")
	p := b.Emit(func(_ context.Context, _ *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
		return nil, nil, nil
	})

	// Add more steps and required columns after Emit.
	b.Require("Y", "Z")
	csvbase.AddStep(b, func(*csvbase.Cells) (string, *ast.Diagnostic, error) { return "", nil, nil })

	req := p.Required()
	if len(req) != 1 || req[0] != "X" {
		t.Errorf("Required() after Builder mutation = %v, want [X]", req)
	}
}

// TestPipelineEndToEnd drives a Pipeline through the L1 Driver via Config and
// Extract, asserting that directives are produced correctly.
func TestPipelineEndToEnd(t *testing.T) {
	b := csvbase.NewBuilder()
	b.Require("Name", "Score")
	kName := csvbase.AddStep(b, func(c *csvbase.Cells) (string, *ast.Diagnostic, error) {
		return c.Field("Name"), nil, nil
	})
	kScore := csvbase.AddStep(b, func(c *csvbase.Cells) (string, *ast.Diagnostic, error) {
		return c.Field("Score"), nil, nil
	})
	pipeline := b.Emit(func(_ context.Context, c *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
		name, _ := csvbase.Value(c, kName)
		score, _ := csvbase.Value(c, kScore)
		return []ast.Directive{&ast.Note{Comment: name + ":" + score}}, nil, nil
	})

	d, err := csvbase.New("e2e", csvbase.Config{Mapper: pipeline})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	body := "Name,Score\nAlice,95\nBob,80\n"
	out, err := d.Extract(context.Background(), inputStr("/test.csv", body))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 2 {
		t.Fatalf("got %d directives, want 2", len(out.Directives))
	}
	note0, ok0 := out.Directives[0].(*ast.Note)
	if !ok0 {
		t.Fatalf("directive[0] type = %T, want *ast.Note", out.Directives[0])
	}
	if note0.Comment != "Alice:95" {
		t.Errorf("directive[0].Comment = %q, want %q", note0.Comment, "Alice:95")
	}
	note1, ok1 := out.Directives[1].(*ast.Note)
	if !ok1 {
		t.Fatalf("directive[1] type = %T, want *ast.Note", out.Directives[1])
	}
	if note1.Comment != "Bob:80" {
		t.Errorf("directive[1].Comment = %q, want %q", note1.Comment, "Bob:80")
	}
}

// TestPipelineConcurrency exercises Map concurrently to verify there are no
// data races. Run with -race.
func TestPipelineConcurrency(t *testing.T) {
	b := csvbase.NewBuilder()
	b.Require("Val")
	kVal := csvbase.AddStep(b, func(c *csvbase.Cells) (string, *ast.Diagnostic, error) {
		return c.Field("Val"), nil, nil
	})
	pipeline := b.Emit(func(_ context.Context, c *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
		v, _ := csvbase.Value(c, kVal)
		return []ast.Directive{&ast.Note{Comment: v}}, nil, nil
	})

	d, err := csvbase.New("concurrent-pipeline", csvbase.Config{Mapper: pipeline})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	body := "Val\nfoo\nbar\nbaz\n"
	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			out, err := d.Extract(context.Background(), inputStr("/concurrent.csv", body))
			if err != nil {
				t.Errorf("Extract: %v", err)
				return
			}
			if len(out.Directives) != 3 {
				t.Errorf("got %d directives, want 3", len(out.Directives))
			}
		}()
	}
	wg.Wait()
}
