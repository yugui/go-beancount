// Package atomicfile writes files atomically.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Write atomically replaces the file at path with data. It creates a
// temp file in the same directory, fsyncs it, closes it, then renames
// over path. If path already exists, the new file inherits its
// permission bits; otherwise the OS default for [os.CreateTemp] applies.
//
// Write does not create parent directories; the caller is responsible
// for ensuring filepath.Dir(path) exists.
//
// On any error after the temp file is created, Write best-effort
// removes the temp file before returning.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := f.Name()

	// Ensure cleanup on failure.
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("fsyncing temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	// Preserve original file permissions if path already exists.
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if err := os.Chmod(tmpPath, info.Mode().Perm()); err != nil {
			return fmt.Errorf("preserving file permissions: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
		// Nothing to preserve.
	default:
		return fmt.Errorf("stat-ing destination file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	success = true
	return nil
}
