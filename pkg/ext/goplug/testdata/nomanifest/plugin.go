// Package main is a goplug test fixture: InitPlugin is present but the
// required Manifest symbol is omitted. The loader must reject this
// plugin before invoking InitPlugin.
package main

// InitPlugin would register the plugin, but the loader must never
// reach this code because Manifest is missing.
func InitPlugin() error {
	panic("goplug: nomanifest fixture's InitPlugin should never be invoked")
}

func main() {}
