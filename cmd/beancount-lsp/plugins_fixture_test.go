//go:build testhelpers

package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
)

// pluginSupportedGOOS lists operating systems where Go's plugin package works.
var pluginSupportedGOOS = map[string]bool{"linux": true, "freebsd": true, "darwin": true}

// okFixturePath returns the runfile path of the //pkg/ext/goplug/testdata/ok
// plugin, skipping when plugins are unsupported or the fixture is absent
// (e.g. raw `go test`).
func okFixturePath(t *testing.T) string {
	t.Helper()
	if !pluginSupportedGOOS[runtime.GOOS] {
		t.Skipf("Go plugins are not supported on GOOS=%s", runtime.GOOS)
	}
	srcDir := os.Getenv("TEST_SRCDIR")
	workspace := os.Getenv("TEST_WORKSPACE")
	if srcDir == "" || workspace == "" {
		t.Skip("no TEST_SRCDIR/TEST_WORKSPACE (run via bazel test)")
	}
	path := filepath.Join(srcDir, workspace, "pkg", "ext", "goplug", "testdata", "ok", "ok.so")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("ok.so fixture not found at %s: %v", path, err)
	}
	return path
}

// TestLoadPlugins_RegistersPostprocessor verifies that a plugin path handed to
// loadPlugins is loaded and its postprocessor becomes resolvable, so a matching
// plugin directive no longer reports plugin-not-registered.
func TestLoadPlugins_RegistersPostprocessor(t *testing.T) {
	path := okFixturePath(t)
	restore := postproc.ResetForTest()
	defer restore()

	var stderr bytes.Buffer
	logger := log.New(&stderr, "beancount-lsp: ", 0)
	loadPlugins([]string{path}, logger)

	const okName = "github.com/yugui/go-beancount/pkg/ext/goplug/testdata/ok"
	ledger := &ast.Ledger{}
	ledger.InsertAll([]ast.Directive{&ast.Plugin{Name: okName}})
	if err := postproc.Apply(context.Background(), ledger); err != nil {
		t.Fatalf("postproc.Apply = %v, want nil", err)
	}
	if len(ledger.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1 (the plugin sentinel); diags=%v", len(ledger.Diagnostics), ledger.Diagnostics)
	}
	if code := ledger.Diagnostics[0].Code; code != "ok.sentinel" {
		t.Errorf("diagnostic code = %q, want ok.sentinel (plugin from BEANCOUNT_PLUGINS did not register?)", code)
	}
}
