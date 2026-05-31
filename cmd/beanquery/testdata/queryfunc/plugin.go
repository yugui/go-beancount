// This file is the .so entry point. It must be compiled with
// -buildmode=plugin and is loaded by cmd/beanquery's -plugin flag in
// the integration tests. See doc.go for the fixture's role.

package main

import (
	"github.com/yugui/go-beancount/pkg/ext/goplug"
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// funcName is the BQL function the fixture registers. It is chosen to
// not collide with any built-in in pkg/query/env/std or sprout; the
// integration test hardcodes the same name, so changing it also
// requires updating the test.
const funcName = "fixture_marker"

// Manifest is exported so goplug.Load can read it via plugin.Lookup.
var Manifest = goplug.Manifest{
	APIVersion: goplug.APIVersion,
	Name:       "github.com/yugui/go-beancount/cmd/beanquery/testdata/queryfunc",
	Version:    "v0.0.0-fixture",
}

// InitPlugin is the goplug entry point. Called once after the Manifest
// checks pass; a non-nil return aborts the load. It registers a single
// context-free scalar overload through the same env.Register the
// built-in std library uses, proving the §7.4 dynamic-load seam.
func InitPlugin() error {
	env.Register(api.Function{
		Name:   funcName,
		In:     []types.Type{types.String},
		Out:    types.String,
		Flavor: api.ScalarFlavor,
		Scalar: api.Pure(func(args []types.Value) (types.Value, error) {
			if args[0].IsNull() {
				return types.Null(types.String), nil
			}
			s, _ := types.AsString(args[0])
			return types.NewString("FIXTURE-" + s), nil
		}),
	})
	return nil
}

// main is required for buildmode=plugin but is never invoked.
func main() {}
