// Package main is a goplug test fixture: Manifest.APIVersion is set
// to a value that cannot match the host's goplug.APIVersion. The
// loader must reject it with an api version mismatch error.
package main

import (
	"github.com/yugui/go-beancount/pkg/postproc/goplug"
)

// Manifest is intentionally misaligned: APIVersion is set to a sentinel
// value that the host's goplug.APIVersion will never equal.
var Manifest = goplug.Manifest{
	APIVersion: 9999,
	Name:       "github.com/yugui/go-beancount/pkg/postproc/goplug/testdata/badapiversion",
}

func InitPlugin() error {
	panic("goplug: badapiversion fixture's InitPlugin should never be invoked")
}

func main() {}
