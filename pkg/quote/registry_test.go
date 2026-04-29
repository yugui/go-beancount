package quote

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/pkg/quote/api"
)

// fakeSource is a minimal api.Source for registry tests.
type fakeSource struct{ name string }

func (f *fakeSource) Name() string                   { return f.name }
func (f *fakeSource) Capabilities() api.Capabilities { return api.Capabilities{} }

// withCleanRegistry swaps the global registry for an empty one for
// the duration of a single test and restores the previous contents
// in t.Cleanup. It mirrors the pattern used in pkg/ext/postproc.
func withCleanRegistry(t *testing.T) {
	t.Helper()
	registryMu.Lock()
	old := registry
	registry = map[string]api.Source{}
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		registry = old
		registryMu.Unlock()
	})
}

func TestRegister_RoundTrip(t *testing.T) {
	withCleanRegistry(t)

	yahoo := &fakeSource{name: "yahoo"}
	google := &fakeSource{name: "google"}
	Register("yahoo", yahoo)
	Register("google", google)

	t.Run("Lookup", func(t *testing.T) {
		cases := []struct {
			name string
			want api.Source
		}{
			{"yahoo", yahoo},
			{"google", google},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, ok := Lookup(tc.name)
				if !ok {
					t.Fatalf("Lookup(%q) returned ok=false", tc.name)
				}
				if got != tc.want {
					t.Errorf("Lookup(%q) = %v, want %v", tc.name, got, tc.want)
				}
			})
		}
	})

	t.Run("Names", func(t *testing.T) {
		want := []string{"google", "yahoo"}
		got := Names()
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Names() mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestRegister_DuplicatePanics(t *testing.T) {
	withCleanRegistry(t)
	Register("yahoo", &fakeSource{name: "yahoo"})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Register did not panic on duplicate name")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "yahoo") {
			t.Errorf("panic = %q, want it to contain %q", msg, "yahoo")
		}
	}()
	Register("yahoo", &fakeSource{name: "yahoo-2"})
}

func TestLookup_Missing(t *testing.T) {
	withCleanRegistry(t)

	got, ok := Lookup("nonexistent")
	if ok {
		t.Errorf("Lookup(\"nonexistent\") returned ok=true with %v", got)
	}
	if got != nil {
		t.Errorf("Lookup(\"nonexistent\") = %v, want nil", got)
	}
}

func TestGlobalRegistry(t *testing.T) {
	withCleanRegistry(t)

	yahoo := &fakeSource{name: "yahoo"}
	Register("yahoo", yahoo)

	gr := GlobalRegistry()

	t.Run("ResolvesRegisteredName", func(t *testing.T) {
		got, ok := gr.Lookup("yahoo")
		if !ok {
			t.Fatal("GlobalRegistry().Lookup(\"yahoo\") returned ok=false")
		}
		direct, _ := Lookup("yahoo")
		if got != direct {
			t.Errorf("GlobalRegistry().Lookup = %v, package Lookup = %v", got, direct)
		}
	})

	t.Run("MissingName", func(t *testing.T) {
		_, ok := gr.Lookup("nonexistent")
		if ok {
			t.Error("GlobalRegistry().Lookup(\"nonexistent\") returned ok=true")
		}
	})
}
