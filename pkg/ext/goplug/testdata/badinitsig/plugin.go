// Package main is a goplug test fixture: Manifest is valid but the
// exported InitPlugin symbol is not a func() error (it is an int
// variable instead). The loader must reject it.
package main

import (
	"github.com/yugui/go-beancount/pkg/ext/goplug"
)

var Manifest = goplug.Manifest{
	APIVersion: goplug.APIVersion,
	Name:       "github.com/yugui/go-beancount/pkg/ext/goplug/testdata/badinitsig",
}

// InitPlugin has the wrong type on purpose.
var InitPlugin = 7

func main() {}
