package predict_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/importer/hook/std/predict"
)

func TestNewDefaultTokenizer(t *testing.T) {
	t.Parallel()

	tok := predict.NewDefaultTokenizer()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name:  "CJK only",
			input: "東京駅",
			// unigrams then bigrams: 東,京,駅,東京,京駅
			want: []string{"東", "京", "駅", "東京", "京駅"},
		},
		{
			name:  "CJK two-rune",
			input: "東京",
			want:  []string{"東", "京", "東京"},
		},
		{
			name:  "CJK single rune",
			input: "駅",
			// size-2 skipped (n > len)
			want: []string{"駅"},
		},
		{
			name:  "non-CJK only",
			input: "Hello World",
			want:  []string{"hello", "world"},
		},
		{
			name:  "non-CJK already lowercase",
			input: "coffee shop",
			want:  []string{"coffee", "shop"},
		},
		{
			name:  "mixed CJK and non-CJK",
			input: "Starbucks 東京駅",
			want:  []string{"starbucks", "東", "京", "駅", "東京", "京駅"},
		},
		{
			name:  "NFKC fullwidth latin folds to ASCII",
			input: "ＡＢＣＤ", // fullwidth A-D → NFKC → "ABCD" → fold → "abcd"
			want:  []string{"abcd"},
		},
		{
			name:  "NFKC halfwidth katakana maps to fullwidth",
			input: "ｱｲｳ", // halfwidth katakana → fullwidth アイウ
			want:  []string{"ア", "イ", "ウ", "アイ", "イウ"},
		},
		{
			name:  "all-digit token replaced with #num",
			input: "order 12345",
			want:  []string{"order", "#num"},
		},
		{
			name:  "short digit-bearing token kept (len < 6)",
			input: "A1B2",
			// 4 runes, < 6 → not collapsed
			want: []string{"a1b2"},
		},
		{
			name:  "long alphanumeric with digit collapsed",
			input: "TXN20230901",
			// 11 runes, has digit, has letter → collapse
			want: []string{"#num"},
		},
		{
			name:  "pure letters long token not collapsed",
			input: "ABCDEFGH",
			// no digit → not collapsed
			want: []string{"abcdefgh"},
		},
		{
			name:  "multiple numeric tokens each get placeholder",
			input: "ref 100 code ABC123DEF",
			want:  []string{"ref", "#num", "code", "#num"},
		},
		{
			name:  "punctuation as run boundary",
			input: "東京・大阪",
			// ・ is punctuation → two CJK runs
			want: []string{"東", "京", "東京", "大", "阪", "大阪"},
		},
		{
			name:  "whitespace-only",
			input: "   ",
			want:  nil,
		},
		{
			name:  "hiragana run",
			input: "ありがとう",
			want:  []string{"あ", "り", "が", "と", "う", "あり", "りが", "がと", "とう"},
		},
		{
			name:  "hangul run",
			input: "한국어",
			want:  []string{"한", "국", "어", "한국", "국어"},
		},
		{
			name:  "katakana prolonged-sound mark binds into CJK run",
			input: "コーヒー",
			// ー (U+30FC) is Lm, not in any script table; it must still bind
			// into the katakana run so bigrams form across it.
			want: []string{"コ", "ー", "ヒ", "ー", "コー", "ーヒ", "ヒー"},
		},
		{
			name:  "katakana with prolonged-sound mark mid-run",
			input: "ラーメン",
			want:  []string{"ラ", "ー", "メ", "ン", "ラー", "ーメ", "メン"},
		},
		{
			name: "decomposed accent recomposed by NFKC, no fragmentation",
			// "cafe" + U+0301 combining acute accent (decomposed form);
			// NFKC composes it to the single rune é (U+00E9).
			input: "café",
			want:  []string{"café"},
		},
		{
			name:  "emoji dropped, surrounding words preserved",
			input: "hello 😀 world",
			want:  []string{"hello", "world"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tok.Tokenize(tc.input)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Tokenize(%q) mismatch (-want +got):\n%s", tc.input, diff)
			}
		})
	}
}

func TestTokenizerDeterminism(t *testing.T) {
	t.Parallel()

	tok := predict.NewDefaultTokenizer()
	input := "Starbucks 東京駅 ref TXN20230901"

	first := tok.Tokenize(input)
	for range 10 {
		got := tok.Tokenize(input)
		if diff := cmp.Diff(first, got); diff != "" {
			t.Fatalf("Tokenize(%q): non-deterministic output on repeat call (-first +got):\n%s", input, diff)
		}
	}
}

func TestWithNumberPlaceholder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		placeholder string
		input       string
		want        []string
	}{
		{
			name:        "empty placeholder drops numeric tokens",
			placeholder: "",
			input:       "order 12345 ref TXN20230901",
			want:        []string{"order", "ref"},
		},
		{
			name:        "custom placeholder",
			placeholder: "NUM",
			input:       "invoice 999",
			want:        []string{"invoice", "NUM"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tok := predict.NewDefaultTokenizer(predict.WithNumberPlaceholder(tc.placeholder))
			got := tok.Tokenize(tc.input)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Tokenize(%q) with placeholder %q mismatch (-want +got):\n%s", tc.input, tc.placeholder, diff)
			}
		})
	}
}

func TestWithCJKNGram(t *testing.T) {
	t.Parallel()

	tok := predict.NewDefaultTokenizer(predict.WithCJKNGram(2))
	// unigrams disabled; only bigrams
	got := tok.Tokenize("東京駅")
	want := []string{"東京", "京駅"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("WithCJKNGram(2) mismatch (-want +got):\n%s", diff)
	}
}

func TestWithDigitIDMinLen(t *testing.T) {
	t.Parallel()

	// Lower the threshold so 4-rune tokens are collapsed.
	tok := predict.NewDefaultTokenizer(predict.WithDigitIDMinLen(4))
	got := tok.Tokenize("A1B2")
	want := []string{"#num"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("WithDigitIDMinLen(4) mismatch (-want +got):\n%s", diff)
	}
}
