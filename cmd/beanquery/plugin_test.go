//go:build testhelpers

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// pluginSupportedGOOS lists the operating systems where Go's plugin package
// is documented to work. The test skips on anything else.
var pluginSupportedGOOS = map[string]bool{
	"linux":   true,
	"freebsd": true,
	"darwin":  true,
}

// pluginPath returns the runfile path of the pre-built queryfn test plugin
// .so, produced by //cmd/beanquery/testdata/queryfn and materialized into
// the test binary's runfiles. It skips when plugins are unsupported on the
// host or when the runfile is absent (e.g. raw `go test` outside Bazel).
func pluginPath(t *testing.T) string {
	t.Helper()
	if !pluginSupportedGOOS[runtime.GOOS] {
		t.Skipf("beanquery: Go plugins are not supported on GOOS=%s", runtime.GOOS)
	}

	srcDir := os.Getenv("TEST_SRCDIR")
	workspace := os.Getenv("TEST_WORKSPACE")
	if srcDir == "" || workspace == "" {
		t.Skipf("beanquery: no TEST_SRCDIR/TEST_WORKSPACE set (run via `bazel test //cmd/beanquery:beanquery_plugin_test`)")
	}

	path := filepath.Join(srcDir, workspace, "cmd", "beanquery", "testdata", "queryfn", "queryfn.so")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("beanquery: fixture queryfn.so not found at %s: %v", path, err)
	}
	return path
}

// TestRun_QueryFunctionPlugin verifies the goplug seam (ARCHITECTURE.md
// §7.4) end to end: a query function registered from inside a .so via
// -plugin resolves during compilation, so SELECT plugin_answer() runs and
// renders 42.
func TestRun_QueryFunctionPlugin(t *testing.T) {
	so := pluginPath(t)
	ledger := writeLedger(t, sampleLedger)
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{
		"-plugin", so,
		ledger,
		"SELECT plugin_answer() AS answer",
	}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("run(plugin query) = %d, want 0; stderr: %q", got, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"answer", "42"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q; got:\n%s", want, out)
		}
	}
}
