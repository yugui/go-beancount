package route

import (
	"fmt"
	"strings"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/format"
)

// defaultOverrideMetaKey is the built-in metadata key used to pick a
// Transaction's destination account when no explicit OverrideMetaKey is
// configured.
const defaultOverrideMetaKey = "route-account"

// OrderKind selects how new directives are positioned relative to
// existing dated directives in a destination file.
type OrderKind int

const (
	// OrderAscending inserts new directives so that older dates precede
	// newer ones.
	OrderAscending OrderKind = iota
	// OrderDescending inserts new directives so that newer dates precede
	// older ones.
	OrderDescending
	// OrderAppend inserts new directives at the end of the file.
	OrderAppend
)

// Decision is the routing outcome for a single directive. PassThrough
// is true when the directive's kind is not routable; in that case the
// remaining fields are zero and the caller decides what to do with the
// directive (typically: error out, or echo to stdout).
//
// Format carries body-level options (comma_grouping, align_amounts,
// amount_column, east_asian_ambiguous_width, indent_width). File-level
// spacing lives on BlankLinesBetweenDirectives and
// InsertBlankLinesBetweenDirectives so the merge.Plan builder can read
// the spacing fields without re-applying Format closures against an
// internal options type.
type Decision struct {
	Path                              string
	Order                             OrderKind
	StripMetaKeys                     []string
	EqMetaKeys                        []string
	Format                            []format.Option
	BlankLinesBetweenDirectives       int
	InsertBlankLinesBetweenDirectives bool
	PassThrough                       bool
}

// FormatSection holds optional format overrides. Each nil pointer means
// "inherit from the parent scope"; non-nil pointers replace the inherited
// value field-wise.
type FormatSection struct {
	CommaGrouping                     *bool `toml:"comma_grouping"`
	AlignAmounts                      *bool `toml:"align_amounts"`
	AmountColumn                      *int  `toml:"amount_column"`
	EastAsianAmbiguousWidth           *int  `toml:"east_asian_ambiguous_width"`
	IndentWidth                       *int  `toml:"indent_width"`
	BlankLinesBetweenDirectives       *int  `toml:"blank_lines_between_directives"`
	InsertBlankLinesBetweenDirectives *bool `toml:"insert_blank_lines_between_directives"`
}

// AccountSection holds the [routes.account] configuration. Empty string
// fields mean "inherit the built-in default" (template, file_pattern,
// order). EquivalenceMetaKeys is a *[]string so the loader can
// distinguish "absent" (nil) from "explicitly empty" (non-nil empty);
// the latter silences inherited keys when used on an override.
type AccountSection struct {
	Template            string            `toml:"template"`
	FilePattern         string            `toml:"file_pattern"`
	Order               string            `toml:"order"`
	EquivalenceMetaKeys *[]string         `toml:"equivalence_meta_keys"`
	Format              FormatSection     `toml:"format"`
	Overrides           []AccountOverride `toml:"override"`
}

// PriceSection holds the [routes.price] configuration. Empty string
// fields mean "inherit the built-in default" (template, file_pattern,
// order). EquivalenceMetaKeys is a *[]string so the loader can
// distinguish "absent" (nil) from "explicitly empty" (non-nil empty);
// the latter silences inherited keys when used on an override.
type PriceSection struct {
	Template            string              `toml:"template"`
	FilePattern         string              `toml:"file_pattern"`
	Order               string              `toml:"order"`
	EquivalenceMetaKeys *[]string           `toml:"equivalence_meta_keys"`
	Format              FormatSection       `toml:"format"`
	Overrides           []CommodityOverride `toml:"override"`
}

// TransactionSection holds the [routes.transaction] config.
//
// DefaultStrategy selects the posting that names the destination
// account when neither the transaction-level nor a posting-level
// override key is set; OverrideMetaKey names the metadata key
// inspected for those overrides (default DefaultOverrideMetaKey).
type TransactionSection struct {
	DefaultStrategy string `toml:"default_strategy"`
	OverrideMetaKey string `toml:"override_meta_key"`
}

// AccountOverride matches accounts whose segments begin with Prefix. A
// match at segment boundaries is required: prefix "Assets:JP" matches
// "Assets:JP" and "Assets:JP:Cash" but not "Assets:JPN".
//
// EquivalenceMetaKeys is *[]string so callers can distinguish "no
// override declared" (nil) from "override declared as empty" (non-nil
// empty slice); the second case silences inherited keys.
type AccountOverride struct {
	Prefix              string        `toml:"prefix"`
	Template            string        `toml:"template"`
	FilePattern         string        `toml:"file_pattern"`
	Order               string        `toml:"order"`
	TxnStrategy         string        `toml:"txn_strategy"`
	EquivalenceMetaKeys *[]string     `toml:"equivalence_meta_keys"`
	Format              FormatSection `toml:"format"`
}

// CommodityOverride matches a commodity by exact-string equality.
type CommodityOverride struct {
	Commodity           string        `toml:"commodity"`
	Template            string        `toml:"template"`
	FilePattern         string        `toml:"file_pattern"`
	Order               string        `toml:"order"`
	EquivalenceMetaKeys *[]string     `toml:"equivalence_meta_keys"`
	Format              FormatSection `toml:"format"`
}

// Routes mirrors the [routes] table of the TOML schema. Keeping the four
// sections under a single field matches the natural TOML hierarchy and
// keeps Config decoding direct (no custom UnmarshalTOML).
type Routes struct {
	Account     AccountSection     `toml:"account"`
	Price       PriceSection       `toml:"price"`
	Transaction TransactionSection `toml:"transaction"`
	Format      FormatSection      `toml:"format"`
}

// Config holds the resolved routing configuration. Decide accepts a nil
// Config and treats it as the zero value.
//
// Root is the destination root directory; it is not part of the TOML
// schema and is populated by the caller. Decide does not consult Root
// directly — callers use it to resolve each Decision.Path on disk.
//
// Warn is an optional sink for non-fatal routing warnings (e.g.
// malformed override metadata). It is not part of the TOML schema;
// the caller installs a closure if it wants to surface these. Nil
// means silent.
type Config struct {
	Root   string                           `toml:"-"`
	Routes Routes                           `toml:"routes"`
	Warn   func(format string, args ...any) `toml:"-"`
}

const (
	defaultAccountTemplate = "transactions/{account}/{date}.beancount"
	defaultPriceTemplate   = "quotes/{commodity}/{date}.beancount"
	defaultFilePattern     = "YYYYmm"
	defaultOrder           = "ascending"
)

// Decide resolves d to a Decision under the configured routing rules.
//
// Returns an error only when the directive is structurally unable to
// supply a routing key — currently only a Transaction with no postings.
// Other directives are assumed to have been validated upstream.
func Decide(d ast.Directive, cfg *Config) (Decision, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	switch v := d.(type) {
	case *ast.Open:
		return decideAccount(cfg, v.Account, v.Date), nil
	case *ast.Close:
		return decideAccount(cfg, v.Account, v.Date), nil
	case *ast.Balance:
		return decideAccount(cfg, v.Account, v.Date), nil
	case *ast.Note:
		return decideAccount(cfg, v.Account, v.Date), nil
	case *ast.Document:
		return decideAccount(cfg, v.Account, v.Date), nil
	case *ast.Pad:
		return decideAccount(cfg, v.Account, v.Date), nil
	case *ast.Transaction:
		if len(v.Postings) == 0 {
			return Decision{}, fmt.Errorf("route: transaction on %s has no postings", v.Date.Format("2006-01-02"))
		}
		return decideTransaction(cfg, v)
	case *ast.Price:
		return decidePrice(cfg, v.Commodity, v.Date), nil
	case *ast.Option, *ast.Plugin, *ast.Include,
		*ast.Event, *ast.Query, *ast.Custom, *ast.Commodity:
		return Decision{PassThrough: true}, nil
	}
	return Decision{}, fmt.Errorf("route: unsupported directive type %T", d)
}

// decideAccount resolves the routing decision for account-keyed
// directives, applying the matching account override when one fires.
func decideAccount(cfg *Config, a ast.Account, date time.Time) Decision {
	override := longestAccountOverride(cfg.Routes.Account.Overrides, a)
	var (
		oTemplate, oFilePattern, oOrder string
		oEqKeys                         *[]string
		oFormat                         FormatSection
	)
	if override != nil {
		oTemplate = override.Template
		oFilePattern = override.FilePattern
		oOrder = override.Order
		oEqKeys = override.EquivalenceMetaKeys
		oFormat = override.Format
	}
	template := firstNonEmpty(oTemplate, cfg.Routes.Account.Template, defaultAccountTemplate)
	filePattern := firstNonEmpty(oFilePattern, cfg.Routes.Account.FilePattern, defaultFilePattern)
	order := firstNonEmpty(oOrder, cfg.Routes.Account.Order, defaultOrder)
	eqKeys := resolveEqKeys(oEqKeys, cfg.Routes.Account.EquivalenceMetaKeys)

	resolved := resolveFormat(cfg.Routes.Format, cfg.Routes.Account.Format, oFormat)
	return Decision{
		Path:                              expandAccountTemplate(template, a, date, filePattern),
		Order:                             orderKindFromString(order),
		EqMetaKeys:                        eqKeys,
		Format:                            resolved.options(),
		BlankLinesBetweenDirectives:       resolved.BlankLinesBetweenDirectives,
		InsertBlankLinesBetweenDirectives: resolved.InsertBlankLinesBetweenDirectives,
	}
}

// decidePrice resolves the routing decision for Price directives,
// applying the matching commodity override when one fires.
func decidePrice(cfg *Config, commodity string, date time.Time) Decision {
	override := commodityOverrideFor(cfg.Routes.Price.Overrides, commodity)
	var (
		oTemplate, oFilePattern, oOrder string
		oEqKeys                         *[]string
		oFormat                         FormatSection
	)
	if override != nil {
		oTemplate = override.Template
		oFilePattern = override.FilePattern
		oOrder = override.Order
		oEqKeys = override.EquivalenceMetaKeys
		oFormat = override.Format
	}
	template := firstNonEmpty(oTemplate, cfg.Routes.Price.Template, defaultPriceTemplate)
	filePattern := firstNonEmpty(oFilePattern, cfg.Routes.Price.FilePattern, defaultFilePattern)
	order := firstNonEmpty(oOrder, cfg.Routes.Price.Order, defaultOrder)
	eqKeys := resolveEqKeys(oEqKeys, cfg.Routes.Price.EquivalenceMetaKeys)

	resolved := resolveFormat(cfg.Routes.Format, cfg.Routes.Price.Format, oFormat)
	return Decision{
		Path:                              expandCommodityTemplate(template, commodity, date, filePattern),
		Order:                             orderKindFromString(order),
		EqMetaKeys:                        eqKeys,
		Format:                            resolved.options(),
		BlankLinesBetweenDirectives:       resolved.BlankLinesBetweenDirectives,
		InsertBlankLinesBetweenDirectives: resolved.InsertBlankLinesBetweenDirectives,
	}
}

// transactionLabel returns a short human-readable identifier for txn, used in
// warning messages. It includes the date and, when present, payee and/or
// narration.
func transactionLabel(txn *ast.Transaction) string {
	date := txn.Date.Format("2006-01-02")
	if txn.Payee != "" {
		return fmt.Sprintf("%s %q %q", date, txn.Payee, txn.Narration)
	}
	if txn.Narration != "" {
		return fmt.Sprintf("%s %q", date, txn.Narration)
	}
	return date
}

// decideTransaction resolves the routing decision for a Transaction directive.
// It applies a four-rule precedence chain to determine the destination account
// and always sets StripMetaKeys to the override key so downstream emit code
// can strip the metadata key from output regardless of which rule fired.
//
// Rules (highest to lowest priority):
//  1. Transaction-level MetaString under the override key.
//  2. First posting carrying MetaBool TRUE under the override key.
//  3. Configured DefaultStrategy: first-posting, last-posting, first-debit, first-credit.
//  4. Fallback: Postings[0].Account.
//
// Malformed values (wrong Kind, invalid account, empty string) emit a one-line
// warning via cfg.Warn and fall through to the next rule.
func decideTransaction(cfg *Config, txn *ast.Transaction) (Decision, error) {
	overrideKey := cfg.Routes.Transaction.OverrideMetaKey
	if overrideKey == "" {
		overrideKey = defaultOverrideMetaKey
	}

	warn := cfg.Warn
	if warn == nil {
		warn = func(string, ...any) {}
	}

	label := transactionLabel(txn)

	// Rule 1: txn-level MetaString under the override key.
	account, found := metaRouteAccount(txn.Meta, overrideKey, label, warn)

	// Rule 2: first posting with MetaBool TRUE under the override key.
	if !found {
		for _, p := range txn.Postings {
			if postingHasRouteTrue(p, overrideKey, label, warn) {
				account = p.Account
				found = true
				break
			}
		}
	}

	// Rule 3: configured DefaultStrategy. pickByStrategy returns
	// ("", false) for an empty or unknown strategy.
	if !found {
		account, found = pickByStrategy(cfg.Routes.Transaction.DefaultStrategy, txn.Postings)
	}

	// Rule 4: fallback to Postings[0].Account (guaranteed non-empty by caller).
	if !found {
		account = txn.Postings[0].Account
	}

	// Build the account decision using the same logic as decideAccount,
	// matching the override that governs this account.
	d := decideAccount(cfg, account, txn.Date)
	// Always strip the override key from the emitted directive so routing
	// metadata does not appear in the output files.
	d.StripMetaKeys = []string{overrideKey}
	return d, nil
}

// metaRouteAccount extracts and validates an account value from meta under key.
// If the key is absent, it returns ("", false) silently. If the value is
// malformed (non-MetaString kind, empty string, or invalid account name), it
// emits a warning via warn using label to identify the directive and returns
// ("", false). On success it returns (account, true).
func metaRouteAccount(
	meta ast.Metadata,
	key string,
	label string,
	warn func(string, ...any),
) (account ast.Account, ok bool) {
	mv, present := meta.Props[key]
	if !present {
		return "", false
	}
	if mv.Kind != ast.MetaString {
		warn("transaction %s: metadata key %q has wrong kind (expected string, got %s); falling through",
			label, key, mv.Kind)
		return "", false
	}
	raw := mv.String
	if raw == "" {
		warn("transaction %s: metadata key %q has invalid or empty account value %q; falling through",
			label, key, raw)
		return "", false
	}
	acct := ast.Account(raw)
	if !acct.IsValid() {
		warn("transaction %s: metadata key %q has invalid or empty account value %q; falling through",
			label, key, raw)
		return "", false
	}
	return acct, true
}

// postingHasRouteTrue reports whether posting p carries MetaBool TRUE under
// key. If the key is absent, it returns false silently. If the value has a
// non-MetaBool kind, it emits a warning via warn (using label to identify the
// parent transaction) and returns false so the caller falls through to the
// next rule. FALSE values are returned as false without warning.
func postingHasRouteTrue(
	p ast.Posting,
	key string,
	label string,
	warn func(string, ...any),
) (ok bool) {
	mv, present := p.Meta.Props[key]
	if !present {
		return false
	}
	if mv.Kind != ast.MetaBool {
		warn("transaction %s: posting %s metadata key %q has wrong kind (expected bool, got %s); falling through",
			label, p.Account, key, mv.Kind)
		return false
	}
	return mv.Bool
}

// pickByStrategy selects a posting account from postings according to the
// configured DefaultStrategy. Auto-postings (Amount == nil) are skipped for
// first-debit and first-credit strategies, which rely on the sign of the
// amount. Returns (account, true) when a match is found, or ("", false) when
// the strategy produces no match (e.g. first-debit on an all-credit transaction).
func pickByStrategy(strategy string, postings []ast.Posting) (ast.Account, bool) {
	switch strategy {
	case "first-posting":
		if len(postings) > 0 {
			return postings[0].Account, true
		}
	case "last-posting":
		if len(postings) > 0 {
			return postings[len(postings)-1].Account, true
		}
	case "first-debit":
		for _, p := range postings {
			if p.Amount == nil {
				continue // skip auto-postings
			}
			if p.Amount.Number.Sign() > 0 {
				return p.Account, true
			}
		}
	case "first-credit":
		for _, p := range postings {
			if p.Amount == nil {
				continue // skip auto-postings
			}
			if p.Amount.Number.Sign() < 0 {
				return p.Account, true
			}
		}
	}
	return "", false
}

// resolveEqKeys implements replacement (not concatenation) inheritance:
// when the override declares its own slice (even an empty one), it
// silences the inherited value entirely.
func resolveEqKeys(override, parent *[]string) []string {
	if override != nil {
		if len(*override) == 0 {
			return nil
		}
		out := make([]string, len(*override))
		copy(out, *override)
		return out
	}
	if parent == nil || len(*parent) == 0 {
		return nil
	}
	out := make([]string, len(*parent))
	copy(out, *parent)
	return out
}

// orderKindFromString converts a validated order literal to an OrderKind.
// Unknown literals fall back to OrderAscending; config-load already
// rejects unsupported values, so this branch only fires when Decide
// receives a hand-built Config.
func orderKindFromString(s string) OrderKind {
	switch strings.ToLower(s) {
	case "descending":
		return OrderDescending
	case "append":
		return OrderAppend
	default:
		return OrderAscending
	}
}

// firstNonEmpty returns the first non-empty string in values, or "" when
// all are empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// expandAccountTemplate fills {account} and {date} in tmpl. Forward
// slashes in {account} reflect the account's component segments; the
// date is formatted per pattern.
func expandAccountTemplate(tmpl string, a ast.Account, date time.Time, pattern string) string {
	dated := formatDate(date, pattern)
	out := strings.ReplaceAll(tmpl, "{account}", strings.Join(a.Parts(), "/"))
	out = strings.ReplaceAll(out, "{date}", dated)
	return out
}

// expandCommodityTemplate fills {commodity} and {date} in tmpl. The
// commodity literal is substituted verbatim; the date is formatted per
// pattern.
func expandCommodityTemplate(tmpl string, commodity string, date time.Time, pattern string) string {
	dated := formatDate(date, pattern)
	out := strings.ReplaceAll(tmpl, "{commodity}", commodity)
	out = strings.ReplaceAll(out, "{date}", dated)
	return out
}

// formatDate formats date under pattern. Calendar fields are read
// directly from the time value to avoid timezone conversion (beancount
// dates are date-only). Supported patterns:
//   - "YYYY"      → year only (e.g. "2024")
//   - "YYYYmm"   → year and month (e.g. "202401")
//   - "YYYYmmdd" → year, month, and day (e.g. "20240115")
//
// Empty string defaults to "YYYYmm". Hand-built Configs bypassing the
// loader fall back to "YYYYmm" for unknown values so Decide stays free
// of error returns.
func formatDate(date time.Time, pattern string) string {
	switch pattern {
	case "YYYY":
		return fmt.Sprintf("%04d", date.Year())
	case "YYYYmmdd":
		return fmt.Sprintf("%04d%02d%02d", date.Year(), int(date.Month()), date.Day())
	default: // "YYYYmm" and ""
		return fmt.Sprintf("%04d%02d", date.Year(), int(date.Month()))
	}
}
