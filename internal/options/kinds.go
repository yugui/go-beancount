package options

import (
	"fmt"
	"strings"

	"github.com/cockroachdb/apd/v3"
)

// parseStringOption is the identity parser.
func parseStringOption(raw string) (any, error) {
	return raw, nil
}

// parseBoolOption accepts TRUE/FALSE case-insensitive.
func parseBoolOption(raw string) (any, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "TRUE":
		return true, nil
	case "FALSE":
		return false, nil
	}
	return nil, fmt.Errorf("expected TRUE or FALSE, got %q", raw)
}

// parseDecimalOption parses a decimal literal.
func parseDecimalOption(raw string) (any, error) {
	d, _, err := apd.BaseContext.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return d, nil
}

// parseCurrencyListItem trims and rejects empty entries.
func parseCurrencyListItem(raw string) (any, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("currency must not be empty")
	}
	return s, nil
}
