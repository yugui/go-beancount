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

		// Date-first lines → ContextKeyword (only when nothing follows the date+space)
		{"2024-01-01 ", ContextKeyword},
		// Partial keyword after date falls through to ContextUnknown (acceptable failure mode)
		{"2023-12-31 op", ContextUnknown},

		// Account token after date + open/close → ContextAccount
		{"2024-01-01 open Assets:Bank", ContextAccount},
		{"2024-01-01 open Assets:資産", ContextAccount},
		{"2024-01-01 open Assets:2024", ContextAccount},
		{"2024-01-01 close Assets:Bank", ContextAccount},

		// Currency token after date + open (pure uppercase comes first in open/close) → ContextCurrency
		{"2024-01-01 open Assets:Bank USD", ContextCurrency},

		// Partially typed account after open (no colon yet) → ContextAccount
		// trailingAccountToken matches and reCurrencyToken also matches "Asset",
		// but the token has no colon (accountTokenWithColon is false), so it falls through to ContextCurrency.
		// This is the documented acceptable false-negative for early typing.
		{"2024-01-01 open Asset", ContextCurrency},

		// Currency after balance/price (fallback path, pure uppercase, no colon) → ContextCurrency
		{"2024-01-01 balance Assets:Bank 100 US", ContextCurrency},
		{"2024-01-01 price USD 1.0 EU", ContextCurrency},

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
