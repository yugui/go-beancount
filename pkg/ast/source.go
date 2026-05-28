package ast

import "os"

// sourceReader supplies raw source bytes for a given absolute path.
type sourceReader interface {
	read(absPath string) ([]byte, error)
}

type sourceReaderFunc func(string) ([]byte, error)

func (f sourceReaderFunc) read(p string) ([]byte, error) { return f(p) }

// defaultSource is the on-disk reader used by all loaders unless overridden.
var defaultSource sourceReader = sourceReaderFunc(os.ReadFile)
