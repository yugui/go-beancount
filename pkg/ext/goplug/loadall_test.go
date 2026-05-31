package goplug_test

import (
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ext/goplug"
)

// TestLoadAll_NoPaths confirms the empty input is a no-op success.
func TestLoadAll_NoPaths(t *testing.T) {
	if err := goplug.LoadAll(nil); err != nil {
		t.Errorf("LoadAll(nil) = %v, want nil", err)
	}
}

// TestLoadAll_ReportsEveryFailure verifies LoadAll attempts every path and
// names each failing path in the returned error. Nonexistent paths fail in
// Load on every platform without a real .so fixture, and without the
// duplicate-load panic since failed opens are not cached.
func TestLoadAll_ReportsEveryFailure(t *testing.T) {
	paths := []string{"/nonexistent/one.so", "/nonexistent/two.so"}
	err := goplug.LoadAll(paths)
	if err == nil {
		t.Fatalf("LoadAll(%v) = nil, want error", paths)
	}
	for _, p := range paths {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("LoadAll error %q does not name %q (path not attempted?)", err, p)
		}
	}
}

// TestLoadAll_DeduplicatesPaths verifies a path repeated in the input is loaded
// only once. Each Load attempt contributes exactly one `loading "<path>"`
// marker to the error, so the marker count is the attempt count.
func TestLoadAll_DeduplicatesPaths(t *testing.T) {
	const p = "/nonexistent/dup.so"
	err := goplug.LoadAll([]string{p, p})
	if err == nil {
		t.Fatalf("LoadAll([dup, dup]) = nil, want error")
	}
	marker := `loading "` + p + `"`
	if n := strings.Count(err.Error(), marker); n != 1 {
		t.Errorf("Load attempted %d times, want 1 (deduplicated); err=%v", n, err)
	}
}
