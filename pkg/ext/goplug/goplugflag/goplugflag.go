package goplugflag

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
)

// EnvVar names the environment variable that lists goplug plugin .so paths to
// load at startup. Its value is split on the OS path-list separator (like PATH
// and GOPATH); a separator is used rather than a comma because plugin paths
// may contain commas.
const EnvVar = "BEANCOUNT_PLUGINS"

// stringSlice accumulates flag occurrences as literal strings, preserving
// commas: .so paths may contain commas, so the flag repeats rather than
// comma-splitting.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, string(filepath.ListSeparator)) }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

// Var registers the repeatable -plugin flag on fs and returns a pointer to the
// accumulated plugin paths. The returned slice is pre-populated with the paths
// from BEANCOUNT_PLUGINS, so once fs is parsed it holds the environment paths
// followed by the -plugin flag paths in that order. Pass the result to
// [github.com/yugui/go-beancount/pkg/ext/goplug.LoadAll].
//
// fs is a parameter so tests can drive a throwaway FlagSet; production callers
// pass flag.CommandLine (or a command's own FlagSet).
func Var(fs *flag.FlagSet) *[]string {
	s := stringSlice(splitPaths())
	fs.Var(&s, "plugin", "load goplug .so plugin from PATH (repeatable)")
	return (*[]string)(&s)
}

func splitPaths() []string {
	var out []string
	for _, p := range filepath.SplitList(os.Getenv(EnvVar)) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
