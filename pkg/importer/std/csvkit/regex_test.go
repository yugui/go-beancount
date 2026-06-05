package csvkit_test

import (
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

func TestNamedSubmatches(t *testing.T) {
	re := regexp.MustCompile(`^(?P<payee>\S+)\s+(?P<narration>.*)$`)

	t.Run("match", func(t *testing.T) {
		got, ok := csvkit.NamedSubmatches(re, "AMZN Online purchase")
		if !ok {
			t.Fatal("NamedSubmatches() ok = false, want true")
		}
		want := map[string]string{"payee": "AMZN", "narration": "Online purchase"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("NamedSubmatches() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("no match", func(t *testing.T) {
		if _, ok := csvkit.NamedSubmatches(re, "nospacehere"); ok {
			t.Error("NamedSubmatches() ok = true on non-match, want false")
		}
	})
}
