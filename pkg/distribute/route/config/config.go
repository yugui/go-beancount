// Package config loads a beanfile TOML routing configuration into a
// route.Config. Decoding is strict: unknown keys, unsupported order /
// file-pattern values, and unsupported transaction strategies are
// rejected at load time.
package config

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/yugui/go-beancount/pkg/distribute/route"
)

// rawConfig mirrors the TOML schema in §5 of the design doc. Slice
// fields use *T pointers (or boolean "Set" flags) so the loader can
// distinguish "absent" from "explicitly empty" — the difference matters
// for equivalence_meta_keys, where an empty list silences inheritance.
type rawConfig struct {
	Routes rawRoutes `toml:"routes"`
}

type rawRoutes struct {
	Account     rawAccountSection     `toml:"account"`
	Price       rawPriceSection       `toml:"price"`
	Transaction rawTransactionSection `toml:"transaction"`
	Format      rawFormatSection      `toml:"format"`
}

type rawAccountSection struct {
	Template            string               `toml:"template"`
	FilePattern         string               `toml:"file_pattern"`
	Order               string               `toml:"order"`
	EquivalenceMetaKeys *[]string            `toml:"equivalence_meta_keys"`
	Format              rawFormatSection     `toml:"format"`
	Override            []rawAccountOverride `toml:"override"`
}

type rawPriceSection struct {
	Template            string             `toml:"template"`
	FilePattern         string             `toml:"file_pattern"`
	Order               string             `toml:"order"`
	EquivalenceMetaKeys *[]string          `toml:"equivalence_meta_keys"`
	Format              rawFormatSection   `toml:"format"`
	Override            []rawPriceOverride `toml:"override"`
}

type rawTransactionSection struct {
	DefaultStrategy string `toml:"default_strategy"`
	OverrideMetaKey string `toml:"override_meta_key"`
}

type rawFormatSection struct {
	CommaGrouping                     *bool `toml:"comma_grouping"`
	AlignAmounts                      *bool `toml:"align_amounts"`
	AmountColumn                      *int  `toml:"amount_column"`
	EastAsianAmbiguousWidth           *int  `toml:"east_asian_ambiguous_width"`
	IndentWidth                       *int  `toml:"indent_width"`
	BlankLinesBetweenDirectives       *int  `toml:"blank_lines_between_directives"`
	InsertBlankLinesBetweenDirectives *bool `toml:"insert_blank_lines_between_directives"`
}

type rawAccountOverride struct {
	Prefix              string           `toml:"prefix"`
	Template            string           `toml:"template"`
	FilePattern         string           `toml:"file_pattern"`
	Order               string           `toml:"order"`
	TxnStrategy         string           `toml:"txn_strategy"`
	EquivalenceMetaKeys *[]string        `toml:"equivalence_meta_keys"`
	Format              rawFormatSection `toml:"format"`
}

type rawPriceOverride struct {
	Commodity           string           `toml:"commodity"`
	Template            string           `toml:"template"`
	FilePattern         string           `toml:"file_pattern"`
	Order               string           `toml:"order"`
	EquivalenceMetaKeys *[]string        `toml:"equivalence_meta_keys"`
	Format              rawFormatSection `toml:"format"`
}

// Load reads a TOML config from path and converts it to a route.Config.
// Unknown TOML keys, unsupported order / file_pattern values, and
// unsupported transaction strategies are rejected with a descriptive
// error.
func Load(path string) (*route.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: opening %q: %w", path, err)
	}
	defer f.Close()
	return decode(f, path)
}

// LoadIfExists is like Load but returns (nil, nil) when path does not
// exist. Other errors (parse, validation, I/O) propagate normally.
func LoadIfExists(path string) (*route.Config, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("config: stat %q: %w", path, err)
	}
	return Load(path)
}

func decode(r io.Reader, source string) (*route.Config, error) {
	var raw rawConfig
	dec := toml.NewDecoder(r)
	meta, err := dec.Decode(&raw)
	if err != nil {
		return nil, fmt.Errorf("config: decoding %s: %w", source, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) != 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("config: %s: unknown keys: %s", source, strings.Join(keys, ", "))
	}
	cfg, err := convert(raw)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", source, err)
	}
	return cfg, nil
}

func convert(raw rawConfig) (*route.Config, error) {
	cfg := &route.Config{}

	if err := validateOrder(raw.Routes.Account.Order, "[routes.account].order"); err != nil {
		return nil, err
	}
	if err := validateFilePattern(raw.Routes.Account.FilePattern, "[routes.account].file_pattern"); err != nil {
		return nil, err
	}
	cfg.Account = route.AccountSection{
		Template:            raw.Routes.Account.Template,
		FilePattern:         raw.Routes.Account.FilePattern,
		Order:               raw.Routes.Account.Order,
		EquivalenceMetaKeys: derefSlice(raw.Routes.Account.EquivalenceMetaKeys),
		Format:              convertFormat(raw.Routes.Account.Format),
	}

	if err := validateOrder(raw.Routes.Price.Order, "[routes.price].order"); err != nil {
		return nil, err
	}
	if err := validateFilePattern(raw.Routes.Price.FilePattern, "[routes.price].file_pattern"); err != nil {
		return nil, err
	}
	cfg.Price = route.PriceSection{
		Template:            raw.Routes.Price.Template,
		FilePattern:         raw.Routes.Price.FilePattern,
		Order:               raw.Routes.Price.Order,
		EquivalenceMetaKeys: derefSlice(raw.Routes.Price.EquivalenceMetaKeys),
		Format:              convertFormat(raw.Routes.Price.Format),
	}

	if err := validateStrategy(raw.Routes.Transaction.DefaultStrategy, "[routes.transaction].default_strategy"); err != nil {
		return nil, err
	}
	cfg.Transaction = route.TransactionSection{
		DefaultStrategy: raw.Routes.Transaction.DefaultStrategy,
		OverrideMetaKey: raw.Routes.Transaction.OverrideMetaKey,
	}
	cfg.Format = convertFormat(raw.Routes.Format)

	for i, ro := range raw.Routes.Account.Override {
		ctx := fmt.Sprintf("[[routes.account.override]] #%d", i+1)
		if err := validateOrder(ro.Order, ctx+".order"); err != nil {
			return nil, err
		}
		if err := validateFilePattern(ro.FilePattern, ctx+".file_pattern"); err != nil {
			return nil, err
		}
		if err := validateStrategy(ro.TxnStrategy, ctx+".txn_strategy"); err != nil {
			return nil, err
		}
		cfg.AccountOverrides = append(cfg.AccountOverrides, route.AccountOverride{
			Prefix:              ro.Prefix,
			Template:            ro.Template,
			FilePattern:         ro.FilePattern,
			Order:               ro.Order,
			TxnStrategy:         ro.TxnStrategy,
			EquivalenceMetaKeys: derefSlice(ro.EquivalenceMetaKeys),
			HasEqMetaKeys:       ro.EquivalenceMetaKeys != nil,
			Format:              convertFormat(ro.Format),
		})
	}

	for i, ro := range raw.Routes.Price.Override {
		ctx := fmt.Sprintf("[[routes.price.override]] #%d", i+1)
		if err := validateOrder(ro.Order, ctx+".order"); err != nil {
			return nil, err
		}
		if err := validateFilePattern(ro.FilePattern, ctx+".file_pattern"); err != nil {
			return nil, err
		}
		cfg.CommodityOverrides = append(cfg.CommodityOverrides, route.CommodityOverride{
			Commodity:           ro.Commodity,
			Template:            ro.Template,
			FilePattern:         ro.FilePattern,
			Order:               ro.Order,
			EquivalenceMetaKeys: derefSlice(ro.EquivalenceMetaKeys),
			HasEqMetaKeys:       ro.EquivalenceMetaKeys != nil,
			Format:              convertFormat(ro.Format),
		})
	}

	return cfg, nil
}

func convertFormat(f rawFormatSection) route.FormatSection {
	return route.FormatSection{
		CommaGrouping:                     f.CommaGrouping,
		AlignAmounts:                      f.AlignAmounts,
		AmountColumn:                      f.AmountColumn,
		EastAsianAmbiguousWidth:           f.EastAsianAmbiguousWidth,
		IndentWidth:                       f.IndentWidth,
		BlankLinesBetweenDirectives:       f.BlankLinesBetweenDirectives,
		InsertBlankLinesBetweenDirectives: f.InsertBlankLinesBetweenDirectives,
	}
}

func derefSlice(p *[]string) []string {
	if p == nil {
		return nil
	}
	out := make([]string, len(*p))
	copy(out, *p)
	return out
}

// validateOrder currently accepts only "ascending". The empty string
// means "inherit" and is always accepted.
func validateOrder(order, where string) error {
	if order == "" {
		return nil
	}
	switch strings.ToLower(order) {
	case "ascending":
		return nil
	case "descending", "append":
		return fmt.Errorf("%s: %q is not yet supported (only \"ascending\")", where, order)
	default:
		return fmt.Errorf("%s: %q is not a valid order (must be \"ascending\")", where, order)
	}
}

// validateFilePattern currently accepts only "YYYYmm".
func validateFilePattern(pattern, where string) error {
	if pattern == "" {
		return nil
	}
	switch pattern {
	case "YYYYmm":
		return nil
	case "YYYY", "YYYYmmdd":
		return fmt.Errorf("%s: %q is not yet supported (only \"YYYYmm\")", where, pattern)
	default:
		return fmt.Errorf("%s: %q is not a valid file pattern", where, pattern)
	}
}

// validateStrategy accepts the four documented values; unset means "use
// the configured default strategy", which Decide does not yet consume.
func validateStrategy(s, where string) error {
	if s == "" {
		return nil
	}
	switch s {
	case "first-posting", "last-posting", "first-debit", "first-credit":
		return nil
	default:
		return fmt.Errorf("%s: %q is not a valid transaction strategy", where, s)
	}
}
