package hook

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Kind registry — populated at init time or via goplug InitPlugin callbacks

var (
	kindMu sync.RWMutex
	kinds  = map[string]Factory{}
)

// RegisterFactory installs f under the given kind in the package-global kind
// registry. Panics if a Factory has already been registered under the same
// kind. Intended to be called from an init() function or a goplug InitPlugin
// callback. Safe for concurrent use alongside [New] and [KindNames]; in
// practice all registrations land before reads begin.
func RegisterFactory(kind string, f Factory) {
	kindMu.Lock()
	defer kindMu.Unlock()
	if _, exists := kinds[kind]; exists {
		panic(fmt.Sprintf("hook: duplicate Factory registration for %q", kind))
	}
	kinds[kind] = f
}

// New constructs a configured Hook of the given kind. One-shot form of
// lookupFactory + Factory.New. Returns an error if kind is not registered.
func New(kind, name string, decode func(dest any) error) (Hook, error) {
	f, ok := lookupFactory(kind)
	if !ok {
		return nil, fmt.Errorf("hook: unknown kind %q", kind)
	}
	return f.New(name, decode)
}

func lookupFactory(kind string) (Factory, bool) {
	kindMu.RLock()
	defer kindMu.RUnlock()
	f, ok := kinds[kind]
	return f, ok
}

// KindNames returns the registered kinds sorted in ascending order so that
// diagnostics and tests have deterministic output.
func KindNames() []string {
	kindMu.RLock()
	defer kindMu.RUnlock()
	names := make([]string, 0, len(kinds))
	for k := range kinds {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Instance registry — built per run from configured Hook values

// Registry is the per-run lookup of fully-configured Hook instances. The CLI
// builds one Registry per beanimport invocation from the [[hook]] entries in
// TOML and hands it to Chain.
//
// Names returns instance names in the order Chain walks them: the order they
// were supplied to [NewRegistry] (declaration order in TOML). Implementations
// MUST preserve a stable, deterministic order across repeated calls on the
// same Registry value.
//
// All Registry methods are safe for concurrent use. A Registry's contents are
// immutable after construction.
type Registry interface {
	Lookup(name string) (Hook, bool)
	Names() []string
}

// MapRegistry is the default in-memory Registry implementation. Build one with
// [NewRegistry]; the zero value behaves as an empty Registry.
type MapRegistry struct {
	m     map[string]Hook
	names []string // declaration order
}

// Lookup returns the Hook registered under name and whether the name was found.
func (r *MapRegistry) Lookup(name string) (Hook, bool) {
	h, ok := r.m[name]
	return h, ok
}

// Names returns instance names in declaration order.
func (r *MapRegistry) Names() []string {
	out := make([]string, len(r.names))
	copy(out, r.names)
	return out
}

// NewRegistry returns a Registry populated with the given Hooks in the order
// supplied; that order is the Chain walk order. NewRegistry returns an error
// if any Hook is nil, if two Hooks share the same Name(), or if any Name() is
// the empty string.
func NewRegistry(hooks []Hook) (*MapRegistry, error) {
	m := make(map[string]Hook, len(hooks))
	names := make([]string, 0, len(hooks))
	for i, h := range hooks {
		if h == nil {
			return nil, fmt.Errorf("hook: hooks[%d] is nil", i)
		}
		name := h.Name()
		if name == "" {
			return nil, errors.New("hook: Hook with empty Name()")
		}
		if _, dup := m[name]; dup {
			return nil, fmt.Errorf("hook: duplicate instance name %q", name)
		}
		m[name] = h
		names = append(names, name)
	}
	return &MapRegistry{m: m, names: names}, nil
}
