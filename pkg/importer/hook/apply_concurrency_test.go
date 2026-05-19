package hook

import (
	"context"
	"sync"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestConcurrency_ApplyOnFrozenHook(t *testing.T) {
	// Verify Apply is safe for concurrent invocation once the Hook is constructed.
	f := FactoryFunc(func(name string, decode func(dest any) error) (Hook, error) {
		return &fakeHook{
			name: name,
			applyFn: func(_ context.Context, in HookInput) (HookResult, error) {
				return HookResult{Directives: in.Directives}, nil
			},
		}, nil
	})

	h, err := f.New("concurrent", func(dest any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 32
	var wg sync.WaitGroup
	in := HookInput{Directives: []ast.Directive{&ast.Transaction{}}}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.Apply(context.Background(), in)
			if err != nil {
				t.Errorf("Apply returned unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
}
