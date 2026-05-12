//go:build beancompat_fixtures

package beancompat

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

// TestParseFixtures drives the parse-tier beancompat suite. Every fixture
// runs by default; only entries listed in parseDivergences (local) or
// the fixture's own known_divergences["go-beancount"] (upstream) skip.
func TestParseFixtures(t *testing.T) {
	runFixtures(t, fixturesDir(t, "parse"), parseDivergences,
		func(src string) (Result, error) {
			ledger, err := ast.Load(src)
			if err != nil {
				return Result{}, err
			}
			return SerializeParsed(ledger)
		})
}
