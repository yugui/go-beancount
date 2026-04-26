package checkcommodity

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"regexp"
	"sort"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// priceContext is the pseudo-account sentinel used when a currency is
// observed only in a Price directive. Upstream uses the literal
// "Price Directive Context" for the same purpose, and the exact
// spelling is preserved so error messages match upstream verbatim and
// any external log-parsing tooling carries over. The leading capital
// deviates from Go's error-string casing convention when this value
// is embedded in a message; the deviation is intentional, for
// upstream message compatibility.
const priceContext = "Price Directive Context"

// codes emitted by the plugin. These are kept close to upstream's
// CheckCommodityError / ConfigError categorisation without copying
// their freeform message strings verbatim (Go error-code conventions
// prefer lowercase-with-dashes).
const (
	codeMissingCommodity = "missing-commodity"
	codeInvalidConfig    = "invalid-config"
	codeInvalidRegexp    = "invalid-regexp"
)

func init() {
	// Dual registration: upstream's Python module path and this
	// package's Go import path. See doc.go for the rationale.
	postproc.Register("beancount.plugins.check_commodity", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/checkcommodity", api.PluginFunc(apply))
}

// occurrence pairs an account-or-sentinel with a currency. It is the
// bucket key used by the two-pass scan. The scope field holds either
// an [ast.Account] string or the [priceContext] sentinel.
type occurrence struct {
	scope    string
	currency string
}

// apply reports a missing-commodity diagnostic for every currency
// used in a directive without a matching Commodity declaration. See
// the package godoc for the full behavior, JSON configuration format,
// and upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}

	ignoreMap, cfgDiags, fatal := parseConfig(in.Config, in.Directive)
	if fatal {
		return api.Result{Diagnostics: cfgDiags}, nil
	}

	if in.Directives == nil {
		return api.Result{Diagnostics: cfgDiags}, nil
	}

	declared, accountOccs, priceOccs := scan(in.Directives)

	diags := cfgDiags
	issued := map[string]struct{}{}
	ignored := map[string]struct{}{}

	for _, occ := range sortOccurrences(accountOccs) {
		if _, ok := declared[occ.currency]; ok {
			continue
		}
		if _, ok := issued[occ.currency]; ok {
			continue
		}
		if matchesIgnore(ignoreMap, occ.scope, occ.currency) {
			ignored[occ.currency] = struct{}{}
			continue
		}
		diags = append(diags, ast.Diagnostic{
			Code:    codeMissingCommodity,
			Span:    spanOf(in.Directive),
			Message: fmt.Sprintf("missing Commodity directive for %q in %q", occ.currency, occ.scope),
		})
		issued[occ.currency] = struct{}{}
	}

	for _, occ := range sortOccurrences(priceOccs) {
		if _, ok := declared[occ.currency]; ok {
			continue
		}
		if _, ok := issued[occ.currency]; ok {
			continue
		}
		if _, ok := ignored[occ.currency]; ok {
			continue
		}
		diags = append(diags, ast.Diagnostic{
			Code:    codeMissingCommodity,
			Span:    spanOf(in.Directive),
			Message: fmt.Sprintf("missing Commodity directive for %q in %q", occ.currency, occ.scope),
		})
		issued[occ.currency] = struct{}{}
	}

	return api.Result{Diagnostics: diags}, nil
}

// scan walks the directive sequence once, collecting declared
// commodities and (account, currency) / (priceContext, currency)
// occurrences.
func scan(seq iter.Seq2[int, ast.Directive]) (declared map[string]struct{}, accountOccs, priceOccs map[occurrence]struct{}) {
	declared = map[string]struct{}{}
	accountOccs = map[occurrence]struct{}{}
	priceOccs = map[occurrence]struct{}{}

	for _, d := range seq {
		switch x := d.(type) {
		case *ast.Commodity:
			declared[x.Currency] = struct{}{}
		case *ast.Open:
			for _, cur := range x.Currencies {
				accountOccs[occurrence{string(x.Account), cur}] = struct{}{}
			}
		case *ast.Transaction:
			for i := range x.Postings {
				p := &x.Postings[i]
				if p.Amount != nil {
					accountOccs[occurrence{string(p.Account), p.Amount.Currency}] = struct{}{}
				}
				if p.Cost != nil {
					if p.Cost.PerUnit != nil {
						accountOccs[occurrence{string(p.Account), p.Cost.PerUnit.Currency}] = struct{}{}
					}
					if p.Cost.Total != nil {
						accountOccs[occurrence{string(p.Account), p.Cost.Total.Currency}] = struct{}{}
					}
				}
				if p.Price != nil {
					accountOccs[occurrence{string(p.Account), p.Price.Amount.Currency}] = struct{}{}
				}
			}
		case *ast.Balance:
			accountOccs[occurrence{string(x.Account), x.Amount.Currency}] = struct{}{}
		case *ast.Price:
			priceOccs[occurrence{priceContext, x.Commodity}] = struct{}{}
			priceOccs[occurrence{priceContext, x.Amount.Currency}] = struct{}{}
		}
	}
	return declared, accountOccs, priceOccs
}

// sortOccurrences returns occs in (scope, currency) lexicographic
// order so diagnostics are deterministic.
func sortOccurrences(occs map[occurrence]struct{}) []occurrence {
	out := make([]occurrence, 0, len(occs))
	for o := range occs {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].scope != out[j].scope {
			return out[i].scope < out[j].scope
		}
		return out[i].currency < out[j].currency
	})
	return out
}

// ignorePair holds a compiled (scope, currency) regex pair from the
// JSON ignore-map. The scope regex matches against [occurrence.scope]
// — almost always an account name; user-facing diagnostics call this
// the "account regexp", matching upstream's terminology.
type ignorePair struct {
	scope    *regexp.Regexp
	currency *regexp.Regexp
}

// parseConfig decodes the plugin's JSON config string into a slice of
// compiled regex pairs. Upstream accepts a Python dict; we accept JSON
// (see package godoc). The fatal bool signals that the configuration
// was malformed enough that the plugin should not proceed.
func parseConfig(cfg string, trigger *ast.Plugin) (pairs []ignorePair, diags []ast.Diagnostic, fatal bool) {
	if cfg == "" {
		return nil, nil, false
	}

	var raw map[string]string
	if err := json.Unmarshal([]byte(cfg), &raw); err != nil {
		diags = append(diags, ast.Diagnostic{
			Code:    codeInvalidConfig,
			Span:    spanOf(trigger),
			Message: fmt.Sprintf("invalid check_commodity config: %v", err),
		})
		return nil, diags, true
	}

	// Deterministic order so diagnostics are stable.
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := raw[k]
		accRe, accErr := compileAnchored(k)
		curRe, curErr := compileAnchored(v)
		if accErr != nil {
			diags = append(diags, ast.Diagnostic{
				Code:    codeInvalidRegexp,
				Span:    spanOf(trigger),
				Message: fmt.Sprintf("invalid account regexp %q: %v", k, accErr),
			})
		}
		if curErr != nil {
			diags = append(diags, ast.Diagnostic{
				Code:    codeInvalidRegexp,
				Span:    spanOf(trigger),
				Message: fmt.Sprintf("invalid currency regexp %q: %v", v, curErr),
			})
		}
		if accErr != nil || curErr != nil {
			continue
		}
		pairs = append(pairs, ignorePair{scope: accRe, currency: curRe})
	}
	return pairs, diags, false
}

// compileAnchored compiles pat as a regex anchored at the start of the
// input, mirroring Python's re.match. The pattern is wrapped in a
// non-capturing group so alternation inside pat can't escape the
// anchor.
func compileAnchored(pat string) (*regexp.Regexp, error) {
	return regexp.Compile(`\A(?:` + pat + `)`)
}

// matchesIgnore reports whether any compiled pair matches the given
// occurrence. The scope parameter mirrors the occurrence.scope field
// so call sites can pass that value directly without a conversion.
func matchesIgnore(pairs []ignorePair, scope, currency string) bool {
	for _, p := range pairs {
		if p.scope.MatchString(scope) && p.currency.MatchString(currency) {
			return true
		}
	}
	return false
}

// spanOf returns the span of the triggering *ast.Plugin directive, or
// the zero span if unavailable. Errors reported by this plugin carry no
// intrinsic source location — the natural anchor is the plugin line
// that activated them.
func spanOf(p *ast.Plugin) ast.Span {
	if p == nil {
		return ast.Span{}
	}
	return p.Span
}
