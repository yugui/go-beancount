package format_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/format"
)

var update = flag.Bool("update", false, "update golden files")

func TestGoldenDefault(t *testing.T) {
	testdataDir := "testdata"
	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		t.Fatalf("reading testdata dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip golden files and non-beancount files.
		if strings.HasSuffix(name, ".golden.beancount") || !strings.HasSuffix(name, ".beancount") {
			continue
		}
		base := strings.TrimSuffix(name, ".beancount")
		// Skip files that have a dedicated option test (e.g., comma).
		if base == "comma" {
			continue
		}
		goldenFile := filepath.Join(testdataDir, base+".golden.beancount")

		t.Run(base, func(t *testing.T) {
			got, err := format.FormatFile(filepath.Join(testdataDir, name))
			if err != nil {
				t.Fatalf("formatting input: %v", err)
			}

			if *update {
				if err := os.WriteFile(goldenFile, []byte(got), 0o644); err != nil {
					t.Fatalf("updating golden file: %v", err)
				}
				return
			}

			want, err := os.ReadFile(goldenFile)
			if err != nil {
				t.Fatalf("reading golden file %s: %v (run with -update to create)", goldenFile, err)
			}
			if got != string(want) {
				t.Errorf("Format(%s) differs from golden file %s\n%s", name, goldenFile, diffStrings(got, string(want), "got", "want"))
			}
		})
	}
}

func TestGoldenCommaGrouping(t *testing.T) {
	got, err := format.FormatFile("testdata/comma.beancount", format.WithCommaGrouping(true))
	if err != nil {
		t.Fatalf("formatting input: %v", err)
	}

	goldenFile := "testdata/comma.golden.beancount"
	if *update {
		if err := os.WriteFile(goldenFile, []byte(got), 0o644); err != nil {
			t.Fatalf("updating golden file: %v", err)
		}
		return
	}

	want, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf("reading golden file: %v (run with -update to create)", err)
	}
	if got != string(want) {
		t.Errorf("Format(comma.beancount, WithCommaGrouping) differs from golden\n%s", diffStrings(got, string(want), "got", "want"))
	}
}
