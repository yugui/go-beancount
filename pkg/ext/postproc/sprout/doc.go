// Package sprout is the Go port of beansprout's plugin library
// (upstream: github.com/yugui/beansprout, beansprout/plugins/*.py).
//
// The package has no runtime code. It exists so callers can activate
// every ported plugin with a single blank import:
//
//	import _ "github.com/yugui/go-beancount/pkg/ext/postproc/sprout"
//
// After this import, plugin directives that name either the upstream
// Python module path (e.g. `plugin "beansprout.plugins.check_metadata"`)
// or the Go import path of the individual subpackage (e.g.
// `plugin "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/checkmetadata"`)
// resolve through [github.com/yugui/go-beancount/pkg/ext/postproc.Apply].
//
// Programs that only want a subset of the library should blank-import
// the specific subpackages they need instead of this umbrella package.
// Each subpackage's own godoc documents upstream attribution, behavior,
// and any deviations from the Python original.
package sprout
