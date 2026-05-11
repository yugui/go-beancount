//go:build beancompat_fixtures

package beancompat

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/loader"
)

// TestCheckFixtures drives the check-tier beancompat suite. It mirrors
// TestParseFixtures but routes the source through the loader pipeline
// (parse + plugins + pad/balance/validations) so the recorded Result
// covers post-validation state. With enabledCheckFixtures empty in
// Step 2, every subtest reports SKIP.
func TestCheckFixtures(t *testing.T) {
	runFixtures(t, fixturesDir(t, "check"), enabledCheckFixtures,
		func(src string) (Result, error) {
			ledger, err := loader.Load(t.Context(), src)
			if err != nil {
				return Result{}, err
			}
			return SerializeChecked(ledger)
		})
}
