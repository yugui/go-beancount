package commoditypattern

import (
	"context"
	"fmt"
	"regexp"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

const (
	codeMismatch      = "commodity-pattern-mismatch"
	codeInvalidRegexp = "commodity-pattern-invalid-regexp"
	metadataKey       = "commodity-pattern"
)

func init() {
	postproc.Register("beansprout.plugins.commodity_pattern", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/sprout/commoditypattern", api.PluginFunc(apply))
}

// patternEntry holds a compiled regexp and its original pattern string for
// error messages.
type patternEntry struct {
	re      *regexp.Regexp
	pattern string
}

// apply validates transaction posting currencies against per-account patterns
// declared in Open directive metadata. See the package godoc for full behavior.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	patterns, invalidDiags := buildPatterns(in)
	if len(invalidDiags) > 0 {
		return api.Result{Diagnostics: invalidDiags}, nil
	}

	if len(patterns) == 0 {
		return api.Result{}, nil
	}

	diags := validateTransactions(in, patterns)
	if len(diags) == 0 {
		return api.Result{}, nil
	}
	return api.Result{Diagnostics: diags}, nil
}

// buildPatterns scans Open directives and compiles commodity-pattern values.
// All invalid-regexp diagnostics are collected so that the user sees every
// bad pattern in one pass. When any diagnostic is present the returned map is
// empty so the caller can skip transaction validation.
func buildPatterns(in api.Input) (map[ast.Account]patternEntry, []ast.Diagnostic) {
	patterns := make(map[ast.Account]patternEntry)
	var diags []ast.Diagnostic

	for _, d := range in.Directives {
		open, ok := d.(*ast.Open)
		if !ok {
			continue
		}
		mv, ok := open.Meta.Props[metadataKey]
		if !ok {
			continue
		}
		patStr := mv.String
		re, err := compileFullMatch(patStr)
		if err != nil {
			span := open.Span
			if span == (ast.Span{}) && in.Directive != nil {
				span = in.Directive.Span
			}
			diags = append(diags, ast.Diagnostic{
				Code:     codeInvalidRegexp,
				Span:     span,
				Severity: ast.Error,
				Message:  fmt.Sprintf("invalid regex pattern %q for account %q: %v", patStr, open.Account, err),
			})
			continue
		}
		patterns[open.Account] = patternEntry{re: re, pattern: patStr}
	}
	if len(diags) > 0 {
		return nil, diags
	}
	return patterns, nil
}

// validateTransactions checks each posting's currency against its account's pattern.
func validateTransactions(in api.Input, patterns map[ast.Account]patternEntry) []ast.Diagnostic {
	var diags []ast.Diagnostic

	for _, d := range in.Directives {
		tx, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		for i := range tx.Postings {
			p := &tx.Postings[i]
			if p.Amount == nil {
				continue
			}
			entry, ok := patterns[p.Account]
			if !ok {
				continue
			}
			if !entry.re.MatchString(p.Amount.Currency) {
				diags = append(diags, ast.Diagnostic{
					Code:     codeMismatch,
					Span:     diagSpan(p, tx, in.Directive),
					Severity: ast.Error,
					Message: fmt.Sprintf(
						"Commodity %q in account %q does not match pattern %q",
						p.Amount.Currency, p.Account, entry.pattern,
					),
				})
			}
		}
	}
	return diags
}

// compileFullMatch wraps pattern so that MatchString performs a full-string
// match, equivalent to Python's re.fullmatch.
func compileFullMatch(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(`\A(?:` + pattern + `)\z`)
}

// diagSpan picks the most specific available span for an offending posting.
func diagSpan(p *ast.Posting, tx *ast.Transaction, plug *ast.Plugin) ast.Span {
	var zero ast.Span
	if p.Span != zero {
		return p.Span
	}
	if tx.Span != zero {
		return tx.Span
	}
	if plug != nil {
		return plug.Span
	}
	return zero
}
