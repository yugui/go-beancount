package main

import (
	"testing"
)

func TestClassifyContext(t *testing.T) {
	tests := []struct {
		linePrefix string
		want       ContextKind
	}{
		// Empty / whitespace-only → top-level keyword position
		{"", ContextKeyword},
		{"   ", ContextKeyword},
		{"\t", ContextKeyword},

		// Date-first lines: empty afterDate and partial directive keywords both
		// classify as ContextKeyword so the editor's word-boundary auto-trigger
		// can prefix-filter against the directive list.
		{"2024-01-01 ", ContextKeyword},
		{"2023-12-31 o", ContextKeyword},
		{"2023-12-31 op", ContextKeyword},
		{"2023-12-31 ope", ContextKeyword},
		// Lowercase tokens that are neither flags nor keyword-prefixes still
		// classify as Keyword; the editor's prefix filter renders them empty,
		// which is preferable to the prior "no completion at all" failure.
		{"2023-12-31 zzz", ContextKeyword},
		// Uppercase or digit-leading tokens take other paths (currency / unknown).
		{"2023-12-31 OPEN", ContextCurrency},
		{"2023-12-31 9", ContextUnknown},

		// Account token after date + open/close → ContextAccount
		{"2024-01-01 open Assets:Bank", ContextAccount},
		{"2024-01-01 open Assets:資産", ContextAccount},
		{"2024-01-01 open Assets:2024", ContextAccount},
		{"2024-01-01 close Assets:Bank", ContextAccount},

		// Currency token after date + open (pure uppercase comes first in open/close) → ContextCurrency
		{"2024-01-01 open Assets:Bank USD", ContextCurrency},

		// Partially typed account after open (no colon yet) → ContextAccount.
		// The per-directive arg-kind table places ContextAccount at open's
		// first positional argument, so even a colon-less prefix surfaces
		// account candidates instead of being misread as a currency.
		{"2024-01-01 open Asset", ContextAccount},
		{"2024-01-01 open A", ContextAccount},

		// Currency after balance/price (3rd positional arg) → ContextCurrency
		{"2024-01-01 balance Assets:Bank 100 US", ContextCurrency},
		{"2024-01-01 price USD 1.0 EU", ContextCurrency},

		// First positional arg of balance/pad/note/document is an account.
		// Colon-less single-letter prefixes must still classify as Account so
		// that account candidates are offered, matching the open/close case.
		{"2024-01-01 balance A", ContextAccount},
		{"2024-01-01 balance Assets:A", ContextAccount},
		{"2024-01-01 pad A Equity:B", ContextAccount},
		{"2024-01-01 pad Assets:A E", ContextAccount},
		{"2024-01-01 note Assets:A", ContextAccount},
		{"2024-01-01 document Assets:A", ContextAccount},

		// commodity / price first argument is a currency.
		{"2024-01-01 commodity U", ContextCurrency},
		{"2024-01-01 price U", ContextCurrency},

		// Posting account (indented, contains colon) → ContextAccount
		{"  Assets:Bank", ContextAccount},
		{"\tIncome:Salary", ContextAccount},

		// Tag context — BOL or preceded by space
		{"  #ta", ContextTag},
		{"#foo", ContextTag},
		// Mid-line tag after header (F2 fix)
		{"2024-01-15 * \"Test\" #f", ContextTag},

		// Link context — BOL or preceded by space
		{"  ^li", ContextLink},
		{"^my", ContextLink},
		// Mid-line link after header (F2 fix)
		{"2024-01-15 * \"Test\" ^my", ContextLink},

		// InString context (odd number of quotes) — takes priority over tag/link
		{`  "`, ContextInString},
		{`  * "`, ContextInString},
		// # inside a string literal must NOT trigger ContextTag
		{`"Hello #notTag"`, ContextUnknown},

		// Currency after amount/account in posting → ContextCurrency
		{"  Assets:Bank  100 US", ContextCurrency},

		// Flag after date (only flag token, no keyword yet)
		{"2024-01-01 *", ContextFlag},
		{"2024-01-01 !", ContextFlag},

		// Negative: closed string (even quotes) is not ContextInString
		{`  "done"`, ContextUnknown},

		// MetaKey
		{"  key", ContextMetaKey},
		{"  k", ContextMetaKey},
		// MetaValue
		{"  key:", ContextMetaValue},
		{`  key: "par`, ContextMetaValue},
		// in-string MetaValue boundary: multiple quotes
		{`  source: "foo" "par`, ContextMetaValue},
		// No indent → not a metadata line
		{"key", ContextUnknown},

		// Negative: non-indented non-date non-special → ContextUnknown
		{"something", ContextUnknown},

		// Transaction header payee/narration heuristics.
		// 1 quote → cursor is in first string → ContextPayee
		{`2024-01-01 * "`, ContextPayee},
		{`2024-01-01 ! "foo`, ContextPayee},
		{`2024-01-01 txn "`, ContextPayee},
		// 3 quotes → cursor is in second string → ContextNarration
		{`2024-01-01 * "Test" "`, ContextNarration},
		{`2024-01-01 * "foo" "bar`, ContextNarration},
		// Even-quote txn-header cases: cursor is outside any string → ContextUnknown
		{`2024-01-01 * "foo" `, ContextUnknown},
		{`2024-01-01 * "foo" "bar" `, ContextUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.linePrefix, func(t *testing.T) {
			got := classifyContext(tc.linePrefix)
			if got != tc.want {
				t.Errorf("classifyContext(%q) = %v, want %v", tc.linePrefix, got, tc.want)
			}
		})
	}
}

// Direct test: the disambiguation logic has independent value and exercising it
// through handleCompletion would require a full server fixture per case.
func TestDisambiguateFirstString(t *testing.T) {
	tests := []struct {
		name   string
		suffix string
		want   ContextKind
	}{
		// No following string: lone string may be payee or narration.
		{"empty suffix", "", ContextPayeeOrNarration},
		{"closing quote only", `"`, ContextPayeeOrNarration},
		{"closing quote then space", `" `, ContextPayeeOrNarration},
		// A second quoted string follows: first string is the payee.
		{"second string follows", `" "narr"`, ContextPayee},
		{"second opening quote started", `" "`, ContextPayee},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := disambiguateFirstString(tc.suffix); got != tc.want {
				t.Errorf("disambiguateFirstString(%q) = %v, want %v", tc.suffix, got, tc.want)
			}
		})
	}
}

func TestContextKindString(t *testing.T) {
	tests := []struct {
		k    ContextKind
		want string
	}{
		{ContextUnknown, "Unknown"},
		{ContextAccount, "Account"},
		{ContextCurrency, "Currency"},
		{ContextKeyword, "Keyword"},
		{ContextFlag, "Flag"},
		{ContextTag, "Tag"},
		{ContextLink, "Link"},
		{ContextInString, "InString"},
		{ContextPayee, "Payee"},
		{ContextNarration, "Narration"},
		{ContextPayeeOrNarration, "PayeeOrNarration"},
		{ContextMetaKey, "MetaKey"},
		{ContextMetaValue, "MetaValue"},
		{ContextKind(99), "ContextKind(99)"},
	}
	for _, tc := range tests {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("ContextKind(%d).String() = %q, want %q", int(tc.k), got, tc.want)
		}
	}
}
