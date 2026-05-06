// Package routeconfig loads a beanfile TOML routing configuration into a
// route.Config. Decoding is strict: unknown keys, unsupported order /
// file-pattern values, and unsupported transaction strategies are
// rejected at load time.
package routeconfig

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/yugui/go-beancount/pkg/distribute/route"
)

// Load reads a TOML config from path and converts it to a route.Config.
// Unknown TOML keys, unsupported order / file_pattern values, and
// unsupported transaction strategies are rejected with a descriptive
// error.
func Load(path string) (*route.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("routeconfig: opening %q: %w", path, err)
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
		return nil, fmt.Errorf("routeconfig: stat %q: %w", path, err)
	}
	return Load(path)
}

func decode(r io.Reader, source string) (*route.Config, error) {
	var cfg route.Config
	dec := toml.NewDecoder(r)
	meta, err := dec.Decode(&cfg)
	if err != nil {
		return nil, fmt.Errorf("routeconfig: decoding %s: %w", source, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) != 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("routeconfig: %s: unknown keys: %s", source, strings.Join(keys, ", "))
	}
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("routeconfig: %s: %w", source, err)
	}
	return &cfg, nil
}

func validate(cfg *route.Config) error {
	if err := validateOrder(cfg.Routes.Account.Order, "[routes.account].order"); err != nil {
		return err
	}
	if err := validateFilePattern(cfg.Routes.Account.FilePattern, "[routes.account].file_pattern"); err != nil {
		return err
	}
	if err := validateOrder(cfg.Routes.Price.Order, "[routes.price].order"); err != nil {
		return err
	}
	if err := validateFilePattern(cfg.Routes.Price.FilePattern, "[routes.price].file_pattern"); err != nil {
		return err
	}
	if err := validateStrategy(cfg.Routes.Transaction.DefaultStrategy, "[routes.transaction].default_strategy"); err != nil {
		return err
	}
	for i, ro := range cfg.Routes.Account.Overrides {
		ctx := fmt.Sprintf("[[routes.account.override]] #%d", i+1)
		if err := validateOrder(ro.Order, ctx+".order"); err != nil {
			return err
		}
		if err := validateFilePattern(ro.FilePattern, ctx+".file_pattern"); err != nil {
			return err
		}
		if err := validateStrategy(ro.TxnStrategy, ctx+".txn_strategy"); err != nil {
			return err
		}
	}
	for i, ro := range cfg.Routes.Price.Overrides {
		ctx := fmt.Sprintf("[[routes.price.override]] #%d", i+1)
		if err := validateOrder(ro.Order, ctx+".order"); err != nil {
			return err
		}
		if err := validateFilePattern(ro.FilePattern, ctx+".file_pattern"); err != nil {
			return err
		}
	}
	return nil
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
