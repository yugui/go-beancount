package ast

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// hasGlobMeta reports whether path contains any glob metacharacter
// recognized by expandGlob ("*", "?", "[").
func hasGlobMeta(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

// expandGlob returns the files matching pattern, sorted ascending. It
// supports the same metacharacters as path/filepath.Match plus "**" as
// a path component matching zero or more directories. If pattern
// contains no glob metacharacter, it is returned as-is so callers can
// surface an open/read error against the literal path. Unreadable
// subtrees encountered during the walk are skipped.
func expandGlob(pattern string) ([]string, error) {
	if !hasGlobMeta(pattern) {
		return []string{pattern}, nil
	}
	root := walkRoot(pattern)
	var matches []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ok, err := matchDoubleStar(pattern, path)
		if err != nil {
			return err
		}
		if ok {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// walkRoot returns the longest leading path of pattern that contains no
// glob metacharacter, used as the starting point for the directory
// walk. Returns "." (or "/" for absolute patterns) when the very first
// segment is already a glob.
func walkRoot(pattern string) string {
	parts := strings.Split(filepath.ToSlash(pattern), "/")
	var staticParts []string
	for _, part := range parts {
		if hasGlobMeta(part) {
			break
		}
		staticParts = append(staticParts, part)
	}
	if len(staticParts) == 0 {
		return "."
	}
	if len(staticParts) == 1 && staticParts[0] == "" {
		return "/"
	}
	return filepath.FromSlash(strings.Join(staticParts, "/"))
}

// matchDoubleStar reports whether name matches pattern, treating "**"
// as a path component that matches zero or more name components and
// every other segment as a path/filepath.Match pattern.
func matchDoubleStar(pattern, name string) (bool, error) {
	pSegs := strings.Split(filepath.ToSlash(pattern), "/")
	nSegs := strings.Split(filepath.ToSlash(name), "/")
	return matchSegments(pSegs, nSegs)
}

func matchSegments(pat, name []string) (bool, error) {
	if len(pat) == 0 {
		return len(name) == 0, nil
	}
	if pat[0] == "**" {
		for k := 0; k <= len(name); k++ {
			ok, err := matchSegments(pat[1:], name[k:])
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	if len(name) == 0 {
		return false, nil
	}
	ok, err := filepath.Match(pat[0], name[0])
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return matchSegments(pat[1:], name[1:])
}
