package quote

import (
	"fmt"
	"sort"
	"sync"

	"github.com/yugui/go-beancount/pkg/quote/api"
)

// registry maps source names to their implementations. Population
// happens at init time via Register; Lookup and Names are read-only
// operations that the orchestrator may invoke concurrently from
// goroutines spawned by Fetch, hence the RWMutex.
var (
	registryMu sync.RWMutex
	registry   = map[string]api.Source{}
)

// Registry is the lookup interface used by Fetch so callers can
// substitute a fake registry in tests. The package-global registry
// returned by GlobalRegistry satisfies this interface.
type Registry interface {
	// Lookup returns the registered source for name. The second
	// return value is false if no such source is registered.
	Lookup(name string) (api.Source, bool)
}

// Register installs s under the given name in the package-global
// registry. It panics if a source has already been registered under
// the same name. Register is intended to be called from an init()
// function (for in-tree quoters) or from a goplug InitPlugin callback
// (for out-of-tree `.so` quoters).
//
// By convention, the name is the upstream tool's own name when the
// source is emulating one (e.g. "yahoo", "google"); otherwise it is
// the Go fully-qualified package path of the implementing package, to
// avoid collisions across independently developed plugins.
func Register(name string, s api.Source) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("quote: duplicate Source registration for %q", name))
	}
	registry[name] = s
}

// Lookup returns the registered source for name. The second return
// value is false if no such source is registered.
func Lookup(name string) (api.Source, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	s, ok := registry[name]
	return s, ok
}

// Names returns the list of registered source names sorted in
// ascending order so that callers (notably diagnostic messages and
// tests) get deterministic output regardless of map iteration order.
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

// globalRegistry is the unexported adapter type that exposes the
// package-global registry through the Registry interface.
type globalRegistry struct{}

func (globalRegistry) Lookup(name string) (api.Source, bool) {
	return Lookup(name)
}

// GlobalRegistry returns a Registry view over the package-global
// state. This lets callers hand the global registry to Fetch (or
// other consumers parameterised on Registry) without exposing the
// underlying map.
func GlobalRegistry() Registry {
	return globalRegistry{}
}
