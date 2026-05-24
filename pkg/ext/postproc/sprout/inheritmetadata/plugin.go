package inheritmetadata

import (
	"bufio"
	"context"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

func init() {
	postproc.Register("beansprout.plugins.inherit_metadata", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/sprout/inheritmetadata", api.PluginFunc(apply))
}

// apply fills in missing metadata on Open directives by inheriting values
// from the nearest ancestor account that carries each configured key.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	keys := parseConfig(in.Config)
	if len(keys) == 0 {
		return api.Result{}, nil
	}

	// Pass 1: collect all directives and build the account metadata index.
	var all []ast.Directive
	index := make(map[ast.Account]map[string]ast.MetaValue)
	for _, d := range in.Directives {
		all = append(all, d)
		if o, ok := d.(*ast.Open); ok {
			if o.Meta.Props != nil {
				for _, k := range keys {
					if v, ok := o.Meta.Props[k]; ok {
						if index[o.Account] == nil {
							index[o.Account] = make(map[string]ast.MetaValue)
						}
						index[o.Account][k] = v
					}
				}
			}
		}
	}

	// Pass 2: rebuild the directive slice, adding inherited metadata where needed.
	changed := false
	out := make([]ast.Directive, len(all))
	for i, d := range all {
		o, ok := d.(*ast.Open)
		if !ok {
			out[i] = d
			continue
		}
		inherited := findInherited(o.Account, keys, index, o.Meta.Props)
		if len(inherited) == 0 {
			out[i] = o
			continue
		}
		out[i] = withMeta(o, inherited)
		changed = true
	}

	if !changed {
		return api.Result{}, nil
	}
	return api.Result{Directives: out}, nil
}

// parseConfig extracts the list of metadata key names from the plugin
// config string. Blank lines and lines beginning with ';' are skipped.
func parseConfig(config string) []string {
	var keys []string
	sc := bufio.NewScanner(strings.NewReader(config))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		keys = append(keys, line)
	}
	return keys
}

// findInherited returns the metadata key/value pairs that should be
// inherited by the Open at acct. Only keys not already present in
// existing are considered.
func findInherited(
	acct ast.Account,
	keys []string,
	index map[ast.Account]map[string]ast.MetaValue,
	existing map[string]ast.MetaValue,
) map[string]ast.MetaValue {
	var result map[string]ast.MetaValue
	for _, k := range keys {
		if _, ok := existing[k]; ok {
			continue
		}
		if v, found := walkParents(acct, k, index); found {
			if result == nil {
				result = make(map[string]ast.MetaValue)
			}
			result[k] = v
		}
	}
	return result
}

// walkParents traverses the parent chain of acct looking for the first
// ancestor whose index entry contains key. Returns the value and true
// when found, or zero and false otherwise.
func walkParents(acct ast.Account, key string, index map[ast.Account]map[string]ast.MetaValue) (ast.MetaValue, bool) {
	for cur := acct.Parent(); cur != ""; cur = cur.Parent() {
		if m, ok := index[cur]; ok {
			if v, ok := m[key]; ok {
				return v, true
			}
		}
	}
	return ast.MetaValue{}, false
}

// withMeta returns a new *ast.Open that copies o with the extra key/value
// pairs in inherited merged into a freshly allocated metadata map. The
// original o is never mutated.
func withMeta(o *ast.Open, inherited map[string]ast.MetaValue) *ast.Open {
	size := len(inherited)
	if o.Meta.Props != nil {
		size += len(o.Meta.Props)
	}
	props := make(map[string]ast.MetaValue, size)
	for k, v := range o.Meta.Props {
		props[k] = v
	}
	for k, v := range inherited {
		props[k] = v
	}
	out := *o
	out.Meta = ast.Metadata{Props: props}
	return &out
}
