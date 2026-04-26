package noduplicates

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// codeDuplicateTransaction is the diagnostic code emitted for a
// Transaction that matches an earlier Transaction under the similarity
// rule documented in the package godoc. Upstream's CompareError
// namedtuple has no machine-readable category; we pick a stable
// kebab-case code so downstream tooling (lsp, log filters) can match
// on it without parsing the human-readable message.
const codeDuplicateTransaction = "duplicate-transaction"

// Dual registration: upstream's Python module path and this package's
// Go import path. See doc.go for the rationale.
func init() {
	postproc.Register("beancount.plugins.noduplicates", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/noduplicates", api.PluginFunc(apply))
}

// apply emits one diagnostic per Transaction whose (Date,
// posting-multiset) similarity key matches an earlier Transaction's.
// It is diagnostic-only and returns nil Result.Directives. See the
// package godoc for the full behavior, the chosen similarity rule, and
// upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// firsts records the set of similarity keys already seen.
	// Subsequent Transactions matching a key in the set are flagged as
	// duplicates of the earlier entry that contributed the key.
	firsts := map[string]struct{}{}

	var errs []api.Error
	for _, d := range in.Directives {
		t, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		k := similarityKey(t)
		if _, seen := firsts[k]; !seen {
			firsts[k] = struct{}{}
			continue
		}
		errs = append(errs, api.Error{
			Code:    codeDuplicateTransaction,
			Span:    diagSpan(t, in.Directive),
			Message: fmt.Sprintf("Duplicate transaction on %s: same postings as earlier entry", t.Date.Format("2006-01-02")),
		})
	}

	if len(errs) == 0 {
		return api.Result{}, nil
	}
	return api.Result{Errors: errs}, nil
}

// similarityKey computes the order-insensitive similarity key for a
// Transaction. The key encodes the calendar date (UTC) and the sorted
// multiset of (Account, Number, Currency) tuples across the
// transaction's postings. Postings with a nil Amount (auto-balanced
// residual postings) contribute under a sentinel marker so two
// transactions that both rely on auto-balancing on the same account
// still match.
//
// The chosen rule deliberately ignores Payee, Narration, Flag, Tags,
// Links, posting Cost, and posting Price. See the package godoc for
// the rationale and the explicit deviation from upstream.
func similarityKey(t *ast.Transaction) string {
	tuples := make([]string, 0, len(t.Postings))
	for i := range t.Postings {
		p := &t.Postings[i]
		if p.Amount == nil {
			// Sentinel marker for an auto-balanced posting: the
			// pipe-separated form keeps the empty number/currency
			// fields unambiguous and distinct from any legal
			// (Account, Number, Currency) tuple, since real
			// currencies cannot contain pipes.
			tuples = append(tuples, string(p.Account)+"||")
			continue
		}
		// apd.Decimal.String() yields a canonical textual form whose
		// equality coincides with Cmp==0 for the values produced by
		// the parser; using the textual form lets us key a map
		// without writing a custom hashable wrapper. Two transactions
		// that both write "1.5" and "1.50" will key the same string
		// only if their textual representations agree, which is a
		// minor stricter-than-Cmp=0 deviation; the importer-double-run
		// bug class always produces byte-identical numbers, so this
		// has not been observed to cause false negatives in practice.
		tuples = append(tuples, fmt.Sprintf("%s|%s|%s", p.Account, p.Amount.Number.String(), p.Amount.Currency))
	}
	sort.Strings(tuples)
	// The leading date segment is followed by a separator that cannot
	// appear in a posting tuple (newline), guaranteeing the key
	// disambiguates date from posting data even when a posting tuple
	// happens to look like a date prefix.
	return t.Date.Format("2006-01-02") + "\n" + strings.Join(tuples, "\n")
}

// diagSpan picks the most actionable span for a diagnostic. The
// duplicate Transaction is where the user fixes the issue (delete it
// or correct the importer that produced it), so we prefer it when its
// Span is non-zero. The triggering plugin directive's Span is the
// fallback, matching the convention used by sibling ports.
func diagSpan(t *ast.Transaction, trigger *ast.Plugin) ast.Span {
	if t != nil {
		var zero ast.Span
		if t.Span != zero {
			return t.Span
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
