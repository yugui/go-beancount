package ast_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestLoad_OverlayPriorityOverDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(path, []byte("2024-01-01 open Assets:Bank USD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overlay := map[string][]byte{
		path: []byte("2024-01-01 open Assets:Bank EUR\n"),
	}
	ledger, err := ast.LoadFile(path, ast.WithOverlay(overlay))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("LoadFile() with WithOverlay: unexpected error diagnostic: %s", d.Message)
		}
		if d.Code == "overlay-non-absolute-key" {
			t.Errorf("LoadFile() with WithOverlay: unexpected overlay-non-absolute-key warning for absolute key")
		}
	}
	var foundEUR bool
	for _, d := range ledger.All() {
		o, ok := d.(*ast.Open)
		if !ok {
			continue
		}
		if len(o.Currencies) == 1 && o.Currencies[0] == "USD" {
			t.Errorf("LoadFile() with WithOverlay: disk content read despite overlay: got USD, want EUR")
		}
		if len(o.Currencies) == 1 && o.Currencies[0] == "EUR" {
			foundEUR = true
		}
	}
	if !foundEUR {
		t.Errorf("LoadFile() with WithOverlay: overlay-supplied EUR Open not found; disk was not short-circuited")
	}
}

func TestLoad_OverlayMissingDiskFallback(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	leaf := filepath.Join(dir, "leaf.beancount")
	if err := os.WriteFile(root, []byte("include \"leaf.beancount\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf, []byte("2024-01-01 open Assets:DiskLeaf USD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Overlay key points to an unrelated path that the include never resolves to.
	unrelated := filepath.Join(dir, "unrelated.beancount")
	overlay := map[string][]byte{
		unrelated: []byte("2024-01-01 open Assets:Unrelated USD\n"),
	}
	ledger, err := ast.LoadFile(root, ast.WithOverlay(overlay))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("LoadFile() with unrelated overlay: unexpected error diagnostic: %s", d.Message)
		}
	}
	var found bool
	for _, d := range ledger.All() {
		if o, ok := d.(*ast.Open); ok && o.Account == "Assets:DiskLeaf" {
			found = true
		}
	}
	if !found {
		t.Errorf("LoadFile() with unrelated overlay: disk-backed include not loaded when overlay key is unrelated")
	}
}
