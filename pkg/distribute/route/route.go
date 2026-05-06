package route

import (
	"fmt"
	"strings"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/format"
)

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
// DefaultStrategy is parsed and validated at config-load time but not
// yet consumed by Decide.
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
// schema and is populated by the CLI from --root (or the directory of
// --ledger). Decide does not consult Root directly; downstream code
// such as the CLI uses it to resolve each Decision.Path on disk.
type Config struct {
	Root   string `toml:"-"`
	Routes Routes `toml:"routes"`
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
		return decideAccount(cfg, v.Postings[0].Account, v.Date), nil
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
// dates are date-only). Only "YYYYmm" (and "" via defaults) reach Decide
// today; config-load validates other values out. Hand-built Configs
// bypassing the loader fall back to YYYYmm here so Decide stays free of
// error returns.
func formatDate(date time.Time, pattern string) string {
	return fmt.Sprintf("%04d%02d", date.Year(), int(date.Month()))
}
