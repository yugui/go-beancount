package checkmetadata

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

const (
	codeMissing       = "check-metadata-missing"
	codeInvalidConfig = "check-metadata-invalid-config"
)

var validDirectives = map[string]struct{}{
	"open":      {},
	"close":     {},
	"balance":   {},
	"note":      {},
	"document":  {},
	"commodity": {},
}

var leafOnlyDirectives = map[string]struct{}{
	"open":     {},
	"close":    {},
	"balance":  {},
	"note":     {},
	"document": {},
}

func init() {
	postproc.Register("beansprout.plugins.check_metadata", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/sprout/checkmetadata", api.PluginFunc(apply))
}

func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}

	directiveName, accountPrefix, metaKeys := parseConfig(in.Config)
	if directiveName == "" || len(metaKeys) == 0 {
		return api.Result{}, nil
	}

	if _, ok := validDirectives[directiveName]; !ok {
		valid := sortedKeys(validDirectives)
		return api.Result{Diagnostics: []ast.Diagnostic{{
			Code:    codeInvalidConfig,
			Span:    spanOf(in.Directive),
			Message: fmt.Sprintf("unknown directive type: %q; valid types: %s", directiveName, strings.Join(valid, ", ")),
		}}}, nil
	}

	if in.Directives == nil {
		return api.Result{}, nil
	}

	// Materialize directives once so we can make two passes (leaf set + check).
	var dirs []ast.Directive
	for _, d := range in.Directives {
		dirs = append(dirs, d)
	}

	var leafAccounts map[ast.Account]struct{}
	if _, needsLeaf := leafOnlyDirectives[directiveName]; needsLeaf {
		leafAccounts = buildLeafSet(dirs, accountPrefix)
	}

	var diags []ast.Diagnostic
	for _, d := range dirs {
		diags = append(diags, checkDirective(d, directiveName, metaKeys, leafAccounts)...)
	}

	return api.Result{Diagnostics: diags}, nil
}

// parseConfig extracts directive name, optional account prefix, and required
// metadata key names from the multi-line config string. Blank lines are
// ignored. The first non-blank line is split on whitespace (matching Python's
// str.split(None, 1)) into directive name and optional account prefix; each
// subsequent non-blank line is one required metadata key.
func parseConfig(config string) (directiveName, accountPrefix string, metaKeys []string) {
	lines := nonEmpty(config)
	if len(lines) == 0 {
		return "", "", nil
	}

	parts := strings.Fields(lines[0])
	directiveName = strings.ToLower(parts[0])
	if len(parts) >= 2 {
		accountPrefix = strings.Join(parts[1:], " ")
	}

	metaKeys = append(metaKeys, lines[1:]...)
	return directiveName, accountPrefix, metaKeys
}

// nonEmpty returns the stripped non-blank lines of config.
func nonEmpty(config string) []string {
	var out []string
	for _, raw := range strings.Split(strings.TrimSpace(config), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// buildLeafSet returns the set of leaf accounts found in dirs. An account is a
// leaf when no other account in dirs has it as a strict ancestor. When
// accountPrefix is non-empty, only accounts equal to or strictly under that
// prefix are included in the returned set.
func buildLeafSet(dirs []ast.Directive, accountPrefix string) map[ast.Account]struct{} {
	referenced := make(map[ast.Account]struct{})
	for _, d := range dirs {
		// Pad is intentionally excluded from leaf scoping here.
		if _, isPad := d.(*ast.Pad); isPad {
			continue
		}
		if acct, ok := ast.AccountOf(d); ok {
			referenced[acct] = struct{}{}
		}
	}

	nonLeaf := make(map[ast.Account]struct{})
	for acct := range referenced {
		for parent := acct.Parent(); parent != ""; parent = parent.Parent() {
			if _, ok := referenced[parent]; ok {
				nonLeaf[parent] = struct{}{}
			}
		}
	}

	leaves := make(map[ast.Account]struct{})
	for acct := range referenced {
		if _, bad := nonLeaf[acct]; bad {
			continue
		}
		if accountPrefix != "" {
			pfx := ast.Account(accountPrefix)
			if acct != pfx && !strings.HasPrefix(string(acct), string(pfx)+":") {
				continue
			}
		}
		leaves[acct] = struct{}{}
	}
	return leaves
}

// checkDirective returns diagnostics for d if it matches directiveName and is
// missing required metadata keys. leafAccounts is nil for commodity directives
// (no leaf scoping).
func checkDirective(
	d ast.Directive,
	directiveName string,
	metaKeys []string,
	leafAccounts map[ast.Account]struct{},
) []ast.Diagnostic {
	if directiveName == "commodity" {
		x, ok := d.(*ast.Commodity)
		if !ok {
			return nil
		}
		return missingDiags(x.Meta, x.Span, metaKeys, "Commodity", "", x.Currency)
	}

	// Account-bearing directives: extract meta, span, and account, then apply
	// leaf filter.
	var dirMeta ast.Metadata
	var dirSpan ast.Span
	var account ast.Account

	switch directiveName {
	case "open":
		x, ok := d.(*ast.Open)
		if !ok {
			return nil
		}
		dirMeta, dirSpan, account = x.Meta, x.Span, x.Account
	case "close":
		x, ok := d.(*ast.Close)
		if !ok {
			return nil
		}
		dirMeta, dirSpan, account = x.Meta, x.Span, x.Account
	case "balance":
		x, ok := d.(*ast.Balance)
		if !ok {
			return nil
		}
		dirMeta, dirSpan, account = x.Meta, x.Span, x.Account
	case "note":
		x, ok := d.(*ast.Note)
		if !ok {
			return nil
		}
		dirMeta, dirSpan, account = x.Meta, x.Span, x.Account
	case "document":
		x, ok := d.(*ast.Document)
		if !ok {
			return nil
		}
		dirMeta, dirSpan, account = x.Meta, x.Span, x.Account
	default:
		return nil
	}

	if _, leaf := leafAccounts[account]; !leaf {
		return nil
	}
	label := strings.ToUpper(directiveName[:1]) + directiveName[1:]
	return missingDiags(dirMeta, dirSpan, metaKeys, label, string(account), "")
}

// missingDiags returns one diagnostic listing all missing keys, or nil when
// all required keys are present. Missing keys are sorted for stable output.
func missingDiags(meta ast.Metadata, span ast.Span, required []string, directive, account, currency string) []ast.Diagnostic {
	var missing []string
	for _, key := range required {
		if _, ok := meta.Props[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	missingStr := strings.Join(missing, ", ")

	var msg string
	if directive == "Commodity" {
		msg = fmt.Sprintf("commodity %q is missing required metadata: %s", currency, missingStr)
	} else {
		msg = fmt.Sprintf("%s directive for account %q is missing required metadata: %s",
			directive, account, missingStr)
	}

	return []ast.Diagnostic{{
		Code:     codeMissing,
		Span:     span,
		Message:  msg,
		Severity: ast.Error,
	}}
}

func spanOf(p *ast.Plugin) ast.Span {
	if p == nil {
		return ast.Span{}
	}
	return p.Span
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
