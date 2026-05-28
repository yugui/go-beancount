package ast

import (
	"testing"
	"time"
)

// directiveAccessorCase describes one directive type's expected DirMeta and
// AccountOf results. There is one entry per concrete Directive type so the
// table doubles as a checklist: a new directive type added without a matching
// entry leaves a coverage gap a reviewer can spot.
type directiveAccessorCase struct {
	name       string
	directive  Directive
	hasMeta    bool   // type carries a Meta field
	account    string // expected AccountOf value
	hasAccount bool   // expected AccountOf ok
}

func directiveAccessorCases(meta Metadata) []directiveAccessorCase {
	date := time.Date(2024, time.March, 15, 0, 0, 0, 0, time.UTC)
	acct := Account("Assets:A")
	amt := Amount{Number: cloneTestDecimal("1"), Currency: "USD"}
	return []directiveAccessorCase{
		// Header directives: no metadata, no account.
		{name: "Option", directive: &Option{Key: "k", Value: "v"}},
		{name: "Plugin", directive: &Plugin{Name: "p"}},
		{name: "Include", directive: &Include{Path: "x.beancount"}},

		// Account-bearing directives.
		{name: "Open", directive: &Open{Date: date, Account: acct, Meta: meta}, hasMeta: true, account: string(acct), hasAccount: true},
		{name: "Close", directive: &Close{Date: date, Account: acct, Meta: meta}, hasMeta: true, account: string(acct), hasAccount: true},
		{name: "Pad", directive: &Pad{Date: date, Account: acct, PadAccount: Account("Equity:Opening"), Meta: meta}, hasMeta: true, account: string(acct), hasAccount: true},
		{name: "Note", directive: &Note{Date: date, Account: acct, Comment: "c", Meta: meta}, hasMeta: true, account: string(acct), hasAccount: true},
		{name: "Document", directive: &Document{Date: date, Account: acct, Path: "/p", Meta: meta}, hasMeta: true, account: string(acct), hasAccount: true},
		{name: "Balance", directive: &Balance{Date: date, Account: acct, Amount: amt, Meta: meta}, hasMeta: true, account: string(acct), hasAccount: true},

		// Metadata-bearing directives without an account.
		{name: "Commodity", directive: &Commodity{Date: date, Currency: "USD", Meta: meta}, hasMeta: true},
		{name: "Price", directive: &Price{Date: date, Commodity: "USD", Amount: amt, Meta: meta}, hasMeta: true},
		{name: "Event", directive: &Event{Date: date, Name: "location", Value: "NYC", Meta: meta}, hasMeta: true},
		{name: "Query", directive: &Query{Date: date, Name: "q", BQL: "SELECT *", Meta: meta}, hasMeta: true},
		{name: "Custom", directive: &Custom{Date: date, TypeName: "t", Meta: meta}, hasMeta: true},
		{name: "Transaction", directive: &Transaction{Date: date, Flag: '*', Narration: "n", Meta: meta}, hasMeta: true},
	}
}

func TestDirMeta(t *testing.T) {
	// MetaValue is struct-comparable, so values are compared with ==; a
	// non-numeric value avoids apd.Decimal's unexported fields entirely.
	meta := Metadata{Props: map[string]MetaValue{"key": {Kind: MetaString, String: "val"}}}
	for _, tc := range directiveAccessorCases(meta) {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.directive.DirMeta()
			if !tc.hasMeta {
				if got.Props != nil {
					t.Errorf("DirMeta() on header directive = %+v, want empty Metadata", got)
				}
				return
			}
			if len(got.Props) != len(meta.Props) {
				t.Fatalf("DirMeta().Props = %+v, want %+v", got.Props, meta.Props)
			}
			for k, want := range meta.Props {
				if got.Props[k] != want {
					t.Errorf("DirMeta().Props[%q] = %+v, want %+v", k, got.Props[k], want)
				}
			}
		})
	}
}

func TestAccountOf(t *testing.T) {
	for _, tc := range directiveAccessorCases(Metadata{}) {
		t.Run(tc.name, func(t *testing.T) {
			gotAcct, gotOK := AccountOf(tc.directive)
			if gotOK != tc.hasAccount {
				t.Fatalf("AccountOf() ok = %v, want %v", gotOK, tc.hasAccount)
			}
			if string(gotAcct) != tc.account {
				t.Errorf("AccountOf() account = %q, want %q", gotAcct, tc.account)
			}
		})
	}
}
