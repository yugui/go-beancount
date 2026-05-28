package infermetadata

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"gopkg.in/yaml.v3"
)

const (
	codeInvalidConfig   = "infer-metadata-invalid-config"
	codeNoSourceFile    = "infer-metadata-no-source-file"
	codeYAMLReadError   = "infer-metadata-yaml-read-error"
	codeYAMLKeyNotFound = "infer-metadata-yaml-key-not-found"

	tokenCommodity = "__commodity__"
	tokenAccount   = "__account__"
)

func init() {
	postproc.Register("beansprout.plugins.infer_metadata", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/sprout/infermetadata", api.PluginFunc(apply))
}

// rule is one parsed entry from the plugin config.
type rule struct {
	directive   string
	target      string
	source      string
	mappingFile string
}

// apply implements the infermetadata behavior described in the package doc.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	rules, parseDiags := parseConfig(in.Config, in.Directive)
	if len(rules) == 0 {
		if len(parseDiags) > 0 {
			return api.Result{Diagnostics: parseDiags}, nil
		}
		return api.Result{}, nil
	}

	diags := parseDiags
	rules, diags = resolveFileRules(rules, in.SourceFilename, in.Directive, diags)

	byType := groupByDirective(rules)

	baseDir := filepath.Dir(in.SourceFilename)
	mappings := make(map[string]map[string]any)

	var out []ast.Directive
	changed := false
	for _, d := range in.Directives {
		rs := byType[directiveName(d)]
		if len(rs) == 0 {
			out = append(out, d)
			continue
		}
		newD, ds := applyRules(d, rs, mappings, baseDir, in.Directive)
		diags = append(diags, ds...)
		if newD != d {
			changed = true
		}
		out = append(out, newD)
	}

	res := api.Result{Diagnostics: diags}
	if changed {
		res.Directives = out
	}
	return res, nil
}

// parseConfig returns the rules parsed from config, along with one diagnostic
// per malformed line.
func parseConfig(config string, plug *ast.Plugin) ([]rule, []ast.Diagnostic) {
	var rules []rule
	var diags []ast.Diagnostic

	for _, raw := range strings.Split(config, "\n") {
		line := raw
		if i := strings.IndexByte(line, ';'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 3 {
			diags = append(diags, ast.Diagnostic{
				Code:     codeInvalidConfig,
				Span:     spanOf(plug),
				Severity: ast.Error,
				Message:  fmt.Sprintf("expected '<directive> <target> <source> [file:<path>]', got %q", line),
			})
			continue
		}
		r := rule{directive: parts[0], target: parts[1], source: parts[2]}
		if len(parts) >= 4 && strings.HasPrefix(parts[3], "file:") {
			r.mappingFile = strings.TrimPrefix(parts[3], "file:")
		}
		rules = append(rules, r)
	}
	return rules, diags
}

// resolveFileRules drops rules whose YAML path cannot be resolved (no source
// filename) and appends a single diagnostic explaining the suppression. The
// non-file rules are returned unchanged.
func resolveFileRules(rules []rule, sourceFilename string, plug *ast.Plugin, diags []ast.Diagnostic) ([]rule, []ast.Diagnostic) {
	hasFile := false
	for _, r := range rules {
		if r.mappingFile != "" {
			hasFile = true
			break
		}
	}
	if !hasFile || sourceFilename != "" {
		return rules, diags
	}

	kept := rules[:0]
	for _, r := range rules {
		if r.mappingFile == "" {
			kept = append(kept, r)
		}
	}
	diags = append(diags, ast.Diagnostic{
		Code:     codeNoSourceFile,
		Span:     spanOf(plug),
		Severity: ast.Error,
		Message:  "cannot resolve YAML mapping path: api.Input.SourceFilename is empty; file-based rules skipped",
	})
	return kept, diags
}

// groupByDirective bins rules under their directive_type key.
func groupByDirective(rules []rule) map[string][]rule {
	out := make(map[string][]rule)
	for _, r := range rules {
		out[r.directive] = append(out[r.directive], r)
	}
	return out
}

// applyRules walks the rules registered for d's directive type and returns
// either d (when no rule fires) or a freshly built directive carrying the
// inferred metadata. Diagnostics surface YAML lookup failures.
func applyRules(d ast.Directive, rules []rule, mappings map[string]map[string]any, baseDir string, plug *ast.Plugin) (ast.Directive, []ast.Diagnostic) {
	props := d.DirMeta().Props
	var diags []ast.Diagnostic
	cloned := false

	for _, r := range rules {
		if _, ok := props[r.target]; ok {
			continue
		}

		srcVal, ok := sourceValue(d, r.source, props)
		if !ok {
			continue
		}

		var value ast.MetaValue
		if r.mappingFile != "" {
			m, loadDiag, loaded := loadMapping(r.mappingFile, baseDir, mappings, plug)
			if loadDiag != nil {
				diags = append(diags, *loadDiag)
			}
			if !loaded {
				continue
			}
			key, ok := metaValueAsKey(srcVal)
			if !ok {
				continue
			}
			raw, present := m[key]
			if !present {
				diags = append(diags, ast.Diagnostic{
					Code:     codeYAMLKeyNotFound,
					Span:     directiveSpan(d, plug),
					Severity: ast.Error,
					Message:  fmt.Sprintf("key %q not found in mapping file %s", key, r.mappingFile),
				})
				continue
			}
			value = yamlValueToMeta(raw)
		} else {
			value = srcVal
		}

		if !cloned {
			props = copyProps(props)
			cloned = true
		}
		props[r.target] = value
	}

	if !cloned {
		return d, diags
	}
	return withMeta(d, ast.Metadata{Props: props}), diags
}

// sourceValue returns the value selected by r.source on d. The second return
// value is false when the source is absent (regular meta key) or inapplicable
// (special token on a directive type that doesn't support it).
func sourceValue(d ast.Directive, source string, props map[string]ast.MetaValue) (ast.MetaValue, bool) {
	switch source {
	case tokenCommodity:
		c, ok := d.(*ast.Commodity)
		if !ok {
			return ast.MetaValue{}, false
		}
		return ast.MetaValue{Kind: ast.MetaCurrency, String: c.Currency}, true
	case tokenAccount:
		acct, ok := ast.AccountOf(d)
		if !ok {
			return ast.MetaValue{}, false
		}
		return ast.MetaValue{Kind: ast.MetaString, String: leafName(acct)}, true
	default:
		v, ok := props[source]
		return v, ok
	}
}

// loadMapping returns the YAML mapping for path (resolved against baseDir),
// caching it across calls. The cache key is the raw rule path so the same
// "file:foo.yaml" reference always hits the same cache entry within an Apply
// call. loadDiag carries any read/parse error encountered on first load,
// emitted only the first time; loaded is false when that load attempt
// failed.
func loadMapping(path, baseDir string, cache map[string]map[string]any, plug *ast.Plugin) (m map[string]any, loadDiag *ast.Diagnostic, loaded bool) {
	if cached, present := cache[path]; present {
		return cached, nil, cached != nil
	}
	resolved := filepath.Join(baseDir, path)
	raw, err := os.ReadFile(resolved)
	if err != nil {
		cache[path] = nil
		return nil, &ast.Diagnostic{
			Code:     codeYAMLReadError,
			Span:     spanOf(plug),
			Severity: ast.Error,
			Message:  fmt.Sprintf("cannot read YAML mapping %s: %v", path, err),
		}, false
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		cache[path] = nil
		return nil, &ast.Diagnostic{
			Code:     codeYAMLReadError,
			Span:     spanOf(plug),
			Severity: ast.Error,
			Message:  fmt.Sprintf("cannot parse YAML mapping %s: %v", path, err),
		}, false
	}
	cache[path] = parsed
	return parsed, nil, true
}

// metaValueAsKey returns the string form of v that is used as the YAML map
// key. Only string-shaped meta kinds are supported as YAML keys.
func metaValueAsKey(v ast.MetaValue) (string, bool) {
	switch v.Kind {
	case ast.MetaString, ast.MetaAccount, ast.MetaCurrency, ast.MetaTag, ast.MetaLink:
		return v.String, true
	}
	return "", false
}

// yamlValueToMeta wraps a yaml-decoded value into a MetaValue. Strings and
// booleans map to their natural MetaValue kinds; anything else (numbers,
// nested maps, sequences) collapses to its fmt-formatted string. Numeric
// YAML scalars are deliberately stringified to keep the public meta surface
// stable across yaml.v3 type-inference quirks; users that need numeric
// metadata can quote the value in YAML.
func yamlValueToMeta(v any) ast.MetaValue {
	switch x := v.(type) {
	case string:
		return ast.MetaValue{Kind: ast.MetaString, String: x}
	case bool:
		return ast.MetaValue{Kind: ast.MetaBool, Bool: x}
	default:
		return ast.MetaValue{Kind: ast.MetaString, String: fmt.Sprint(v)}
	}
}

// directiveName returns the lowercase tag matching the rule DSL's directive
// type column.
func directiveName(d ast.Directive) string {
	switch d.(type) {
	case *ast.Open:
		return "open"
	case *ast.Close:
		return "close"
	case *ast.Balance:
		return "balance"
	case *ast.Pad:
		return "pad"
	case *ast.Document:
		return "document"
	case *ast.Note:
		return "note"
	case *ast.Commodity:
		return "commodity"
	case *ast.Transaction:
		return "transaction"
	}
	return ""
}

// leafName returns the last colon-separated component of acct.
func leafName(acct ast.Account) string {
	s := string(acct)
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// withMeta returns a shallow copy of d with its Meta field replaced. The
// input directive is never mutated. Header directives (Option, Plugin,
// Include), which carry no metadata, are returned unchanged.
func withMeta(d ast.Directive, meta ast.Metadata) ast.Directive {
	switch x := d.(type) {
	case *ast.Open:
		c := *x
		c.Meta = meta
		return &c
	case *ast.Close:
		c := *x
		c.Meta = meta
		return &c
	case *ast.Balance:
		c := *x
		c.Meta = meta
		return &c
	case *ast.Pad:
		c := *x
		c.Meta = meta
		return &c
	case *ast.Document:
		c := *x
		c.Meta = meta
		return &c
	case *ast.Note:
		c := *x
		c.Meta = meta
		return &c
	case *ast.Commodity:
		c := *x
		c.Meta = meta
		return &c
	case *ast.Transaction:
		c := *x
		c.Meta = meta
		return &c
	case *ast.Event:
		c := *x
		c.Meta = meta
		return &c
	case *ast.Price:
		c := *x
		c.Meta = meta
		return &c
	case *ast.Query:
		c := *x
		c.Meta = meta
		return &c
	case *ast.Custom:
		c := *x
		c.Meta = meta
		return &c
	}
	return d
}

// copyProps returns a fresh map carrying the same entries as src.
func copyProps(src map[string]ast.MetaValue) map[string]ast.MetaValue {
	out := make(map[string]ast.MetaValue, len(src)+1)
	for k, v := range src {
		out[k] = v
	}
	return out
}

// directiveSpan picks the most specific span available for d, falling back
// to plug when d carries no Span of its own.
func directiveSpan(d ast.Directive, plug *ast.Plugin) ast.Span {
	if s := d.DirSpan(); s != (ast.Span{}) {
		return s
	}
	return spanOf(plug)
}

func spanOf(p *ast.Plugin) ast.Span {
	if p == nil {
		return ast.Span{}
	}
	return p.Span
}
