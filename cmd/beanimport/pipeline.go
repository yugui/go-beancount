package main

import (
	"fmt"

	"github.com/yugui/go-beancount/pkg/importer/hook"
)

// selectHooks returns the subset of all whose Name() appears in
// names, in the order names lists them. When names is nil or empty
// it returns all unchanged (declaration order). It returns an error
// of the form "unknown hook %q" naming the first unknown entry.
func selectHooks(all []hook.Hook, names []string) ([]hook.Hook, error) {
	if len(names) == 0 {
		return all, nil
	}
	index := make(map[string]hook.Hook, len(all))
	for _, h := range all {
		index[h.Name()] = h
	}
	out := make([]hook.Hook, 0, len(names))
	for _, name := range names {
		h, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("unknown hook %q", name)
		}
		out = append(out, h)
	}
	return out, nil
}
