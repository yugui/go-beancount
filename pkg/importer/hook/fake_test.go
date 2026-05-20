package hook

import (
	"context"
)

type fakeHook struct {
	name    string
	applyFn func(ctx context.Context, in HookInput) (HookResult, error)
}

func (f *fakeHook) Name() string { return f.name }

func (f *fakeHook) Apply(ctx context.Context, in HookInput) (HookResult, error) {
	if f.applyFn == nil {
		return HookResult{Directives: in.Directives}, nil
	}
	return f.applyFn(ctx, in)
}

// withCleanKindRegistry swaps the global kind registry for an empty one for
// the duration of a single test and restores it in t.Cleanup. Direct access
// to the unexported global is justified here because the package has no
// exported reset API and the concurrent-stress test requires atomic swap.
// Must not be used with t.Parallel(); the global swap is process-wide.
func withCleanKindRegistry(t interface {
	Helper()
	Cleanup(func())
}) {
	t.Helper()
	kindMu.Lock()
	old := kinds
	kinds = map[string]Factory{}
	kindMu.Unlock()
	t.Cleanup(func() {
		kindMu.Lock()
		kinds = old
		kindMu.Unlock()
	})
}
