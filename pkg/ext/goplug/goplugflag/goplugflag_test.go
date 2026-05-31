package goplugflag_test

import (
	"flag"
	"io"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yugui/go-beancount/pkg/ext/goplug/goplugflag"
)

func newFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// TestVarSeedsEnvThenAppendsFlags verifies that Var pre-populates the slice
// with BEANCOUNT_PLUGINS entries and that parsing appends -plugin occurrences
// after them, giving the environment-first ordering LoadAll relies on.
func TestVarSeedsEnvThenAppendsFlags(t *testing.T) {
	sep := string(filepath.ListSeparator)
	t.Setenv(goplugflag.EnvVar, "/env/a.so"+sep+"/env/b.so")

	fs := newFlagSet()
	paths := goplugflag.Var(fs)

	// Seeded from the environment before any parsing.
	if want := []string{"/env/a.so", "/env/b.so"}; !reflect.DeepEqual(*paths, want) {
		t.Fatalf("after Var, *paths = %#v, want %#v", *paths, want)
	}

	if err := fs.Parse([]string{"-plugin", "/flag/c.so", "-plugin", "/flag/d.so"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"/env/a.so", "/env/b.so", "/flag/c.so", "/flag/d.so"}
	if !reflect.DeepEqual(*paths, want) {
		t.Errorf("after Parse, *paths = %#v, want %#v", *paths, want)
	}
}

// TestVarEmptyEnv confirms that an unset (empty) environment variable seeds an
// empty list, and that the -plugin flag does not split on commas (a .so path
// may legitimately contain a comma).
func TestVarEmptyEnv(t *testing.T) {
	t.Setenv(goplugflag.EnvVar, "")

	fs := newFlagSet()
	paths := goplugflag.Var(fs)
	if len(*paths) != 0 {
		t.Fatalf("after Var with empty env, *paths = %#v, want empty", *paths)
	}

	if err := fs.Parse([]string{"-plugin", "/flag/b,c.so"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := []string{"/flag/b,c.so"}; !reflect.DeepEqual(*paths, want) {
		t.Errorf("after Parse, *paths = %#v, want %#v (comma must not split)", *paths, want)
	}
}

// TestVarDropsEmptySegments confirms that empty path-list segments (e.g. a
// trailing or doubled separator) are discarded when seeding from the
// environment.
func TestVarDropsEmptySegments(t *testing.T) {
	sep := string(filepath.ListSeparator)
	t.Setenv(goplugflag.EnvVar, sep+"/env/a.so"+sep+sep+"/env/b.so"+sep)

	fs := newFlagSet()
	paths := goplugflag.Var(fs)
	if want := []string{"/env/a.so", "/env/b.so"}; !reflect.DeepEqual(*paths, want) {
		t.Errorf("after Var, *paths = %#v, want %#v", *paths, want)
	}
}
