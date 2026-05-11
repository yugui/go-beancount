//go:build beancompat_fixtures

package beancompat

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

// TestParseFixtures drives the parse-tier beancompat suite. Every fixture
// is enumerated as a subtest, but with an empty enabledParseFixtures
// (Step 2) every subtest reports SKIP. This verifies the fixture
// discovery and gating plumbing in isolation, before any serializer
// implementation can claim behavioral coverage.
func TestParseFixtures(t *testing.T) {
	runFixtures(t, fixturesDir(t, "parse"), enabledParseFixtures,
		func(src string) (Result, error) {
			ledger, err := ast.Load(src)
			if err != nil {
				return Result{}, err
			}
			return SerializeParsed(ledger)
		})
}
