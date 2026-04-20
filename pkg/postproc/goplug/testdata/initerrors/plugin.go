// Package main is a goplug test fixture: Manifest is valid and
// InitPlugin has the right signature, but InitPlugin itself returns a
// non-nil error. The loader must propagate that error unwrappable via
// errors.Is.
package main

import (
	"errors"

	"github.com/yugui/go-beancount/pkg/postproc/goplug"
)

var Manifest = goplug.Manifest{
	APIVersion: goplug.APIVersion,
	Name:       "github.com/yugui/go-beancount/pkg/postproc/goplug/testdata/initerrors",
}

// ErrInit is the sentinel the test compares against via errors.Is.
var ErrInit = errors.New("goplug-test: init failed")

func InitPlugin() error {
	return ErrInit
}

func main() {}
