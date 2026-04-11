package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wellFormatted is a snippet that is already formatted with default options.
const wellFormatted = "2024-01-15 * \"Store\" \"Groceries\"\n  Expenses:Food                            50.00 USD\n  Assets:Checking\n"

// unformatted has no blank line between directives and needs normalization.
const unformatted = "2024-01-01 open Assets:Cash\n2024-01-02 open Expenses:Food\n"

// formatted is the expected output of unformatted with default options.
const formatted = "2024-01-01 open Assets:Cash\n\n2024-01-02 open Expenses:Food\n"

func TestFormatStdin(t *testing.T) {
	var stdout bytes.Buffer
	err := run(nil, strings.NewReader(unformatted), &stdout)
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if got := stdout.String(); got != formatted {
		t.Errorf("stdout mismatch:\ngot:  %q\nwant: %q", got, formatted)
	}
}

func TestFormatFileToStdout(t *testing.T) {
	path := writeTempFile(t, unformatted)

	var stdout bytes.Buffer
	err := run([]string{path}, nil, &stdout)
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if got := stdout.String(); got != formatted {
		t.Errorf("stdout mismatch:\ngot:  %q\nwant: %q", got, formatted)
	}
}

func TestFormatFileInPlace(t *testing.T) {
	path := writeTempFile(t, unformatted)

	var stdout bytes.Buffer
	err := run([]string{"-w", path}, nil, &stdout)
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected no stdout output with -w, got: %q", stdout.String())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if got := string(data); got != formatted {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, formatted)
	}
}

func TestFormatMultipleFiles(t *testing.T) {
	path1 := writeTempFile(t, unformatted)
	path2 := writeTempFile(t, wellFormatted)

	var stdout bytes.Buffer
	err := run([]string{path1, path2}, nil, &stdout)
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}

	got := stdout.String()

	// Should contain headers for both files.
	if !strings.Contains(got, "==> "+path1+" <==") {
		t.Errorf("missing header for first file")
	}
	if !strings.Contains(got, "==> "+path2+" <==") {
		t.Errorf("missing header for second file")
	}
	// Should contain formatted content.
	if !strings.Contains(got, formatted) {
		t.Errorf("missing formatted content of first file")
	}
	if !strings.Contains(got, wellFormatted) {
		t.Errorf("missing content of second file")
	}
}

func TestCustomOptions(t *testing.T) {
	// Test -comma flag inserts commas.
	src := "2024-01-15 * \"Store\" \"Groceries\"\n  Expenses:Food  12345.00 USD\n  Assets:Checking\n"

	var stdout bytes.Buffer
	path := writeTempFile(t, src)
	err := run([]string{"-comma", path}, nil, &stdout)
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if !strings.Contains(stdout.String(), "12,345.00") {
		t.Errorf("expected comma grouping in output, got: %q", stdout.String())
	}

	// Test -column flag.
	stdout.Reset()
	err = run([]string{"-column", "60", path}, nil, &stdout)
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}
	// With column=60 and no comma, the amount should be further right.
	got := stdout.String()
	if got == "" {
		t.Fatal("expected non-empty output")
	}
}

func TestWriteRequiresFiles(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"-w"}, strings.NewReader(""), &stdout)
	if err == nil {
		t.Fatal("expected error when -w used without files")
	}
	if !strings.Contains(err.Error(), "-w requires") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.beancount")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	return path
}
