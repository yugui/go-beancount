package beancompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/runfiles"
)

// adapterName is the identifier this package reports as in beancompat's
// per-adapter divergence book-keeping. A fixture's known_divergences map
// keyed by this name causes the corresponding subtest to skip with the
// divergence reason, independent of the allowlist.
const adapterName = "go-beancount"

// fixturesDir resolves the runfiles location of an upstream beancompat
// fixture directory (tier is "parse" or "check"). The upstream archive is
// fetched as the @beancompat repo, so the apparent runfile path is
// "beancompat/fixtures/<tier>"; rules_go's runfiles library maps that to
// the canonical bzlmod path. A resolution failure is fatal — the tests
// cannot run without the fixtures.
func fixturesDir(t *testing.T, tier string) string {
	t.Helper()
	dir, err := runfiles.Rlocation("beancompat/fixtures/" + tier)
	if err != nil {
		t.Fatalf("resolve beancompat fixtures runfile for tier %q: %v", tier, err)
	}
	return dir
}

// loadFixture reads p and unmarshals it into a Fixture. Failures are
// fatal: a malformed fixture file is a defect in the upstream pin (or in
// this package's schema), not a behavioral divergence to be tolerated.
func loadFixture(t *testing.T, p string) Fixture {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read fixture %q: %v", p, err)
	}
	var fx Fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatalf("unmarshal fixture %q: %v", p, err)
	}
	return fx
}

// runFixtures drives one tier of the beancompat suite. It iterates every
// fixture file in dir as a subtest, applies a two-tier gating policy
// (known_divergences first, then allowlist), and invokes serialize +
// Match only for explicitly enabled fixtures. After the loop it checks
// that every allowlist entry corresponds to an actual fixture file, so
// stale entries surface as test failures rather than silently passing.
//
// The serialize callback is the tier-specific bridge from beancount
// source text to an Result — parse-tier and check-tier tests differ
// only in which Load entry point and which SerializeXxx helper they call.
func runFixtures(
	t *testing.T,
	dir string,
	allowlist map[string]string,
	serialize func(src string) (Result, error),
) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures under %q: %v", dir, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no fixtures found under %q", dir)
	}
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), ".json")
		seen[name] = true
		t.Run(name, func(t *testing.T) {
			fx := loadFixture(t, p)
			if reason, ok := fx.KnownDivergences[adapterName]; ok {
				t.Skipf("known divergence: %s", reason)
			}
			if _, ok := allowlist[name]; !ok {
				t.Skipf("not in allowlist; add %q to enabledParseFixtures or enabledCheckFixtures to enable", name)
			}
			actual, err := serialize(fx.Source)
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			if diags := Match(fx.Expected, actual); len(diags) > 0 {
				t.Fatal(formatFailure(fx.Expected, actual, diags))
			}
		})
	}
	for name := range allowlist {
		if !seen[name] {
			t.Errorf("allowlist entry %q has no matching fixture in %q", name, dir)
		}
	}
}
