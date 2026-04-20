// Package main is a goplug test fixture: a minimal valid plugin that
// exports the required Manifest and InitPlugin symbols and registers
// a trivial api.PluginFunc.
package main

import (
	"context"

	"github.com/yugui/go-beancount/pkg/postproc"
	"github.com/yugui/go-beancount/pkg/postproc/api"
	"github.com/yugui/go-beancount/pkg/postproc/goplug"
)

// pluginName is the registry key the plugin uses. The test hardcodes
// the same string (package main fixtures cannot be imported), so
// changing this constant also requires updating the test.
const pluginName = "github.com/yugui/go-beancount/pkg/postproc/goplug/testdata/ok"

// sentinel is the diagnostic the plugin emits when invoked. The test
// looks for its Code ("ok.sentinel") in the api.Error slice returned
// by postproc.Apply to confirm the plugin registered from inside the
// .so ran through the host's registry.
var sentinel = &api.Error{Code: "ok.sentinel", Message: "ok plugin ran"}

// Manifest declares the plugin metadata required by goplug.Load.
var Manifest = goplug.Manifest{
	APIVersion: goplug.APIVersion,
	Name:       pluginName,
	Version:    "v0.0.0-testdata",
}

// plug is the api.PluginFunc that InitPlugin registers under
// pluginName. It records that it was invoked by emitting sentinel as
// its only diagnostic. Unexported because goplug.Load only looks up
// Manifest and InitPlugin; keeping the other .so symbols unexported
// minimizes the advertised plugin surface.
var plug api.PluginFunc = func(_ context.Context, _ api.Input) (api.Result, error) {
	return api.Result{Errors: []api.Error{*sentinel}}, nil
}

// InitPlugin is called by goplug.Load. It registers plug under
// pluginName via the host's postproc registry.
func InitPlugin() error {
	postproc.Register(pluginName, plug)
	return nil
}

// main is required by buildmode=plugin but never runs.
func main() {}
