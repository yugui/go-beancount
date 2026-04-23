// Package main is a goplug test fixture: Manifest is declared but has
// the wrong type (plain int instead of goplug.Manifest). The loader
// must reject it with a type-mismatch error.
package main

// Manifest has the wrong type on purpose.
var Manifest = 42

func InitPlugin() error {
	panic("goplug: badmanifest fixture's InitPlugin should never be invoked")
}

func main() {}
