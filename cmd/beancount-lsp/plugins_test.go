package main

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadPlugins_BadPathIsNonFatal pins the LSP-specific contract: a plugin
// path that does not load is reported but does not abort startup. The server
// is launched by an editor, so a stale path must not leave the user staring
// at a dead language server.
func TestLoadPlugins_BadPathIsNonFatal(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.so")
	var stderr bytes.Buffer
	logger := log.New(&stderr, "beancount-lsp: ", 0)

	// Must return normally (no os.Exit, no panic).
	loadPlugins([]string{missing}, logger)

	out := stderr.String()
	if !strings.Contains(out, missing) {
		t.Errorf("stderr = %q, want it to name the failing plugin %q", out, missing)
	}
	if !strings.Contains(out, "continuing without") {
		t.Errorf("stderr = %q, want it to note that startup continues", out)
	}
}
