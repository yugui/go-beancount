package postproc

import (
	"fmt"
	"sync"

	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// registry maps plugin names to their implementations. The map is
// guarded by registryMu so that registration via Register and lookups
// via lookup (the path Apply takes) may safely interleave even when
// goplug.Load is invoked after main has started.
var (
	registryMu sync.RWMutex
	registry   = map[string]api.Plugin{}
)

// Register adds p to the global plugin registry under the given name.
// It panics if a plugin with that name has already been registered.
// Register is safe for concurrent use; goplug.Load relies on this so
// dynamically loaded plugins can register themselves while a host
// application continues to run.
//
// By convention, plugin names use the Go fully-qualified package path of
// the implementing package (e.g.
// "github.com/yugui/go-beancount/plugins/auto_accounts"). When multiple
// instances of the same type are registered, prefix with the package path
// and append a distinguishing suffix.
func Register(name string, p api.Plugin) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("postproc: plugin %q already registered", name))
	}
	registry[name] = p
}

// lookup returns the registered plugin for the given name. It returns
// the zero value and false if no plugin is registered under that name.
func lookup(name string) (api.Plugin, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[name]
	return p, ok
}
