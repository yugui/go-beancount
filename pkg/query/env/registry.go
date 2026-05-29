package env

import (
	"fmt"
	"strings"
	"sync"

	"github.com/yugui/go-beancount/pkg/query/api"
)

// registry maps a lowercased function name to its set of overloads. The
// map is guarded by registryMu so registration via Register and lookups
// via overloads (the path Resolve takes) may safely interleave even when a
// future goplug registers functions after main has started.
var (
	registryMu sync.RWMutex
	registry   = map[string][]api.Function{}
)

// Register adds fn to the global function registry, intended to be called
// at init time. The name is lowercased to form the overload-set key, so
// lookups are case-insensitive.
//
// Distinct overloads of one name (differing in their [api.Function.In]
// signature) coexist; that is how overloading works. Register panics, with
// a message prefixed "query/env:", if fn is malformed (its implementation
// fields do not match its flavor) or if an overload with an identical In
// signature is already registered under the same name.
//
// Register is safe for concurrent use.
func Register(fn api.Function) {
	if err := validate(fn); err != nil {
		panic(fmt.Sprintf("query/env: %v", err))
	}
	key := strings.ToLower(fn.Name)

	registryMu.Lock()
	defer registryMu.Unlock()
	for _, existing := range registry[key] {
		if sameSignature(existing.In, fn.In) {
			panic(fmt.Sprintf("query/env: function %q already registered with signature %s", fn.Name, formatSignature(fn.In)))
		}
	}
	registry[key] = append(registry[key], fn)
}

// overloads returns the overloads registered under the lowercased name. It
// returns nil when none are registered. The returned slice must not be
// mutated by callers.
func overloads(name string) []api.Function {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[strings.ToLower(name)]
}

func validate(fn api.Function) error {
	switch fn.Flavor {
	case api.ScalarFlavor:
		if fn.Scalar == nil {
			return fmt.Errorf("function %q is scalar but has no Scalar implementation", fn.Name)
		}
		if fn.Aggregator != nil {
			return fmt.Errorf("function %q is scalar but also sets an Aggregator", fn.Name)
		}
	case api.AggregatorFlavor:
		if fn.Aggregator == nil {
			return fmt.Errorf("function %q is an aggregator but has no Aggregator implementation", fn.Name)
		}
		if fn.Scalar != nil {
			return fmt.Errorf("function %q is an aggregator but also sets a Scalar", fn.Name)
		}
	case api.PassContextFlavor:
		return fmt.Errorf("function %q uses reserved PassContextFlavor, which the lean engine does not register", fn.Name)
	default:
		return fmt.Errorf("function %q has unknown flavor %d", fn.Name, fn.Flavor)
	}
	return nil
}
