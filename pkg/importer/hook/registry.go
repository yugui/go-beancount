package hook

import (
	"fmt"
	"sort"
	"sync"
)

// concurrent access guarded by registryMu
var (
	registryMu sync.RWMutex
	registry   = map[string]Hook{}
)

// Registry is the lookup-and-iteration interface that Chain accepts so callers
// can substitute a fake in tests. The package-global registry returned by
// GlobalRegistry satisfies this interface.
//
// Names returns names in ascending sorted order; this ordering is contractual
// for listing UIs and test determinism. Unlike [github.com/yugui/go-beancount/pkg/importer.Registry],
// the sort order is not load-bearing for the chain runner — Chain uses
// caller-supplied order, not registry order.
type Registry interface {
	Lookup(name string) (Hook, bool)
	Names() []string
}

// Register adds h to the package-global registry under the given name. It
// panics if the name has already been registered. Intended to be called from
// init (in-tree hooks) or from a goplug InitPlugin callback (plugin hooks).
// Safe for concurrent use.
func Register(name string, h Hook) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("hook: duplicate Hook registration for %q", name))
	}
	registry[name] = h
}

// Lookup returns the registered Hook for name. The second return value is
// false if no such hook is registered.
func Lookup(name string) (Hook, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	h, ok := registry[name]
	return h, ok
}

// Names returns the registered hook names. See [Registry.Names] for the
// ordering contract.
func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type globalRegistry struct{}

func (globalRegistry) Lookup(name string) (Hook, bool) { return Lookup(name) }
func (globalRegistry) Names() []string                 { return Names() }

// GlobalRegistry returns a Registry view over the package-global state.
func GlobalRegistry() Registry {
	return globalRegistry{}
}
