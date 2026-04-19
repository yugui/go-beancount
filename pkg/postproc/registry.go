package postproc

import (
	"fmt"

	"github.com/yugui/go-beancount/pkg/postproc/api"
)

// registry maps plugin names to their implementations. It is populated
// at init time via Register and is not safe for concurrent modification.
var registry = map[string]api.Plugin{}

// Register adds p to the global plugin registry under the given name.
// It panics if a plugin with that name has already been registered. This
// is intended to be called from an init() function; the registry is not
// safe for concurrent modification.
//
// By convention, plugin names use the Go fully-qualified package path of
// the implementing package (e.g.
// "github.com/yugui/go-beancount/plugins/auto_accounts"). When multiple
// instances of the same type are registered, prefix with the package path
// and append a distinguishing suffix.
func Register(name string, p api.Plugin) {
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("plugin: %q already registered", name))
	}
	registry[name] = p
}

// lookup returns the registered plugin for the given name. It returns
// the zero value and false if no plugin is registered under that name.
func lookup(name string) (api.Plugin, bool) {
	p, ok := registry[name]
	return p, ok
}
