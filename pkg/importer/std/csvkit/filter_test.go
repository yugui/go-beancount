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
		t.Errorf("ExcludeMatching.Skip(Type=%q) = false, want true", "Total")
	}

	row["Type"] = "Purchase"
	if f.Skip([]string{"Purchase", "-10"}, get) {
		t.Errorf("ExcludeMatching.Skip(Type=%q) = true, want false", "Purchase")
	}
}

func TestExcludeAnyField(t *testing.T) {
	f := csvkit.ExcludeAnyField(regexp.MustCompile("^※"))

	footnote := []string{"※ reference only", "", ""}
	if !f.Skip(footnote, nil) {
		t.Errorf("ExcludeAnyField.Skip(%v) = false, want true", footnote)
	}
	data := []string{"2024-08-01", "Shop", "-1000"}
	if f.Skip(data, nil) {
		t.Errorf("ExcludeAnyField.Skip(%v) = true, want false", data)
	}
}
