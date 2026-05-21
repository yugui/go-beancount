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

// pluginSupportedGOOS lists operating systems where Go's plugin package works.
var pluginSupportedGOOS = map[string]bool{
	"linux":   true,
	"freebsd": true,
	"darwin":  true,
}

// fixtureDir resolves the absolute path to a testdata subdirectory under
// cmd/beanimport. It skips the test when the Bazel runfiles environment
// variables are absent, naming the required invocation.
func fixtureDir(t *testing.T, name string) string {
	t.Helper()
	srcDir := os.Getenv("TEST_SRCDIR")
	workspace := os.Getenv("TEST_WORKSPACE")
	if srcDir == "" || workspace == "" {
		t.Skipf("beanimport: no TEST_SRCDIR/TEST_WORKSPACE set (run via `bazel test //cmd/beanimport:beanimport_test`)")
	}
	return filepath.Join(srcDir, workspace, "cmd", "beanimport", "testdata", name)
}

// staticImporterPath returns the runfiles path of the pre-built staticimporter
// .so fixture. It skips the test on unsupported platforms or when the file
// is absent (the latter covers platforms that slip past the GOOS guard).
func staticImporterPath(t *testing.T) string {
	t.Helper()
	if !pluginSupportedGOOS[runtime.GOOS] {
		t.Skipf("beanimport: Go plugins are not supported on GOOS=%s", runtime.GOOS)
	}
	srcDir := os.Getenv("TEST_SRCDIR")
	workspace := os.Getenv("TEST_WORKSPACE")
	if srcDir == "" || workspace == "" {
		t.Skipf("beanimport: no TEST_SRCDIR/TEST_WORKSPACE set (run via `bazel test //cmd/beanimport:beanimport_test`)")
	}
	path := filepath.Join(srcDir, workspace, "cmd", "beanimport", "testdata", "staticimporter", "staticimporter.so")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("beanimport: staticimporter.so not found at %s", path)
	}
	return path
}

// TestFixture_MultiInstance verifies that Dispatch picks the first-declared
// [[importer]] instance when multiple shapes' match regexes all fire.
func TestFixture_MultiInstance(t *testing.T) {
	dir := fixtureDir(t, "multi_instance")
	var stdout, stderr bytes.Buffer
	args := []string{
		"-config", filepath.Join(dir, "config.toml"),
		filepath.Join(dir, "statement.csv"),
	}
	code := run(context.Background(), args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, want 0\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Assets:First") {
		t.Errorf("stdout does not contain %q\nstdout: %s", "Assets:First", out)
	}
	if strings.Contains(out, "Assets:Second") {
		t.Errorf("stdout contains %q but second instance must not run\nstdout: %s", "Assets:Second", out)
	}
}

// TestFixture_Plugin verifies the -plugin flag drives the
// goplug.Load -> InitPlugin -> importer.RegisterFactory chain end to end.
func TestFixture_Plugin(t *testing.T) {
	soPath := staticImporterPath(t)
	dir := fixtureDir(t, "plugin")
	var stdout, stderr bytes.Buffer
	args := []string{
		"-config", filepath.Join(dir, "config.toml"),
		"-plugin", soPath,
		filepath.Join(dir, "statement.csv"),
	}
	code := run(context.Background(), args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, want 0\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Assets:Static") {
		t.Errorf("stdout does not contain %q\nstdout: %s", "Assets:Static", out)
	}
	if !strings.Contains(out, "static-fixture") {
		t.Errorf("stdout does not contain %q\nstdout: %s", "static-fixture", out)
	}
}
