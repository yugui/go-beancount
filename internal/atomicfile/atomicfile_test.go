package atomicfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yugui/go-beancount/internal/atomicfile"
)

func TestWrite_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	want := []byte("hello world\n")

	if err := atomicfile.Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Write: file contents = %q; want %q", got, want)
	}
}

func TestWrite_OverwriteExisting_PreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	// Pre-create with 0600.
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	// Re-chmod in case the process umask altered the bits at creation.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod seed file: %v", err)
	}

	want := []byte("new contents")
	if err := atomicfile.Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Write: file contents = %q; want %q", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("Write: file mode = %o; want %o", got, want)
	}
}

func TestWrite_NoLeftoverTempFiles_OnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	if err := atomicfile.Write(path, []byte("data")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("ReadDir returned %d entries (%v); want 1", len(entries), names)
	}
	if entries[0].Name() != "out.txt" {
		t.Errorf("Write: entry name = %q; want %q", entries[0].Name(), "out.txt")
	}
}

func TestWrite_NonexistentParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "out.txt")

	err := atomicfile.Write(path, []byte("data"))
	if err == nil {
		t.Fatal("Write: expected error for nonexistent parent directory, got nil")
	}

	// Ensure no stray temp files materialized in the parent (which doesn't
	// exist) — and the destination was not somehow created.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("destination unexpectedly exists or stat error: %v", err)
	}
}

func TestWrite_NoLeftoverTempFiles_OnError(t *testing.T) {
	// Exercise the post-CreateTemp cleanup path: pre-create a directory
	// at the destination so that CreateTemp succeeds (parent exists) but
	// the final os.Rename fails because the destination is a non-empty
	// directory of a different type than the temp file.
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("seed directory at destination: %v", err)
	}
	// Place a child inside so rename-onto-directory is unambiguously
	// rejected on platforms that might otherwise allow replacing an
	// empty directory.
	if err := os.WriteFile(filepath.Join(path, "child"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed child inside destination directory: %v", err)
	}

	if err := atomicfile.Write(path, []byte("data")); err == nil {
		t.Fatal("Write: expected error from rename onto directory, got nil")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("Write: ReadDir returned %d entries (%v); want 1 (only the destination)", len(entries), names)
	}
	if len(entries) > 0 && entries[0].Name() != "out.txt" {
		t.Errorf("Write: entry name = %q; want %q", entries[0].Name(), "out.txt")
	}
}
