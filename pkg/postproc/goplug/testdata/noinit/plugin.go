// Package main is a goplug test fixture: Manifest is valid but the
// InitPlugin symbol is not exported. The loader must reject it.
package main

import (
	"github.com/yugui/go-beancount/pkg/postproc/goplug"
)

var Manifest = goplug.Manifest{
	APIVersion: goplug.APIVersion,
	Name:       "github.com/yugui/go-beancount/pkg/postproc/goplug/testdata/noinit",
}

// Deliberately no InitPlugin function.

func main() {}
