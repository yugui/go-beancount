// Package main is a beanquery goplug test fixture: a minimal valid plugin
// that exports the required Manifest and InitPlugin symbols and registers a
// trivial BQL query function via pkg/query/env.Register. It proves the
// goplug seam (ARCHITECTURE.md §7.4) end to end: a function registered from
// inside a .so resolves during query compilation in the host.
package main

import (
	"github.com/yugui/go-beancount/pkg/ext/goplug"
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// pluginName is the registry key the plugin reports via Manifest. The test
// hardcodes the same string (package main fixtures cannot be imported), so
// changing this constant also requires updating the test.
const pluginName = "github.com/yugui/go-beancount/cmd/beanquery/testdata/queryfn"

// Manifest declares the plugin metadata required by goplug.Load.
var Manifest = goplug.Manifest{
	APIVersion: goplug.APIVersion,
	Name:       pluginName,
	Version:    "v0.0.0-testdata",
}

// InitPlugin is called by goplug.Load. It registers the niladic scalar
// plugin_answer() -> 42 so the host can resolve it during query compilation.
func InitPlugin() error {
	env.Register(api.Function{
		Name:   "plugin_answer",
		In:     nil,
		Out:    types.Int,
		Flavor: api.ScalarFlavor,
		Scalar: api.Pure(func(_ []types.Value) (types.Value, error) {
			return types.NewInt(42), nil
		}),
	})
	return nil
}

// main is required by buildmode=plugin but never runs.
func main() {}
