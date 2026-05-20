// This file is the .so entry point. It must be compiled with
// -buildmode=plugin and is loaded by cmd/beanimport's --plugin flag
// in the integration tests. See doc.go for the fixture's role.

package main

import (
	"context"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/goplug"
	"github.com/yugui/go-beancount/pkg/importer"
)

// pluginName is the Manifest-facing name. The registered kind is "static"
// (a separate string); changing either also requires updating fixture_test.go.
const pluginName = "staticimporter"

// Manifest is exported so goplug.Load can read it via plugin.Lookup.
var Manifest = goplug.Manifest{
	APIVersion: goplug.APIVersion,
	Name:       pluginName,
	Version:    "v0.0.0-fixture",
}

// InitPlugin is the goplug entry point. Called once after the
// Manifest checks pass; a non-nil return aborts the load.
func InitPlugin() error {
	importer.RegisterFactory("static", importer.FactoryFunc(newStatic))
	return nil
}

// newStatic is the factory for the "static" kind.
func newStatic(name string, decode func(dest any) error) (importer.Importer, error) {
	if err := decode(&struct{}{}); err != nil {
		return nil, err
	}
	return &staticImp{name: name}, nil
}

// staticImp is a minimal importer that returns a single canned Transaction.
type staticImp struct {
	name string
}

func (s *staticImp) Name() string { return s.name }

func (s *staticImp) Identify(_ context.Context, _ importer.Input) bool { return true }

func (s *staticImp) Extract(_ context.Context, _ importer.Input) (importer.Output, error) {
	var num apd.Decimal
	num.SetInt64(1)
	tx := &ast.Transaction{
		Date:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag:      '*',
		Payee:     "static-fixture",
		Narration: "",
		Postings: []ast.Posting{
			{
				Account: "Assets:Static",
				Amount:  &ast.Amount{Number: num, Currency: "USD"},
			},
			{
				Account: "Equity:Other",
				Amount:  nil,
			},
		},
	}
	return importer.Output{Directives: []ast.Directive{tx}}, nil
}

// main is required for buildmode=plugin but is never invoked.
func main() {}
