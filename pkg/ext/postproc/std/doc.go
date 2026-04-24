// Package std is the Go port of beancount's standard plugin library
// (upstream: github.com/beancount/beancount, beancount/plugins/*.py).
//
// The package has no runtime code. It exists so callers can activate
// every ported plugin with a single blank import:
//
//	import _ "github.com/yugui/go-beancount/pkg/ext/postproc/std"
//
// After this import, plugin directives that name either the upstream
// Python module path (e.g. `plugin "beancount.plugins.check_commodity"`)
// or the Go import path of the individual subpackage (e.g.
// `plugin "github.com/yugui/go-beancount/pkg/ext/postproc/std/checkcommodity"`)
// resolve through [github.com/yugui/go-beancount/pkg/ext/postproc.Apply].
//
// Programs that only want a subset of the library should blank-import
// the specific subpackages they need instead of this umbrella package.
// Each subpackage's own godoc documents upstream attribution, behavior,
// and any deviations from the Python original.
//
// See PLAN.md Phase 6d for the overall design and the list of plugins
// slated for porting.
package std

import (
	_ "github.com/yugui/go-beancount/pkg/ext/postproc/std/checkclosing"
	_ "github.com/yugui/go-beancount/pkg/ext/postproc/std/checkcommodity"
	_ "github.com/yugui/go-beancount/pkg/ext/postproc/std/checkdrained"
)
