package predict

import (
	"unicode"
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/words"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Tokenizer converts a free-text field value into an ordered, deterministic
// token slice for feature extraction. It is the extension point for
// alternative tokenization strategies (e.g. a morphological-analyzer plugin);
// the default implementation is dictionary-free and language-agnostic.
//
// Tokenize MUST be a pure function of its input: equal inputs yield slices
// that are equal element-wise and in the same order. Implementations must be
// safe for concurrent use. Tokens are namespace-agnostic raw terms; the caller
// is responsible for any field-namespacing or weighting.
type Tokenizer interface {
	// Tokenize returns the tokens of s in their order of first contribution
	// from the input. It returns a nil or empty slice for empty or
	// token-free input; it never returns an error.
	Tokenize(s string) []string
}

// Option configures [NewDefaultTokenizer].
type Option func(*tokenizerConfig)

// WithCJKNGram sets contiguous character n-gram sizes for CJK runs (default {1,2}).
func WithCJKNGram(sizes ...int) Option {
	return func(c *tokenizerConfig) { c.cjkNGram = append([]int(nil), sizes...) }
}

// WithNumberPlaceholder sets the placeholder for digit/reference tokens
// (default "#num"; "" drops such tokens entirely).
func WithNumberPlaceholder(tok string) Option {
	return func(c *tokenizerConfig) { c.numPlace = tok }
}

// WithDigitIDMinLen sets the min rune length at which a digit-bearing
// alphanumeric token is collapsed to the placeholder (default 6).
func WithDigitIDMinLen(n int) Option {
	return func(c *tokenizerConfig) { c.digitIDMin = n }
}

// NewDefaultTokenizer returns the dictionary-free default Tokenizer. With no
// options it emits, per script run: character bigrams plus unigrams for CJK
// runs, and Unicode word-segmented (UAX #29) lowercase tokens for non-CJK
// runs, after NFKC normalization and case folding. Pure-digit and long
// alphanumeric reference tokens collapse to a single placeholder token.
// The returned Tokenizer is immutable and safe for concurrent use.
func NewDefaultTokenizer(opts ...Option) Tokenizer {
	cfg := tokenizerConfig{
		cjkNGram:   []int{1, 2},
		numPlace:   "#num",
		digitIDMin: 6,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &defaultTokenizer{cfg: cfg}
}

type tokenizerConfig struct {
	cjkNGram   []int
	numPlace   string
	digitIDMin int
}

type defaultTokenizer struct{ cfg tokenizerConfig }

// stateless; concurrent-safe
var folder = cases.Fold()

func (dt *defaultTokenizer) Tokenize(s string) []string {
	if s == "" {
		return nil
	}
	s = norm.NFKC.String(s)
	s = folder.String(s)

	runs := splitRuns(s)
	var out []string
	for _, run := range runs {
		if run.isCJK {
			out = append(out, cjkNGrams(run.text, dt.cfg.cjkNGram)...)
		} else {
			out = append(out, wordTokens(run.text, &dt.cfg)...)
		}
	}
	return out
}

type scriptRun struct {
	text  string
	isCJK bool
}

// cjkRanges covers the CJK scripts treated as character n-gram input.
var cjkRanges = []*unicode.RangeTable{
	unicode.Han,
	unicode.Hiragana,
	unicode.Katakana,
	unicode.Hangul,
	unicode.Bopomofo,
}

// cjkExtend holds CJK prolonged-sound and iteration marks that carry the
// modifier-letter category (Lm) rather than a script, so unicode.In against
// cjkRanges misses them. Binding them into adjacent CJK runs keeps n-gram
// generation intact (e.g. コーヒー → コー/ーヒ/ヒー).
var cjkExtend = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x3005, Hi: 0x3005, Stride: 1}, // 々 ideographic iteration
		{Lo: 0x3031, Hi: 0x3035, Stride: 1}, // 〱-〵 vertical kana repeat
		{Lo: 0x309D, Hi: 0x309E, Stride: 1}, // ゝゞ hiragana iteration
		{Lo: 0x30FC, Hi: 0x30FE, Stride: 1}, // ー prolonged sound, ヽヾ katakana iteration
	},
}

// splitRuns segments s into maximal runs of CJK runes and non-CJK runes.
// Separator, punctuation, and symbol runes form run boundaries and are not
// emitted. After NFKC + case-fold, fullwidth Latin/digits have already been
// mapped to ASCII and fall into non-CJK runs.
func splitRuns(s string) []scriptRun {
	const (
		classNone  = 0
		classCJK   = 1
		classOther = 2
	)
	classOf := func(r rune) int {
		if unicode.In(r, cjkRanges...) || unicode.Is(cjkExtend, r) {
			return classCJK
		}
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return classNone
		}
		return classOther
	}

	var runs []scriptRun
	curClass := classNone
	start := 0

	appendRun := func(end int) {
		if start < end {
			runs = append(runs, scriptRun{text: s[start:end], isCJK: curClass == classCJK})
		}
	}

	pos := 0
	for pos < len(s) {
		r, size := utf8.DecodeRuneInString(s[pos:])
		cls := classOf(r)
		switch {
		case cls == classNone:
			if curClass != classNone {
				appendRun(pos)
				curClass = classNone
			}
			start = pos + size
		case cls != curClass:
			if curClass != classNone {
				appendRun(pos)
			}
			curClass = cls
			start = pos
		}
		pos += size
	}
	if curClass != classNone {
		appendRun(len(s))
	}
	return runs
}

// cjkNGrams emits n-gram windows for each size in sizes, in the order given,
// for the CJK rune sequence s. Sizes ≤ 0 or larger than len(runes) are skipped.
func cjkNGrams(s string, sizes []int) []string {
	runes := []rune(s)
	var out []string
	for _, n := range sizes {
		if n <= 0 || n > len(runes) {
			continue
		}
		for i := 0; i <= len(runes)-n; i++ {
			out = append(out, string(runes[i:i+n]))
		}
	}
	return out
}

// wordTokens segments the non-CJK run s via UAX #29 word boundaries and
// collapses digit/reference tokens.
func wordTokens(s string, cfg *tokenizerConfig) []string {
	iter := words.FromString(s)
	var out []string
	for iter.Next() {
		tok := iter.Value()
		if !isWordToken(tok) {
			continue
		}
		canon, isNum := canonNumeric(tok, cfg)
		if !isNum {
			out = append(out, tok)
		} else if canon != "" {
			out = append(out, canon)
		}
	}
	return out
}

// isWordToken reports whether s contains at least one letter or digit.
func isWordToken(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// canonNumeric returns (placeholder, true) when tok should be collapsed:
// all-digit tokens always, and alphanumeric tokens of rune length ≥ digitIDMin
// that contain at least one digit. Returns ("", false) to keep the token as-is.
//
// Callers guarantee tok is non-empty and contains at least one letter or digit.
func canonNumeric(tok string, cfg *tokenizerConfig) (string, bool) {
	allDigit := true
	hasDigit := false
	hasLetter := false
	runeLen := 0
	for _, r := range tok {
		runeLen++
		if unicode.IsDigit(r) {
			hasDigit = true
		} else {
			allDigit = false
			if unicode.IsLetter(r) {
				hasLetter = true
			}
		}
	}
	if allDigit {
		return cfg.numPlace, true
	}
	if runeLen >= cfg.digitIDMin && hasDigit && hasLetter {
		return cfg.numPlace, true
	}
	return "", false
}
