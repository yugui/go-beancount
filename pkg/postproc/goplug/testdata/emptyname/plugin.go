// Package main is a goplug test fixture: Manifest.APIVersion matches
// but Manifest.Name is empty. The loader must reject it.
package main

import (
	"github.com/yugui/go-beancount/pkg/postproc/goplug"
)

var Manifest = goplug.Manifest{
	APIVersion: goplug.APIVersion,
	Name:       "", // intentionally empty
}

func InitPlugin() error {
	panic("goplug: emptyname fixture's InitPlugin should never be invoked")
}

func main() {}
