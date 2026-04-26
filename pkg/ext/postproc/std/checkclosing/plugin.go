package checkclosing

import (
	"context"
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// closingKey is the posting metadata key that triggers expansion.
// Upstream spells it "closing" in lowercase; we preserve that spelling
// to keep ledgers portable.
const closingKey = "closing"

// oneDay is the offset from transaction.Date to the synthesized
// Balance's date.
const oneDay = 24 * time.Hour

func init() {
	postproc.Register("beancount.plugins.check_closing", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/checkclosing", api.PluginFunc(apply))
}

// apply expands a closing=TRUE posting-metadata entry into a
// zero-balance assertion dated one day after the transaction,
// stripping the metadata key on a cloned posting. See the package
// godoc for accepted metadata forms and upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	var out []ast.Directive
	for _, d := range in.Directives {
		tx, ok := d.(*ast.Transaction)
		if !ok {
			out = append(out, d)
			continue
		}
		// Synthesized balances are emitted before the transaction
		// they came from, matching upstream's source-ordering
		// behavior. Canonical chronological order is restored later
		// by [ast.Ledger.ReplaceAll] when the runner commits the
		// returned slice.
		cloneTx, balances := expand(tx)
		out = append(out, balances...)
		if cloneTx == nil {
			out = append(out, tx)
		} else {
			out = append(out, cloneTx)
		}
	}

	return api.Result{Directives: out}, nil
}

// expand inspects tx for postings with truthy `closing` metadata. The
// cloneTx return holds a freshly cloned *ast.Transaction with the
// `closing` key stripped from each matching posting's metadata; it is
// nil when no posting needed editing, signalling that the caller may
// reuse tx as-is. The balances return holds one synthesized Balance
// directive per expanded posting (nil if none).
func expand(tx *ast.Transaction) (cloneTx *ast.Transaction, balances []ast.Directive) {
	for i := range tx.Postings {
		orig := &tx.Postings[i]
		if !isClosing(orig.Meta) {
			continue
		}
		if orig.Amount == nil {
			// No currency to balance against — leave the posting
			// alone, per the package godoc.
			continue
		}
		if cloneTx == nil {
			cloneTx = clonePostings(tx)
		}
		stripClosing(&cloneTx.Postings[i])

		balances = append(balances, &ast.Balance{
			Date:    tx.Date.Add(oneDay),
			Account: orig.Account,
			Amount:  ast.Amount{Number: zeroDecimal(), Currency: orig.Amount.Currency},
			// Point errors at the transaction's span so the user
			// can locate the source posting. Upstream attaches a
			// fresh "<check_closing>" metadata stub; we reuse the
			// transaction's span, which is more useful.
			Span: tx.Span,
		})
	}
	return cloneTx, balances
}

// isClosing reports whether meta has a truthy `closing` entry. Both
// MetaBool{Bool:true} and MetaString whose value is a
// case-insensitive "true" qualify; see the package godoc.
func isClosing(meta ast.Metadata) bool {
	v, ok := meta.Props[closingKey]
	if !ok {
		return false
	}
	switch v.Kind {
	case ast.MetaBool:
		return v.Bool
	case ast.MetaString:
		return strings.EqualFold(v.String, "true")
	}
	return false
}

// clonePostings returns a shallow copy of tx with a fresh Postings
// slice (new backing array). Posting structs are copied by value; the
// Metadata.Props map on each posting is NOT deep-copied yet — that
// copy is deferred to stripClosing, which only runs on the postings
// that actually need editing.
func clonePostings(tx *ast.Transaction) *ast.Transaction {
	clone := *tx
	clone.Postings = make([]ast.Posting, len(tx.Postings))
	copy(clone.Postings, tx.Postings)
	return &clone
}

// stripClosing removes the `closing` key from p.Meta.Props, working on
// a freshly allocated map so the input metadata is not mutated.
func stripClosing(p *ast.Posting) {
	newProps := make(map[string]ast.MetaValue, len(p.Meta.Props)-1)
	for k, v := range p.Meta.Props {
		if k == closingKey {
			continue
		}
		newProps[k] = v
	}
	p.Meta = ast.Metadata{Props: newProps}
}

// zeroDecimal returns a freshly allocated apd.Decimal representing 0.
func zeroDecimal() apd.Decimal {
	var d apd.Decimal
	d.SetInt64(0)
	return d
}
