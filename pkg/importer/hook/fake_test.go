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

type fakeConfigurableHook struct {
	fakeHook
	configureCalled bool
}

func (c *fakeConfigurableHook) Configure(decode func(dest any) error) error {
	c.configureCalled = true
	return decode(new(any))
}

// withCleanRegistry swaps the global registry for an empty one for the
// duration of a single test and restores it in t.Cleanup. Direct access to
// the unexported global is justified here: the package has no exported reset
// API and the concurrent-stress test requires atomic swap.
func withCleanRegistry(t interface {
	Helper()
	Cleanup(func())
}) {
	t.Helper()
	registryMu.Lock()
	old := registry
	registry = map[string]Hook{}
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		registry = old
		registryMu.Unlock()
	})
}
