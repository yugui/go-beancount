package importer

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Kind registry — populated at init time or via goplug InitPlugin callbacks.

var (
	kindMu sync.RWMutex
	kinds  = map[string]Factory{}
)

// RegisterFactory installs f under the given kind in the package-global
// kind registry. It panics if a Factory has already been registered
// under the same kind, mirroring the pattern in pkg/quote.Register.
// Intended to be called from an init() function (in-tree kinds) or
// from a goplug InitPlugin callback (plugin kinds). Safe for
// concurrent use; reads (New, KindNames) MAY run concurrently with
// RegisterFactory, though in practice all registrations land before
// reads begin.
func RegisterFactory(kind string, f Factory) {
	kindMu.Lock()
	defer kindMu.Unlock()
	if _, exists := kinds[kind]; exists {
		panic(fmt.Sprintf("importer: duplicate Factory registration for %q", kind))
	}
	kinds[kind] = f
}

// New constructs a configured Importer of the given kind. It is the
// one-shot form of lookupFactory + Factory.New, and is the recommended
// way for CLIs and tests to build an Importer instance. Returns an error
// if kind is not registered or the factory returns an error.
func New(kind, name string, decode func(dest any) error) (Importer, error) {
	f, ok := lookupFactory(kind)
	if !ok {
		return nil, fmt.Errorf("importer: unknown kind %q", kind)
	}
	return f.New(name, decode)
}

func lookupFactory(kind string) (Factory, bool) {
	kindMu.RLock()
	defer kindMu.RUnlock()
	f, ok := kinds[kind]
	return f, ok
}

// KindNames returns the registered kinds sorted in ascending order so
// that diagnostics and tests have deterministic output.
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

// Instance registry — built per run from configured Importer values.

// Registry is the per-run lookup of fully-configured Importer
// instances. The CLI builds one Registry per beanimport invocation
// from the [[importer]] entries in TOML and hands it to Dispatch/Apply.
//
// Names returns instance names in the order Dispatch must walk them.
// In ABI v1 this is declaration order (the order the CLI handed the
// instances to the constructor); implementations MUST preserve a
// stable, deterministic order across repeated calls on the same
// Registry value.
//
// All Registry methods are safe for concurrent use. A Registry's
// contents are immutable after construction.
type Registry interface {
	Lookup(name string) (Importer, bool)
	Names() []string
}

// MapRegistry is the default in-memory Registry implementation. Build
// one with NewRegistry; the zero value has a nil map and must not be used.
type MapRegistry struct {
	m     map[string]Importer
	names []string // declaration order
}

// Lookup returns the Importer registered under name and whether the name was found.
func (r *MapRegistry) Lookup(name string) (Importer, bool) {
	imp, ok := r.m[name]
	return imp, ok
}

// Names returns instance names in declaration order.
func (r *MapRegistry) Names() []string {
	out := make([]string, len(r.names))
	copy(out, r.names)
	return out
}

// NewRegistry returns a Registry populated with the given Importers in
// the order supplied; that order is the Dispatch walk order. NewRegistry
// returns an error if any Importer is nil, if two Importers share the
// same Name(), or if any Name() is the empty string.
func NewRegistry(imps []Importer) (*MapRegistry, error) {
	m := make(map[string]Importer, len(imps))
	names := make([]string, 0, len(imps))
	for i, imp := range imps {
		if imp == nil {
			return nil, fmt.Errorf("importer: imps[%d] is nil", i)
		}
		name := imp.Name()
		if name == "" {
			return nil, errors.New("importer: Importer with empty Name()")
		}
		if _, dup := m[name]; dup {
			return nil, fmt.Errorf("importer: duplicate instance name %q", name)
		}
		m[name] = imp
		names = append(names, name)
	}
	return &MapRegistry{m: m, names: names}, nil
}
