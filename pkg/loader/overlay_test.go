package loader_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/loader"
)

func TestLoadFile_OverlayReplacesDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(path, []byte("2024-01-01 open Assets:Bank USD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overlay := map[string][]byte{
		path: []byte("2024-01-01 open Assets:Bank EUR\n"),
	}
	ledger, err := loader.LoadFile(context.Background(), path, loader.WithOverlay(overlay))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("LoadFile() with WithOverlay: unexpected error diagnostic: %s", d.Message)
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
		t.Errorf("LoadFile() with WithOverlay: overlay-supplied EUR Open not found in ledger")
	}
}

func TestLoadFile_OverlayIncludeRelative(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	leaf := filepath.Join(dir, "leaf.beancount")
	if err := os.WriteFile(root, []byte("include \"leaf.beancount\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Disk leaf is empty; overlay provides content.
	if err := os.WriteFile(leaf, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	overlay := map[string][]byte{
		leaf: []byte("2024-01-01 open Assets:Overlay USD\n"),
	}
	ledger, err := loader.LoadFile(context.Background(), root, loader.WithOverlay(overlay))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("LoadFile() with relative include overlay: unexpected error diagnostic: %s", d.Message)
		}
	}
	var found bool
	for _, d := range ledger.All() {
		if o, ok := d.(*ast.Open); ok && o.Account == "Assets:Overlay" {
			found = true
		}
	}
	if !found {
		t.Errorf("LoadFile() with relative include overlay: overlay-supplied Open directive not found")
	}
}

func TestLoadFile_OverlayIncludeAbsolute(t *testing.T) {
	dir := t.TempDir()
	leaf := filepath.Join(dir, "leaf.beancount")
	root := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(root, []byte("include \""+leaf+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// leaf does not exist on disk; overlay provides it.
	overlay := map[string][]byte{
		leaf: []byte("2024-01-01 open Assets:Overlay USD\n"),
	}
	ledger, err := loader.LoadFile(context.Background(), root, loader.WithOverlay(overlay))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("LoadFile() with absolute include overlay: unexpected error diagnostic: %s", d.Message)
		}
	}
	var found bool
	for _, d := range ledger.All() {
		if o, ok := d.(*ast.Open); ok && o.Account == "Assets:Overlay" {
			found = true
		}
	}
	if !found {
		t.Errorf("LoadFile() with absolute include overlay: overlay-supplied Open directive not found")
	}
}

func TestLoadFile_OverlayGlobUnion(t *testing.T) {
	dir := t.TempDir()
	diskLeaf := filepath.Join(dir, "a.beancount")
	overlayLeaf := filepath.Join(dir, "b.beancount")
	root := filepath.Join(dir, "main.beancount")

	if err := os.WriteFile(diskLeaf, []byte("2024-01-01 open Assets:DiskAccount USD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("include \"*.beancount\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// overlayLeaf does not exist on disk.
	overlay := map[string][]byte{
		overlayLeaf: []byte("2024-01-01 open Assets:OverlayAccount USD\n"),
	}
	ledger, err := loader.LoadFile(context.Background(), root, loader.WithOverlay(overlay))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("LoadFile() glob union: unexpected error diagnostic: %s", d.Message)
		}
		if d.Severity == ast.Warning && strings.Contains(d.Message, "matched no files") {
			t.Errorf("LoadFile() glob union: emitted 'matched no files' warning despite overlay hit")
		}
	}
	var foundDisk, foundOverlay bool
	for _, d := range ledger.All() {
		if o, ok := d.(*ast.Open); ok {
			switch o.Account {
			case "Assets:DiskAccount":
				foundDisk = true
			case "Assets:OverlayAccount":
				foundOverlay = true
			}
		}
	}
	if !foundDisk {
		t.Errorf("LoadFile() glob union: disk file (a.beancount) directives missing from result")
	}
	if !foundOverlay {
		t.Errorf("LoadFile() glob union: overlay-only file (b.beancount) directives missing from result")
	}
}

func TestLoadFile_OverlayGlobOverlayOnly(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	overlayLeaf := filepath.Join(dir, "leaf.beancount")

	if err := os.WriteFile(root, []byte("include \"*.beancount\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// overlayLeaf does NOT exist on disk.
	overlay := map[string][]byte{
		overlayLeaf: []byte("2024-01-01 open Assets:OverlayOnly USD\n"),
	}
	ledger, err := loader.LoadFile(context.Background(), root, loader.WithOverlay(overlay))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("LoadFile() glob overlay-only: unexpected error diagnostic: %s", d.Message)
		}
		if d.Severity == ast.Warning && strings.Contains(d.Message, "matched no files") {
			t.Errorf("LoadFile() glob overlay-only: emitted 'matched no files' warning despite overlay hit")
		}
	}
	var found bool
	for _, d := range ledger.All() {
		if o, ok := d.(*ast.Open); ok && o.Account == "Assets:OverlayOnly" {
			found = true
		}
	}
	if !found {
		t.Errorf("LoadFile() glob overlay-only: overlay-only file not loaded via glob")
	}
}

func TestLoad_OverlayNonAbsoluteKeyWarning(t *testing.T) {
	src := "2024-01-01 open Assets:Bank USD\n"
	overlay := map[string][]byte{
		"relative/alpha.beancount": []byte("2024-01-01 open Assets:Alpha USD\n"),
		"relative/beta.beancount":  []byte("2024-01-01 open Assets:Beta USD\n"),
	}
	ledger, err := loader.Load(context.Background(), src, loader.WithOverlay(overlay))
	if err != nil {
		t.Fatal(err)
	}
	var warnMessages []string
	for _, d := range ledger.Diagnostics {
		if d.Code == "overlay-non-absolute-key" {
			if d.Severity != ast.Warning {
				t.Errorf("Load() non-absolute key: Severity = %v, want Warning", d.Severity)
			}
			if d.Span != (ast.Span{}) {
				t.Errorf("Load() non-absolute key: Span = %v, want zero", d.Span)
			}
			warnMessages = append(warnMessages, d.Message)
		}
	}
	if len(warnMessages) != 2 {
		t.Errorf("Load() non-absolute key: warning count = %d, want 2; diagnostics: %+v", len(warnMessages), ledger.Diagnostics)
	}
	// Messages must be sorted for determinism.
	if len(warnMessages) == 2 && warnMessages[0] > warnMessages[1] {
		t.Errorf("Load() non-absolute key: warnings not in sorted order: %q then %q", warnMessages[0], warnMessages[1])
	}
	// Relative keys must have no effect.
	for _, d := range ledger.All() {
		if o, ok := d.(*ast.Open); ok && (o.Account == "Assets:Alpha" || o.Account == "Assets:Beta") {
			t.Errorf("Load() non-absolute key: relative overlay key was applied despite being invalid")
		}
	}
}

func TestLoadFile_OverlayLastWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(path, []byte("2024-01-01 open Assets:Bank USD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := map[string][]byte{
		path: []byte("2024-01-01 open Assets:Bank GBP\n"),
	}
	second := map[string][]byte{
		path: []byte("2024-01-01 open Assets:Bank EUR\n"),
	}
	ledger, err := loader.LoadFile(context.Background(), path,
		loader.WithOverlay(first),
		loader.WithOverlay(second),
	)
	if err != nil {
		t.Fatal(err)
	}
	var currencies []string
	for _, d := range ledger.All() {
		if o, ok := d.(*ast.Open); ok && o.Account == "Assets:Bank" {
			currencies = append(currencies, o.Currencies...)
		}
	}
	if len(currencies) != 1 || currencies[0] != "EUR" {
		t.Errorf("LoadFile() last-wins: got currencies %v, want [EUR]", currencies)
	}
}

func TestLoadFile_OverlayWithBaseDir(t *testing.T) {
	dir := t.TempDir()
	leaf := filepath.Join(dir, "leaf.beancount")
	// leaf does not exist on disk; overlay provides it.
	overlay := map[string][]byte{
		leaf: []byte("2024-01-01 open Assets:OverlayLeaf USD\n"),
	}
	src := "include \"leaf.beancount\"\n"
	ledger, err := ast.Load(src, ast.WithBaseDir(dir), ast.WithOverlay(overlay))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("Load() with WithBaseDir+WithOverlay: unexpected error diagnostic: %s", d.Message)
		}
	}
	var found bool
	for _, d := range ledger.All() {
		if o, ok := d.(*ast.Open); ok && o.Account == "Assets:OverlayLeaf" {
			found = true
		}
	}
	if !found {
		t.Errorf("Load() with WithBaseDir+WithOverlay: overlay leaf not reached via relative include")
	}
}

func TestLoadFile_OverlayEmptyMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(path, []byte("2024-01-01 open Assets:Bank USD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		overlay map[string][]byte
	}{
		{"nil", nil},
		{"empty", map[string][]byte{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ledger, err := loader.LoadFile(context.Background(), path, loader.WithOverlay(tc.overlay))
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range ledger.Diagnostics {
				if d.Severity == ast.Error {
					t.Errorf("LoadFile() WithOverlay(%s): unexpected error diagnostic: %s", tc.name, d.Message)
				}
			}
			var found bool
			for _, d := range ledger.All() {
				if o, ok := d.(*ast.Open); ok && o.Account == "Assets:Bank" {
					found = true
				}
			}
			if !found {
				t.Errorf("LoadFile() WithOverlay(%s): disk-backed Open not loaded", tc.name)
			}
		})
	}
}
