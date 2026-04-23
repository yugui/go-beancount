//go:build testhelpers

// This file exposes ResetForTest to test binaries in other packages
// (most notably pkg/ext/goplug). A blank line separates this
// design-rationale comment from the package clause so it is NOT
// treated as the package doc comment — the real doc lives in doc.go.
//
// Why not export_test.go?
//
// The idiomatic Go pattern for exposing an unexported package
// internal to tests is a *_test.go file (optionally export_test.go)
// inside the same package. Such files are only compiled for the
// package's own test binary — the _test.go suffix files of package
// postproc are not visible to test binaries of other packages such
// as pkg/ext/goplug.
//
// The registry lives inside package postproc and we need a handle
// reachable from pkg/ext/goplug's test binary. A non-_test.go
// file in package postproc is therefore the only option. The
// //go:build testhelpers constraint keeps the file out of any build
// that does not explicitly request the tag, matching the
// production-exclusion guarantee the _test.go suffix would have
// given. Consumers of //pkg/ext/postproc without gotags=["testhelpers"]
// do not see ResetForTest; a direct reference without the tag fails
// to compile.

package postproc

import "github.com/yugui/go-beancount/pkg/ext/postproc/api"

// ResetForTest replaces the global registry with an empty map and
// returns a closure that restores the previous contents when called.
// It is intended for tests in other packages (notably
// pkg/ext/goplug) that need to start from a clean registry.
//
// ResetForTest is not safe for concurrent use: call it from a single
// goroutine, and do not launch t.Parallel subtests against a
// registry in mid-swap. Its lifecycle matches the enclosing test's,
// not the process's.
func ResetForTest() func() {
	old := registry
	registry = map[string]api.Plugin{}
	return func() { registry = old }
}
