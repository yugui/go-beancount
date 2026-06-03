package csvkit_test

import (
	"regexp"
	"testing"

	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

func TestExcludeMatching(t *testing.T) {
	f := csvkit.ExcludeMatching("Type", regexp.MustCompile("^Total"))
	row := map[string]string{"Type": "Total", "Amount": "-3000"}
	get := func(c string) string { return row[c] }

	if !f.Skip([]string{"Total", "-3000"}, get) {
		t.Error("Skip = false on matching column, want true")
	}

	row["Type"] = "Purchase"
	if f.Skip([]string{"Purchase", "-10"}, get) {
		t.Error("Skip = true on non-matching column, want false")
	}
}

func TestExcludeAnyField(t *testing.T) {
	f := csvkit.ExcludeAnyField(regexp.MustCompile("^※"))

	if !f.Skip([]string{"※ reference only", "", ""}, nil) {
		t.Error("Skip = false on footnote row, want true")
	}
	if f.Skip([]string{"2024-08-01", "Shop", "-1000"}, nil) {
		t.Error("Skip = true on data row, want false")
	}
}
