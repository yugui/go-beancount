package importer

import (
	"fmt"
	"sort"
	"sync"
)

// concurrent access guarded by registryMu
var (
	registryMu sync.RWMutex
	registry   = map[string]Importer{}
)

// Registry is the lookup-and-iteration interface that Dispatch accepts so
// callers can substitute a fake in tests. The package-global registry
// returned by GlobalRegistry satisfies this interface.
//
// Names returns names in ascending sorted order; that order is part of the
// Registry contract because Dispatch's first-match behaviour is only
// meaningful with a stable iteration order.
type Registry interface {
	Lookup(name string) (Importer, bool)
	Names() []string
}

// Register adds imp to the package-global registry under the given name. It
// panics if a name has already been registered. Intended to be called from
// init (in-tree importers) or from a goplug InitPlugin callback (plugin
// importers). Safe for concurrent use.
func Register(name string, imp Importer) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("importer: duplicate Importer registration for %q", name))
	}
	registry[name] = imp
}

// Lookup returns the registered Importer for name. The second return value is
// false if no such importer is registered.
func Lookup(name string) (Importer, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	imp, ok := registry[name]
	return imp, ok
}

// Names returns the registered importer names sorted in ascending order. The
// sort order is part of the contract because Dispatch walks names in this
// order and the first-match behaviour is only meaningful with a stable order.
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

func (globalRegistry) Lookup(name string) (Importer, bool) { return Lookup(name) }
func (globalRegistry) Names() []string                     { return Names() }

// GlobalRegistry returns a Registry view over the package-global state.
func GlobalRegistry() Registry {
	return globalRegistry{}
}
