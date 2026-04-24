package ast

import (
	"os"
	"path/filepath"
)

// ResolvePath resolves path against the directory context implied by ledger
// and spanFilename, returning an absolute path suitable for filesystem
// access. It centralizes the base-directory fallback used by plugins (e.g.
// the document plugin) that consume paths appearing inside ledger source.
//
// Resolution order:
//
//  1. If path is absolute, it is returned unchanged.
//  2. If spanFilename is absolute, path is joined to filepath.Dir(spanFilename).
//  3. Otherwise the ledger root — ledger.Files[0].Filename — is used (after
//     making it absolute relative to the working directory if needed). When
//     spanFilename is non-empty it is first joined with the root directory
//     to allow per-include relative spans.
//  4. If no anchor can be derived (no root file and os.Getwd fails), path
//     is returned unchanged so callers can surface a meaningful error.
//
// The returned string is non-empty whenever path is non-empty: every
// resolution branch either returns path verbatim or filepath.Join's path
// onto a directory. ResolvePath performs no filesystem stat — callers
// must check existence themselves.
func ResolvePath(ledger *Ledger, spanFilename, path string) string {
	if filepath.IsAbs(path) {
		return path
	}

	if filepath.IsAbs(spanFilename) {
		return filepath.Join(filepath.Dir(spanFilename), path)
	}

	rootDir, ok := ledgerBaseDir(ledger)
	if !ok {
		return path
	}
	if spanFilename == "" {
		return filepath.Join(rootDir, path)
	}
	return filepath.Join(filepath.Dir(filepath.Join(rootDir, spanFilename)), path)
}

// ledgerBaseDir returns the directory that anchors relative paths for ledger.
// The returned path is absolute when ok is true. ok is false only when no
// root file is recorded and the working directory cannot be obtained.
func ledgerBaseDir(ledger *Ledger) (string, bool) {
	var root string
	if ledger != nil && len(ledger.Files) > 0 {
		root = ledger.Files[0].Filename
	}
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		return cwd, true
	}
	if !filepath.IsAbs(root) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		root = filepath.Join(cwd, root)
	}
	return filepath.Dir(root), true
}
