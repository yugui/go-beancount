package main

import (
	"fmt"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/inventory/inspect"
)

// hoverInventory renders the inventory effect of the directive at offset in
// filename, or "" when the directive under the cursor has no effect to show.
func (s *Server) hoverInventory(ledger *ast.Ledger, filename string, offset int) string {
	view, err := inspect.Resolve(ledger, inspect.Target{Filename: filename, Offset: offset})
	if err != nil {
		s.logger.Printf("handleHover: inspect: %v", err)
		return ""
	}
	return renderInventoryView(view)
}

func renderInventoryView(view inspect.View) string {
	switch view.Kind {
	case inspect.KindTransaction:
		return renderTxnEffect(view)
	case inspect.KindBalance:
		return renderBalanceEffect(view)
	case inspect.KindClose:
		return renderCloseEffect(view)
	default:
		return ""
	}
}

func renderTxnEffect(view inspect.View) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Inventory effect** (%s)\n", view.Date.Format("2006-01-02"))
	if len(view.Booked) > 0 {
		sb.WriteString("\n*Booked postings*\n")
		for _, b := range view.Booked {
			fmt.Fprintf(&sb, "- %s\n", formatBookedPosting(b))
		}
	}
	currencies := bookedCurrencies(view.Booked)
	for _, av := range view.Accounts {
		fmt.Fprintf(&sb, "\n*%s*\n", av.Account)
		fmt.Fprintf(&sb, "- before: %s\n", joinPositions(av.Before, currencies))
		fmt.Fprintf(&sb, "- after: %s\n", joinPositions(av.After, currencies))
	}
	return sb.String()
}

// bookedCurrencies is the set of unit currencies the transaction actually
// moved, taken from its booked postings (auto-balanced legs included). A nil
// result means "do not filter": it falls back to showing every lot rather than
// hiding everything when no currency is known.
func bookedCurrencies(booked []inventory.BookedPosting) map[string]bool {
	set := map[string]bool{}
	for _, b := range booked {
		if b.Units.Currency != "" {
			set[b.Units.Currency] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func renderBalanceEffect(view inspect.View) string {
	if len(view.Accounts) == 0 {
		return ""
	}
	av := view.Accounts[0]
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Balance assertion** (%s)\n", view.Date.Format("2006-01-02"))
	fmt.Fprintf(&sb, "\n*%s*\n", av.Account)
	fmt.Fprintf(&sb, "- actual: %s\n", actualLine(av.After))
	if view.Asserted != nil {
		fmt.Fprintf(&sb, "- asserted: %s %s\n", view.Asserted.Number.Text('f'), view.Asserted.Currency)
	}
	return sb.String()
}

func renderCloseEffect(view inspect.View) string {
	if len(view.Accounts) == 0 {
		return ""
	}
	av := view.Accounts[0]
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Account close** (%s)\n", view.Date.Format("2006-01-02"))
	fmt.Fprintf(&sb, "\n*%s*\n", av.Account)
	fmt.Fprintf(&sb, "- final: %s\n", actualLine(av.After))
	return sb.String()
}

// actualLine renders an account's state for an assertion, distinguishing a nil
// inventory (no qualifying transaction) from a seen-but-empty one.
func actualLine(inv *inventory.Inventory) string {
	if inv == nil {
		return "(no prior transaction)"
	}
	return joinPositions(inv, nil)
}

// joinPositions renders an inventory's positions on one line, or "(empty)" when
// it is nil, holds nothing, or holds nothing in currencies. A nil currencies
// keeps every position; otherwise only positions whose unit currency is in the
// set are shown.
func joinPositions(inv *inventory.Inventory, currencies map[string]bool) string {
	if inv == nil || inv.IsEmpty() {
		return "(empty)"
	}
	var parts []string
	for p := range inv.All() {
		if currencies != nil && !currencies[p.Units.Currency] {
			continue
		}
		parts = append(parts, formatPosition(p))
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, ", ")
}

func formatPosition(p inventory.Position) string {
	s := fmt.Sprintf("%s %s", p.Units.Number.Text('f'), p.Units.Currency)
	if p.Cost != nil {
		s += " {" + formatLot(*p.Cost) + "}"
	}
	return s
}

func formatLot(l inventory.Lot) string {
	parts := []string{fmt.Sprintf("%s %s", l.Number.Text('f'), l.Currency)}
	if !l.Date.IsZero() {
		parts = append(parts, l.Date.Format("2006-01-02"))
	}
	if l.Label != "" {
		parts = append(parts, fmt.Sprintf("%q", l.Label))
	}
	return strings.Join(parts, ", ")
}

// formatBookedPosting renders a single booked posting: account, the signed
// units routed through inventory, the augmented lot or the reduced lot (with
// sale price and realized gain when present), and an "(auto)" marker for an
// amount the booking pass inferred from the transaction residual.
func formatBookedPosting(b inventory.BookedPosting) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s: %s %s", b.Account, b.Units.Number.Text('f'), b.Units.Currency)
	switch {
	case b.Lot != nil:
		sb.WriteString(" {" + formatLot(*b.Lot) + "}")
	case b.Reduction != nil && b.Reduction.Lot.Currency != "":
		sb.WriteString(" {" + formatLot(b.Reduction.Lot) + "}")
	}
	if b.Reduction != nil && b.Reduction.SalePricePer != nil {
		fmt.Fprintf(&sb, " @ %s %s", b.Reduction.SalePricePer.Text('f'), b.Reduction.GainCurrency)
	}
	if b.Reduction != nil && b.Reduction.RealizedGain != nil {
		fmt.Fprintf(&sb, " (realized gain %s %s)", b.Reduction.RealizedGain.Text('f'), b.Reduction.GainCurrency)
	}
	if b.InferredAuto {
		sb.WriteString(" (auto)")
	}
	return sb.String()
}
