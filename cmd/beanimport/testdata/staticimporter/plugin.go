package main

import (
	"context"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/goplug"
	"github.com/yugui/go-beancount/pkg/importer"
)

const pluginName = "staticimporter"

const importerKind = "github.com/yugui/go-beancount/cmd/beanimport/testdata/staticimporter"

var Manifest = goplug.Manifest{
	APIVersion: goplug.APIVersion,
	Name:       pluginName,
	Version:    "v0.0.0-fixture",
}

func InitPlugin() error {
	importer.RegisterFactory(importerKind, importer.FactoryFunc(newStatic))
	return nil
}

func newStatic(name string, decode func(dest any) error) (importer.Importer, error) {
	if err := decode(&struct{}{}); err != nil {
		return nil, err
	}
	return &staticImp{name: name}, nil
}

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

func main() {}
