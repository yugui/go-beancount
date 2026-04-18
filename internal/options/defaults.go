package options

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
)

// defaultRegistry is the package-wide registry used by Parse. It is
// initialized once with the set of options consumed by validation.
var defaultRegistry = newDefaultRegistry()

// newDefaultRegistry constructs the package-default registry.
func newDefaultRegistry() *registry {
	r := newRegistry()
	must(r.register(spec{
		key:          "operating_currency",
		kind:         kindStringList,
		parse:        parseCurrencyListItem,
		defaultValue: []string(nil),
	}))
	// inferred_tolerance_multiplier: decimal, default 0.5. Multiplies the
	// per-exponent unit when inferring tolerance from an amount's precision.
	must(r.register(spec{
		key:          "inferred_tolerance_multiplier",
		kind:         kindDecimal,
		parse:        parseDecimalOption,
		defaultValue: apd.New(5, -1),
	}))
	// infer_tolerance_from_cost: bool, default false. When true, postings
	// with an explicit cost spec also contribute a per-cost-currency
	// tolerance of |units| * (multiplier * 10^costExp).
	must(r.register(spec{
		key:          "infer_tolerance_from_cost",
		kind:         kindBool,
		parse:        parseBoolOption,
		defaultValue: false,
	}))
	return r
}

// must panics if err is non-nil. It is used to surface programming errors
// from registry initialization, which happens once at package load.
func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("options: default registry initialization failed: %v", err))
	}
}
