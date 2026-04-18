// Package printer writes beancount AST nodes as formatted text.
package printer

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/internal/formatopt"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/format"
)

// Fprint writes the beancount text representation of the given value to w.
// The value can be ast.Directive (any concrete type), ast.File, ast.Ledger,
// []ast.Directive, or ast.Amount.
func Fprint(w io.Writer, node any, opts ...format.Option) error {
	o := formatopt.Resolve(opts)
	p := &printer{w: w, opts: o}

	switch v := node.(type) {
	case *ast.File:
		p.printDirectives(v.Directives)
	case ast.File:
		p.printDirectives(v.Directives)
	case *ast.Ledger:
		p.printLedger(v)
	case ast.Ledger:
		p.printLedger(&v)
	case []ast.Directive:
		p.printDirectives(v)
	case *ast.Amount:
		if v != nil {
			p.printAmount(*v)
		}
	case ast.Amount:
		p.printAmount(v)
	default:
		// Try as a single directive.
		if d, ok := node.(ast.Directive); ok {
			p.printDirective(d)
		} else {
			return fmt.Errorf("printer: unsupported type %T", node)
		}
	}

	return p.err
}

type printer struct {
	w    io.Writer
	opts formatopt.Options
	err  error
}

func (p *printer) write(s string) {
	if p.err != nil {
		return
	}
	_, p.err = io.WriteString(p.w, s)
}

func (p *printer) printf(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, args...)
}

func (p *printer) printDirectives(dirs []ast.Directive) {
	for i, d := range dirs {
		if i > 0 && p.opts.InsertBlankLinesBetweenDirectives {
			for range p.opts.BlankLinesBetweenDirectives {
				p.write("\n")
			}
		}
		p.printDirective(d)
	}
}

// printLedger walks a Ledger in its canonical chronological order using
// Ledger.All, which avoids materializing the directive slice.
func (p *printer) printLedger(l *ast.Ledger) {
	for i, d := range l.All() {
		if i > 0 && p.opts.InsertBlankLinesBetweenDirectives {
			for range p.opts.BlankLinesBetweenDirectives {
				p.write("\n")
			}
		}
		p.printDirective(d)
	}
}

func (p *printer) printDirective(d ast.Directive) {
	switch v := d.(type) {
	case *ast.Option:
		p.printOption(v)
	case *ast.Plugin:
		p.printPlugin(v)
	case *ast.Include:
		p.printInclude(v)
	case *ast.Open:
		p.printOpen(v)
	case *ast.Close:
		p.printClose(v)
	case *ast.Commodity:
		p.printCommodity(v)
	case *ast.Balance:
		p.printBalance(v)
	case *ast.Pad:
		p.printPad(v)
	case *ast.Note:
		p.printNote(v)
	case *ast.Document:
		p.printDocument(v)
	case *ast.Event:
		p.printEvent(v)
	case *ast.Query:
		p.printQuery(v)
	case *ast.Price:
		p.printPrice(v)
	case *ast.Custom:
		p.printCustom(v)
	case *ast.Transaction:
		p.printTransaction(v)
	}
}

func (p *printer) printOption(o *ast.Option) {
	p.printf("option %s %s\n", beancountQuote(o.Key), beancountQuote(o.Value))
}

func (p *printer) printPlugin(pl *ast.Plugin) {
	if pl.Config != "" {
		p.printf("plugin %s %s\n", beancountQuote(pl.Name), beancountQuote(pl.Config))
	} else {
		p.printf("plugin %s\n", beancountQuote(pl.Name))
	}
}

func (p *printer) printInclude(inc *ast.Include) {
	p.printf("include %s\n", beancountQuote(inc.Path))
}

func (p *printer) indent() string {
	return strings.Repeat(" ", p.opts.IndentWidth)
}

func (p *printer) doubleIndent() string {
	return strings.Repeat(" ", p.opts.IndentWidth*2)
}

func (p *printer) printOpen(o *ast.Open) {
	p.printf("%s open %s", o.Date.Format("2006-01-02"), o.Account)
	if len(o.Currencies) > 0 {
		p.printf(" %s", strings.Join(o.Currencies, ","))
	}
	if o.Booking != ast.BookingDefault {
		p.printf(" %s", beancountQuote(o.Booking.String()))
	}
	p.write("\n")
	p.printMetadata(o.Meta, p.indent())
}

func (p *printer) printClose(c *ast.Close) {
	p.printf("%s close %s\n", c.Date.Format("2006-01-02"), c.Account)
	p.printMetadata(c.Meta, p.indent())
}

func (p *printer) printCommodity(c *ast.Commodity) {
	p.printf("%s commodity %s\n", c.Date.Format("2006-01-02"), c.Currency)
	p.printMetadata(c.Meta, p.indent())
}

func (p *printer) printBalance(b *ast.Balance) {
	p.printf("%s balance %s %s", b.Date.Format("2006-01-02"), b.Account, p.formatDecimal(&b.Amount.Number))
	if b.Tolerance != nil {
		p.printf(" ~ %s", p.formatDecimal(b.Tolerance))
	}
	p.printf(" %s\n", b.Amount.Currency)
	p.printMetadata(b.Meta, p.indent())
}

func (p *printer) printPad(pd *ast.Pad) {
	p.printf("%s pad %s %s\n", pd.Date.Format("2006-01-02"), pd.Account, pd.PadAccount)
	p.printMetadata(pd.Meta, p.indent())
}

func (p *printer) printNote(n *ast.Note) {
	p.printf("%s note %s %s\n", n.Date.Format("2006-01-02"), n.Account, beancountQuote(n.Comment))
	p.printMetadata(n.Meta, p.indent())
}

func (p *printer) printDocument(d *ast.Document) {
	p.printf("%s document %s %s\n", d.Date.Format("2006-01-02"), d.Account, beancountQuote(d.Path))
	p.printMetadata(d.Meta, p.indent())
}

func (p *printer) printEvent(e *ast.Event) {
	p.printf("%s event %s %s\n", e.Date.Format("2006-01-02"), beancountQuote(e.Name), beancountQuote(e.Value))
	p.printMetadata(e.Meta, p.indent())
}

func (p *printer) printQuery(q *ast.Query) {
	p.printf("%s query %s %s\n", q.Date.Format("2006-01-02"), beancountQuote(q.Name), beancountQuote(q.BQL))
	p.printMetadata(q.Meta, p.indent())
}

func (p *printer) printPrice(pr *ast.Price) {
	p.printf("%s price %s %s\n", pr.Date.Format("2006-01-02"), pr.Commodity, p.formatAmount(pr.Amount))
	p.printMetadata(pr.Meta, p.indent())
}

func (p *printer) printCustom(c *ast.Custom) {
	p.printf("%s custom %s", c.Date.Format("2006-01-02"), beancountQuote(c.TypeName))
	for _, v := range c.Values {
		p.printf(" %s", p.formatMetaValue(v))
	}
	p.write("\n")
	p.printMetadata(c.Meta, p.indent())
}

func (p *printer) printTransaction(t *ast.Transaction) {
	p.printf("%s %s", t.Date.Format("2006-01-02"), string(rune(t.Flag)))
	if t.Payee != "" {
		p.printf(" %s %s", beancountQuote(t.Payee), beancountQuote(t.Narration))
	} else if t.Narration != "" {
		p.printf(" %s", beancountQuote(t.Narration))
	}
	for _, tag := range t.Tags {
		p.printf(" #%s", tag)
	}
	for _, link := range t.Links {
		p.printf(" ^%s", link)
	}
	p.write("\n")
	p.printMetadata(t.Meta, p.indent())

	for _, posting := range t.Postings {
		p.printPosting(posting, t)
	}
}

func (p *printer) printPosting(posting ast.Posting, txn *ast.Transaction) {
	indent := p.indent()

	// Build the account prefix part.
	var prefix strings.Builder
	prefix.WriteString(indent)
	if posting.Flag != 0 {
		prefix.WriteByte(posting.Flag)
		prefix.WriteByte(' ')
	}
	prefix.WriteString(string(posting.Account))

	if posting.Amount == nil {
		// Auto-balanced posting, just account.
		p.write(prefix.String())
		p.write("\n")
		p.printMetadata(posting.Meta, p.doubleIndent())
		return
	}

	// Build the amount + cost + price suffix.
	var suffix strings.Builder
	suffix.WriteString(p.formatAmount(*posting.Amount))
	if posting.Cost != nil {
		suffix.WriteByte(' ')
		suffix.WriteString(p.formatCostSpec(*posting.Cost))
	}
	if posting.Price != nil {
		suffix.WriteByte(' ')
		suffix.WriteString(p.formatPriceAnnotation(*posting.Price))
	}

	if p.opts.AlignAmounts {
		// Calculate padding so the currency of the direct amount ends at AmountColumn.
		prefixWidth := formatopt.StringWidth(prefix.String(), p.opts.EastAsianAmbiguousWidth)
		// The "amount text" for alignment is just "number currency" (the direct amount),
		// not the cost/price suffixes.
		amtText := p.formatAmount(*posting.Amount)
		amtWidth := formatopt.StringWidth(amtText, p.opts.EastAsianAmbiguousWidth)

		totalUsed := prefixWidth + amtWidth
		padding := p.opts.AmountColumn - totalUsed
		if padding < 2 {
			padding = 2
		}

		p.write(prefix.String())
		p.write(strings.Repeat(" ", padding))
		p.write(suffix.String())
	} else {
		p.write(prefix.String())
		p.write("  ")
		p.write(suffix.String())
	}
	p.write("\n")
	p.printMetadata(posting.Meta, p.doubleIndent())
}

func (p *printer) printAmount(a ast.Amount) {
	p.write(p.formatAmount(a))
}

func (p *printer) formatAmount(a ast.Amount) string {
	num := p.formatDecimal(&a.Number)
	return num + " " + a.Currency
}

func (p *printer) formatDecimal(d *apd.Decimal) string {
	s := d.Text('f')
	if p.opts.CommaGrouping {
		s = formatopt.InsertCommas(s)
	}
	return s
}

// formatCostSpec renders a CostSpec back to source form.
//
// Brace selection: "{{...}}" is used iff the cost has a Total component and
// no PerUnit (the legacy total-only form). Every other case—including a
// completely empty CostSpec and the combined "{X # Y CUR}" form—uses single
// braces. As a consequence an empty CostSpec is normalized to "{}", so a
// source "{{}}" parses, lowers, and re-prints as "{}".
//
// Combined form: when both PerUnit and Total are present, the rendering is
// "perUnit # total". When the per-unit and total currencies match (the common
// case after lowering, which guarantees they do), the per-unit currency is
// suppressed so the output mirrors typical beancount source like
// "{502.12 # 9.95 USD}". If the currencies happen to differ — a defensive
// branch that the lowerer rejects — both currencies are emitted explicitly
// rather than panicking.
func (p *printer) formatCostSpec(cs ast.CostSpec) string {
	totalOnly := cs.Total != nil && cs.PerUnit == nil
	openBrace, closeBrace := "{", "}"
	if totalOnly {
		openBrace, closeBrace = "{{", "}}"
	}

	var parts []string
	switch {
	case cs.PerUnit != nil && cs.Total != nil:
		var leading string
		if cs.PerUnit.Currency == cs.Total.Currency {
			// Suppress the per-unit currency to match typical source form.
			leading = p.formatDecimal(&cs.PerUnit.Number) + " # " + p.formatAmount(*cs.Total)
		} else {
			// The lowerer from source rejects mismatched currencies, but a
			// directly-constructed AST may reach this branch; emit both
			// currencies explicitly rather than panicking.
			leading = p.formatAmount(*cs.PerUnit) + " # " + p.formatAmount(*cs.Total)
		}
		parts = append(parts, leading)
	case cs.PerUnit != nil:
		parts = append(parts, p.formatAmount(*cs.PerUnit))
	case cs.Total != nil:
		parts = append(parts, p.formatAmount(*cs.Total))
	}
	if cs.Date != nil {
		parts = append(parts, cs.Date.Format("2006-01-02"))
	}
	if cs.Label != "" {
		parts = append(parts, beancountQuote(cs.Label))
	}

	return openBrace + strings.Join(parts, ", ") + closeBrace
}

func (p *printer) formatPriceAnnotation(pa ast.PriceAnnotation) string {
	op := "@"
	if pa.IsTotal {
		op = "@@"
	}
	return op + " " + p.formatAmount(pa.Amount)
}

func (p *printer) formatMetaValue(v ast.MetaValue) string {
	switch v.Kind {
	case ast.MetaString:
		return beancountQuote(v.String)
	case ast.MetaAccount:
		return v.String
	case ast.MetaCurrency:
		return v.String
	case ast.MetaDate:
		return v.Date.Format("2006-01-02")
	case ast.MetaTag:
		return "#" + v.String
	case ast.MetaLink:
		return "^" + v.String
	case ast.MetaNumber:
		return p.formatDecimal(&v.Number)
	case ast.MetaAmount:
		return p.formatAmount(v.Amount)
	case ast.MetaBool:
		if v.Bool {
			return "TRUE"
		}
		return "FALSE"
	default:
		return ""
	}
}

// beancountQuote wraps s in double quotes, escaping only backslashes and
// double quotes. Unlike strconv.Quote, it preserves literal newlines, tabs,
// and all other characters as-is, matching beancount's string syntax.
// The byte-by-byte loop is safe because the only escaped bytes (0x5C and 0x22)
// never appear as continuation bytes in valid UTF-8.
func beancountQuote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (p *printer) printMetadata(meta ast.Metadata, indent string) {
	if len(meta.Props) == 0 {
		return
	}
	keys := make([]string, 0, len(meta.Props))
	for k := range meta.Props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := meta.Props[k]
		p.printf("%s%s: %s\n", indent, k, p.formatMetaValue(v))
	}
}
