//go:build testhelpers

package goplug_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/goplug"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
)

// pluginSupportedGOOS lists the operating systems where Go's plugin
// package is documented to work. The tests skip on anything else.
var pluginSupportedGOOS = map[string]bool{
	"linux":   true,
	"freebsd": true,
	"darwin":  true,
}

// skipIfPluginsUnsupported skips the caller when the current GOOS does
// not support Go plugins at all, so tests don't report failures for an
// environment limitation. Shared by the unit test and pluginPath.
func skipIfPluginsUnsupported(t *testing.T) {
	t.Helper()
	if !pluginSupportedGOOS[runtime.GOOS] {
		t.Skipf("goplug: Go plugins are not supported on GOOS=%s", runtime.GOOS)
	}
}

// pluginPath returns the runfile path of a pre-built test plugin .so.
// The plugin is produced by //pkg/ext/goplug/testdata/<name>:<name>
// in Bazel and materialized into the test binary's runfiles.
//
// If the runfile isn't found — which happens outside Bazel, e.g. raw
// `go test` — the test skips with an explanation.
func pluginPath(t *testing.T, name string) string {
	t.Helper()
	skipIfPluginsUnsupported(t)

	srcDir := os.Getenv("TEST_SRCDIR")
	workspace := os.Getenv("TEST_WORKSPACE")
	if srcDir == "" || workspace == "" {
		t.Skipf("goplug: no TEST_SRCDIR/TEST_WORKSPACE set (run via `bazel test //pkg/ext/goplug:goplug_test`)")
	}

	path := filepath.Join(srcDir, workspace, "pkg", "ext", "goplug", "testdata", name, name+".so")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("goplug: fixture %s.so not found at %s: %v", name, path, err)
	}
	return path
}

func TestLoad_OpenFailure(t *testing.T) {
	skipIfPluginsUnsupported(t)
	restore := postproc.ResetForTest()
	defer restore()

	const path = "/nonexistent/path/does-not-exist.so"
	err := goplug.Load(path)
	if err == nil {
		t.Fatalf("goplug.Load(%q) = nil, want error", path)
	}
	if !errors.Is(err, goplug.ErrOpen) {
		t.Errorf("goplug.Load(%q) = %v, want an error wrapping goplug.ErrOpen", path, err)
	}
	assertRegistryEmpty(t)
}

func TestLoad_HappyPath(t *testing.T) {
	path := pluginPath(t, "ok")
	restore := postproc.ResetForTest()
	defer restore()

	if err := goplug.Load(path); err != nil {
		t.Fatalf("goplug.Load(%q) = %v, want nil", path, err)
	}

	// Assert the plugin registered under the expected name by dispatching
	// through postproc.Apply. The runner appends plugin diagnostics to
	// the ledger; if the plugin's postproc.Register did not mutate the
	// host's registry, the ledger would carry plugin-not-registered
	// instead of the plugin's sentinel.
	const pluginName = "github.com/yugui/go-beancount/pkg/ext/goplug/testdata/ok"
	ledger := &ast.Ledger{}
	ledger.InsertAll([]ast.Directive{&ast.Plugin{Name: pluginName}})

	if err := postproc.Apply(context.Background(), ledger); err != nil {
		t.Fatalf("postproc.Apply after goplug.Load(%q) = %v, want nil", path, err)
	}
	if len(ledger.Diagnostics) != 1 {
		t.Fatalf("postproc.Apply after goplug.Load(%q) wrote %d diagnostics, want 1 (the plugin's sentinel); diags = %v", path, len(ledger.Diagnostics), ledger.Diagnostics)
	}
	if ledger.Diagnostics[0].Code != "ok.sentinel" {
		t.Errorf("postproc.Apply after goplug.Load(%q) diagnostic code = %q, want %q (plugin did not run with the host's registry?)",
			path, ledger.Diagnostics[0].Code, "ok.sentinel")
	}
}

// TestLoad_Rejections covers every rejection path where the loader
// refuses a plugin before InitPlugin can touch the registry. Each
// subtest loads the corresponding pre-built testdata .so via
// goplug.Load and asserts (a) the returned error wraps the expected
// sentinel and (b) the registry is still empty. A note is included
// for the InitPluginFailed case explaining why we cannot check the
// plugin's own sentinel via errors.Is from this test binary.
//
// Subtests are intentionally sequential (no t.Parallel): they share
// the global postproc.registry via ResetForTest, which is not
// goroutine-safe. Running them concurrently would race on the swap
// and restore closures.
func TestLoad_Rejections(t *testing.T) {
	tests := []struct {
		name         string
		fixture      string
		wantSentinel error
	}{
		{name: "manifest missing", fixture: "nomanifest", wantSentinel: goplug.ErrManifestMissing},
		{name: "manifest wrong type", fixture: "badmanifest", wantSentinel: goplug.ErrManifestWrongType},
		{name: "api version mismatch", fixture: "badapiversion", wantSentinel: goplug.ErrAPIVersionMismatch},
		{name: "manifest name empty", fixture: "emptyname", wantSentinel: goplug.ErrManifestNameEmpty},
		{name: "init plugin missing", fixture: "noinit", wantSentinel: goplug.ErrInitPluginMissing},
		{name: "init plugin wrong signature", fixture: "badinitsig", wantSentinel: goplug.ErrInitPluginWrongType},
		// The InitPluginFailed case also attaches the plugin's own
		// error as a second %w operand so callers with access to the
		// plugin's sentinel can errors.Is against it. We don't check
		// that here because importing the fixture package would pull
		// the testdata directory into the test binary's production
		// graph and defeat the point of building it as a separate
		// .so.
		{name: "init plugin returned error", fixture: "initerrors", wantSentinel: goplug.ErrInitPluginFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := pluginPath(t, tc.fixture)
			restore := postproc.ResetForTest()
			defer restore()

			err := goplug.Load(path)
			if err == nil {
				t.Fatalf("goplug.Load(%q) = nil, want error", path)
			}
			if !errors.Is(err, tc.wantSentinel) {
				t.Errorf("goplug.Load(%q) = %v, want an error wrapping %v", path, err, tc.wantSentinel)
			}
			assertRegistryEmpty(t)
		})
	}
}

// assertRegistryEmpty verifies that no plugin is discoverable by
// checking that an unknown directive produces plugin-not-registered.
// We can't inspect the registry map directly from an external test
// package without broadening the public surface, so we probe through
// postproc.Apply and inspect the resulting ledger.Diagnostics.
func assertRegistryEmpty(t *testing.T) {
	t.Helper()
	const probeName = "goplug.test/probe"
	ledger := &ast.Ledger{}
	ledger.InsertAll([]ast.Directive{&ast.Plugin{Name: probeName}})
	if err := postproc.Apply(context.Background(), ledger); err != nil {
		t.Errorf("postproc.Apply(plugin %q) after failed goplug.Load: err = %v, want nil", probeName, err)
		return
	}
	if len(ledger.Diagnostics) != 1 {
		t.Errorf("postproc.Apply(plugin %q) after failed goplug.Load: wrote %d diagnostics, want 1 (plugin-not-registered); diags=%v", probeName, len(ledger.Diagnostics), ledger.Diagnostics)
		return
	}
	if ledger.Diagnostics[0].Code != "plugin-not-registered" {
		t.Errorf("postproc.Apply(plugin %q) after failed goplug.Load: diagnostic code = %q, want %q", probeName, ledger.Diagnostics[0].Code, "plugin-not-registered")
	}
}
