package print

import (
	"context"
	"fmt"
	"os"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/printer"
)

func init() {
	postproc.Register("beansprout.plugins.print", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/sprout/print", api.PluginFunc(apply))
}

// apply writes options and every directive to os.Stderr, then returns the
// ledger unchanged. See the package godoc for the deliberate os.Stderr
// deviation and upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}

	for _, e := range in.Options.Snapshot() {
		var val any
		switch e.Kind {
		case ast.KindString:
			val = e.String()
		case ast.KindBool:
			val = e.Bool()
		case ast.KindDecimal:
			val = e.Decimal()
		case ast.KindStringList:
			val = e.StringList()
		case ast.KindInt:
			val = e.Int()
		case ast.KindDecimalMap:
			val = e.DecimalMap()
		case ast.KindIntMap:
			val = e.IntMap()
		}
		fmt.Fprintf(os.Stderr, "%s: %v\n", e.Key, val)
	}

	if in.Directives != nil {
		for _, d := range in.Directives {
			_ = printer.Fprint(os.Stderr, d)
		}
	}

	return api.Result{}, nil
}
