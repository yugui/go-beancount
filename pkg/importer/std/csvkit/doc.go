// Package csvkit provides reusable building blocks for assembling
// CSV/TSV importers: a record-reading engine and numeric parsing
// primitives. [github.com/yugui/go-beancount/pkg/importer/std/csvimp] is
// the reference consumer; third-party importers may compose these blocks
// into their own [github.com/yugui/go-beancount/pkg/importer.Importer]
// without depending on csvimp's factory registration.
//
// API stability: csvkit's exported surface is still evolving and is NOT
// yet frozen. Callers depending on it directly should pin a version.
package csvkit
