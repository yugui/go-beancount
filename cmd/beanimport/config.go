package main

import (
	"fmt"
	"io"

	"github.com/BurntSushi/toml"

	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/importer/hook"
)

type rawDoc struct {
	Importer []toml.Primitive `toml:"importer"`
	Hook     []toml.Primitive `toml:"hook"`
}

type entryShim struct {
	Kind string `toml:"kind"`
	Name string `toml:"name"`
}

// loadConfig reads one flat-schema TOML document from r and returns the
// [[importer]] and [[hook]] entries in declaration order, each already
// constructed via importer.New / hook.New. path is the display name used
// in error messages.
//
// Errors are wrapped as "beanimport: config %s: <cause>" with one of
// the following cause forms:
//
//   - "decode: <toml error>"
//   - "unknown top-level key %q"
//   - "[[importer]] #%d: missing %q"      — kind or name absent
//   - "[[hook]] #%d: missing %q"
//   - "[[importer]] #%d (%q): %v"         — factory error verbatim
//   - "[[hook]] #%d (%q): %v"
//   - "unknown body key %q"
//   - "no [[importer]] entries"
func loadConfig(r io.Reader, path string) (
	importers []importer.Importer,
	hooks []hook.Hook,
	err error,
) {
	wrapMsg := func(cause string) error {
		return fmt.Errorf("beanimport: config %s: %s", path, cause)
	}
	wrapErr := func(prefix string, cause error) error {
		return fmt.Errorf("beanimport: config %s: %s: %w", path, prefix, cause)
	}

	var raw rawDoc
	dec := toml.NewDecoder(r)
	meta, decErr := dec.Decode(&raw)
	if decErr != nil {
		return nil, nil, wrapErr("decode", decErr)
	}

	for _, k := range meta.Undecoded() {
		if len(k) == 1 && k[0] != "importer" && k[0] != "hook" {
			return nil, nil, wrapMsg(fmt.Sprintf("unknown top-level key %q", k[0]))
		}
	}

	if len(raw.Importer) == 0 {
		return nil, nil, wrapMsg("no [[importer]] entries")
	}

	for i, prim := range raw.Importer {
		var shim entryShim
		if err := meta.PrimitiveDecode(prim, &shim); err != nil {
			return nil, nil, wrapErr("decode", err)
		}
		if shim.Kind == "" {
			return nil, nil, wrapMsg(fmt.Sprintf("[[importer]] #%d: missing %q", i+1, "kind"))
		}
		if shim.Name == "" {
			return nil, nil, wrapMsg(fmt.Sprintf("[[importer]] #%d: missing %q", i+1, "name"))
		}

		imp, factErr := importer.New(shim.Kind, shim.Name, func(dest any) error {
			return meta.PrimitiveDecode(prim, dest)
		})
		if factErr != nil {
			return nil, nil, wrapMsg(fmt.Sprintf("[[importer]] #%d (%q): %v", i+1, shim.Name, factErr))
		}
		importers = append(importers, imp)
	}

	for i, prim := range raw.Hook {
		var shim entryShim
		if err := meta.PrimitiveDecode(prim, &shim); err != nil {
			return nil, nil, wrapErr("decode", err)
		}
		if shim.Kind == "" {
			return nil, nil, wrapMsg(fmt.Sprintf("[[hook]] #%d: missing %q", i+1, "kind"))
		}
		if shim.Name == "" {
			return nil, nil, wrapMsg(fmt.Sprintf("[[hook]] #%d: missing %q", i+1, "name"))
		}

		h, factErr := hook.New(shim.Kind, shim.Name, func(dest any) error {
			return meta.PrimitiveDecode(prim, dest)
		})
		if factErr != nil {
			return nil, nil, wrapMsg(fmt.Sprintf("[[hook]] #%d (%q): %v", i+1, shim.Name, factErr))
		}
		hooks = append(hooks, h)
	}

	for _, k := range meta.Undecoded() {
		if len(k) >= 2 && (k[0] == "importer" || k[0] == "hook") {
			leaf := k[len(k)-1]
			if leaf != "kind" && leaf != "name" {
				return nil, nil, wrapMsg(fmt.Sprintf("unknown body key %q", leaf))
			}
		}
	}

	return importers, hooks, nil
}
