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
// ResolvedBlankLinesBetweenDirectives and
// ResolvedInsertBlankLinesBetweenDirectives mirror the spacing values
// also encoded in Format. They are exposed separately so callers (the
// merge.Plan builder) can read the spacing fields without re-applying
// Format closures against an internal options type.
type Decision struct {
	Path                                      string
	Order                                     OrderKind
	StripMetaKeys                             []string
	EqMetaKeys                                []string
	Format                                    []format.Option
	ResolvedBlankLinesBetweenDirectives       int
	ResolvedInsertBlankLinesBetweenDirectives bool
	PassThrough                               bool
}

// FormatSection holds optional format overrides. Each nil pointer means
// "inherit from the parent scope"; non-nil pointers replace the inherited
// value field-wise.
type FormatSection struct {
	CommaGrouping                     *bool
	AlignAmounts                      *bool
	AmountColumn                      *int
	EastAsianAmbiguousWidth           *int
	IndentWidth                       *int
	BlankLinesBetweenDirectives       *int
	InsertBlankLinesBetweenDirectives *bool
}

// AccountSection holds the [routes.account] configuration. Empty string
// fields mean "inherit the built-in default" (template, file_pattern,
// order).
type AccountSection struct {
	Template            string
	FilePattern         string
	Order               string
	EquivalenceMetaKeys []string
	Format              FormatSection
}

// PriceSection holds the [routes.price] configuration.
type PriceSection struct {
	Template            string
	FilePattern         string
	Order               string
	EquivalenceMetaKeys []string
	Format              FormatSection
}

// TransactionSection holds the [routes.transaction] config.
//
// DefaultStrategy is parsed and validated at config-load time but not
// yet consumed by Decide.
type TransactionSection struct {
	DefaultStrategy string
	OverrideMetaKey string
}

// AccountOverride matches accounts whose segments begin with Prefix. A
// match at segment boundaries is required: prefix "Assets:JP" matches
// "Assets:JP" and "Assets:JP:Cash" but not "Assets:JPN".
//
// HasEqMetaKeys distinguishes "no override declared" from "override
// declared as empty"; the second case silences inherited keys.
type AccountOverride struct {
	Prefix              string
	Template            string
	FilePattern         string
	Order               string
	TxnStrategy         string
	EquivalenceMetaKeys []string
	HasEqMetaKeys       bool
	Format              FormatSection
}

// CommodityOverride matches a commodity by exact-string equality.
type CommodityOverride struct {
	Commodity           string
	Template            string
	FilePattern         string
	Order               string
	EquivalenceMetaKeys []string
	HasEqMetaKeys       bool
	Format              FormatSection
}

// Config holds the resolved routing configuration. Decide accepts a nil
// Config and treats it as the zero value.
type Config struct {
	// Root is the destination root directory. Decide does not consult
	// it directly; downstream code such as the CLI uses it to resolve
	// each Decision.Path on disk.
	Root               string
	Account            AccountSection
	Price              PriceSection
	Transaction        TransactionSection
	Format             FormatSection
	AccountOverrides   []AccountOverride
	CommodityOverrides []CommodityOverride
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
	override := longestAccountOverride(cfg.AccountOverrides, a)
	var (
		oTemplate, oFilePattern, oOrder string
		oEqKeys                         []string
		oHasEqKeys                      bool
		oFormat                         FormatSection
	)
	if override != nil {
		oTemplate = override.Template
		oFilePattern = override.FilePattern
		oOrder = override.Order
		oEqKeys = override.EquivalenceMetaKeys
		oHasEqKeys = override.HasEqMetaKeys
		oFormat = override.Format
	}
	template := firstNonEmpty(oTemplate, cfg.Account.Template, defaultAccountTemplate)
	filePattern := firstNonEmpty(oFilePattern, cfg.Account.FilePattern, defaultFilePattern)
	order := firstNonEmpty(oOrder, cfg.Account.Order, defaultOrder)
	eqKeys := resolveEqKeys(oHasEqKeys, oEqKeys, cfg.Account.EquivalenceMetaKeys)

	resolved := resolveFormat(cfg.Format, cfg.Account.Format, oFormat)
	return Decision{
		Path:                                expandAccountTemplate(template, a, date, filePattern),
		Order:                               orderKindFromString(order),
		EqMetaKeys:                          eqKeys,
		Format:                              resolved.options(),
		ResolvedBlankLinesBetweenDirectives: resolved.BlankLinesBetweenDirectives,
		ResolvedInsertBlankLinesBetweenDirectives: resolved.InsertBlankLinesBetweenDirectives,
	}
}

// decidePrice resolves the routing decision for Price directives,
// applying the matching commodity override when one fires.
func decidePrice(cfg *Config, commodity string, date time.Time) Decision {
	override := commodityOverrideFor(cfg.CommodityOverrides, commodity)
	var (
		oTemplate, oFilePattern, oOrder string
		oEqKeys                         []string
		oHasEqKeys                      bool
		oFormat                         FormatSection
	)
	if override != nil {
		oTemplate = override.Template
		oFilePattern = override.FilePattern
		oOrder = override.Order
		oEqKeys = override.EquivalenceMetaKeys
		oHasEqKeys = override.HasEqMetaKeys
		oFormat = override.Format
	}
	template := firstNonEmpty(oTemplate, cfg.Price.Template, defaultPriceTemplate)
	filePattern := firstNonEmpty(oFilePattern, cfg.Price.FilePattern, defaultFilePattern)
	order := firstNonEmpty(oOrder, cfg.Price.Order, defaultOrder)
	eqKeys := resolveEqKeys(oHasEqKeys, oEqKeys, cfg.Price.EquivalenceMetaKeys)

	resolved := resolveFormat(cfg.Format, cfg.Price.Format, oFormat)
	return Decision{
		Path:                                expandCommodityTemplate(template, commodity, date, filePattern),
		Order:                               orderKindFromString(order),
		EqMetaKeys:                          eqKeys,
		Format:                              resolved.options(),
		ResolvedBlankLinesBetweenDirectives: resolved.BlankLinesBetweenDirectives,
		ResolvedInsertBlankLinesBetweenDirectives: resolved.InsertBlankLinesBetweenDirectives,
	}
}

// resolveEqKeys implements replacement (not concatenation) inheritance:
// when the override declares its own slice (even an empty one), it
// silences the inherited value entirely.
func resolveEqKeys(hasOverride bool, overrideKeys []string, parent []string) []string {
	if hasOverride {
		if len(overrideKeys) == 0 {
			return nil
		}
		out := make([]string, len(overrideKeys))
		copy(out, overrideKeys)
		return out
	}
	if len(parent) == 0 {
		return nil
	}
	out := make([]string, len(parent))
	copy(out, parent)
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
