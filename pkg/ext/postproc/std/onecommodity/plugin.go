package onecommodity

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// Diagnostic codes emitted by the plugin. The kebab-case
// "multi-commodity-account" code mirrors the Python plugin's intent —
// upstream's OneCommodityError namedtuple has no machine-readable
// category — and gives downstream tooling a stable string to match on
// without parsing the human-readable message. The "invalid-regexp"
// code reuses the spelling already established by checkcommodity for
// consistency across the std library.
const (
	codeMultiCommodityAccount = "multi-commodity-account"
	codeInvalidRegexp         = "invalid-regexp"
)

// optOutKey is the metadata key on an Open directive that disables the
// check for that account. Upstream spells it "onecommodity" in
// lowercase; the spelling is preserved so existing ledgers carry over
// unchanged. A truthy value is the upstream default, so only an
// explicit FALSE disables the check on a per-account basis.
const optOutKey = "onecommodity"

// Dual registration: upstream's Python module path and this package's
// Go import path. See doc.go for the rationale.
func init() {
	postproc.Register("beancount.plugins.onecommodity", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/onecommodity", api.PluginFunc(apply))
}

// apply emits one diagnostic per account whose unit-currency or
// cost-currency set has more than one element. It is diagnostic-only
// and returns nil Result.Directives. See the package godoc for the
// full behavior, opt-out rules, and upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}

	accountsRe, regexErrs := compileFilter(in.Config, in.Directive)

	if in.Directives == nil {
		if len(regexErrs) == 0 {
			return api.Result{}, nil
		}
		return api.Result{Diagnostics: regexErrs}, nil
	}

	// First pass: locate Open directives so we can read opt-out
	// metadata, the declared currency set, and source spans for
	// diagnostics. Materialize directives into a slice so we can do a
	// second pass without re-iterating the (potentially side-effecting)
	// caller-provided sequence.
	opens := map[ast.Account]*ast.Open{}
	var all []ast.Directive
	for _, d := range in.Directives {
		all = append(all, d)
		if o, ok := d.(*ast.Open); ok {
			// Record the first Open seen for an account; subsequent
			// Opens on the same account are a separate validator's
			// concern.
			if _, exists := opens[o.Account]; !exists {
				opens[o.Account] = o
			}
		}
	}

	// skip is the set of accounts excluded from the check, computed
	// from Open metadata, declared currency sets, and the regex
	// filter. Mirrors upstream's `skip_accounts` set.
	skip := map[ast.Account]struct{}{}
	for acct, o := range opens {
		if !optedIn(o.Meta) {
			skip[acct] = struct{}{}
			continue
		}
		if len(o.Currencies) > 1 {
			// User has explicitly declared a multi-currency account;
			// the plugin defers to that declaration.
			skip[acct] = struct{}{}
			continue
		}
		if accountsRe != nil && !accountsRe.MatchString(string(acct)) {
			skip[acct] = struct{}{}
			continue
		}
	}

	// units and costs accumulate the observed currencies per account.
	// Accounts missing an Open directive are still checked: upstream
	// only filters by Open when an Open exists, so a referenced-only
	// account passes through unless the regex filter excludes it.
	units := map[ast.Account]map[string]struct{}{}
	costs := map[ast.Account]map[string]struct{}{}

	observe := func(set map[ast.Account]map[string]struct{}, acct ast.Account, cur string) {
		if cur == "" {
			return
		}
		if _, opened := opens[acct]; !opened {
			// No Open exists; only the regex filter applies.
			if accountsRe != nil && !accountsRe.MatchString(string(acct)) {
				return
			}
		} else if _, drop := skip[acct]; drop {
			return
		}
		s, ok := set[acct]
		if !ok {
			s = map[string]struct{}{}
			set[acct] = s
		}
		s[cur] = struct{}{}
	}

	for _, d := range all {
		switch x := d.(type) {
		case *ast.Transaction:
			for i := range x.Postings {
				p := &x.Postings[i]
				if p.Amount != nil {
					observe(units, p.Account, p.Amount.Currency)
				}
				if p.Cost != nil {
					if p.Cost.PerUnit != nil {
						observe(costs, p.Account, p.Cost.PerUnit.Currency)
					}
					if p.Cost.Total != nil {
						observe(costs, p.Account, p.Cost.Total.Currency)
					}
				}
			}
		case *ast.Balance:
			observe(units, x.Account, x.Amount.Currency)
		}
	}

	errs := regexErrs
	errs = appendDiags(errs, units, opens, in.Directive, "More than one currency in account")
	errs = appendDiags(errs, costs, opens, in.Directive, "More than one cost currency in account")

	if len(errs) == 0 {
		return api.Result{}, nil
	}
	return api.Result{Diagnostics: errs}, nil
}

// appendDiags emits one diagnostic per account in set whose currency
// collection has more than one element, in stable account-name order.
// The leading clause of the message is provided by the caller so the
// same helper serves both the unit and cost passes.
func appendDiags(
	errs []ast.Diagnostic,
	set map[ast.Account]map[string]struct{},
	opens map[ast.Account]*ast.Open,
	trigger *ast.Plugin,
	prefix string,
) []ast.Diagnostic {
	accts := make([]ast.Account, 0, len(set))
	for a := range set {
		if len(set[a]) <= 1 {
			continue
		}
		accts = append(accts, a)
	}
	sort.Slice(accts, func(i, j int) bool { return accts[i] < accts[j] })

	for _, acct := range accts {
		curs := make([]string, 0, len(set[acct]))
		for c := range set[acct] {
			curs = append(curs, c)
		}
		sort.Strings(curs)
		errs = append(errs, ast.Diagnostic{
			Code:     codeMultiCommodityAccount,
			Span:     diagSpan(opens[acct], trigger),
			Message:  fmt.Sprintf("%s '%s': %s", prefix, acct, strings.Join(curs, ",")),
			Severity: ast.Error,
		})
	}
	return errs
}

// optedIn reports whether the Open directive's metadata leaves the
// account opted in to the check (the upstream default). The opt-out
// triggers on either MetaBool{Bool:false} or MetaString whose value
// case-insensitively equals "false"; any other shape (including a
// missing key) is treated as opted in.
func optedIn(meta ast.Metadata) bool {
	v, ok := meta.Props[optOutKey]
	if !ok {
		return true
	}
	switch v.Kind {
	case ast.MetaBool:
		return v.Bool
	case ast.MetaString:
		return !strings.EqualFold(v.String, "false")
	}
	return true
}

// compileFilter turns the plugin's optional Config string into a
// compiled regular expression anchored at the start of the input,
// mirroring Python's re.match. An invalid expression produces an
// "invalid-regexp" diagnostic and disables the filter for the rest of
// the run, matching upstream's behavior of treating a config error as
// non-fatal — the user gets the bad-config error plus full coverage of
// the ledger.
func compileFilter(cfg string, trigger *ast.Plugin) (*regexp.Regexp, []ast.Diagnostic) {
	if cfg == "" {
		return nil, nil
	}
	re, err := regexp.Compile(`\A(?:` + cfg + `)`)
	if err != nil {
		return nil, []ast.Diagnostic{{
			Code:     codeInvalidRegexp,
			Span:     spanOf(trigger),
			Message:  fmt.Sprintf("invalid onecommodity account regexp %q: %v", cfg, err),
			Severity: ast.Error,
		}}
	}
	return re, nil
}

// diagSpan picks the most actionable span for a diagnostic. The Open
// directive is the place a user would add the opt-out metadata, so we
// prefer it when available. Falling back to the triggering plugin
// directive matches the convention used by checkcommodity and
// leafonly: a span tied to plugin activation is the closest stand-in.
func diagSpan(o *ast.Open, trigger *ast.Plugin) ast.Span {
	if o != nil {
		var zero ast.Span
		if o.Span != zero {
			return o.Span
		}
	}
	return spanOf(trigger)
}

// spanOf returns the span of the triggering plugin directive, or the
// zero span when no trigger was supplied.
func spanOf(p *ast.Plugin) ast.Span {
	if p == nil {
		return ast.Span{}
	}
	return p.Span
}
