package route

import (
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
)

// longestAccountOverride returns the override whose Prefix is the longest
// segment-aligned prefix of a. Ties (multiple overrides at the same
// segment depth match) resolve in declared order: the first one wins.
// Returns nil when no override matches.
func longestAccountOverride(overrides []AccountOverride, a ast.Account) *AccountOverride {
	if len(overrides) == 0 {
		return nil
	}
	parts := a.Parts()
	bestDepth := -1
	var best *AccountOverride
	for i := range overrides {
		o := &overrides[i]
		if o.Prefix == "" {
			continue
		}
		prefixParts := strings.Split(o.Prefix, ":")
		if !segmentPrefix(prefixParts, parts) {
			continue
		}
		if len(prefixParts) > bestDepth {
			bestDepth = len(prefixParts)
			best = o
		}
	}
	return best
}

// segmentPrefix reports whether prefix is a segment-wise prefix of parts
// (i.e. parts starts with the same segments). An empty prefix matches
// every account; an empty parts matches only an empty prefix.
func segmentPrefix(prefix, parts []string) bool {
	if len(prefix) > len(parts) {
		return false
	}
	for i, p := range prefix {
		if parts[i] != p {
			return false
		}
	}
	return true
}

// commodityOverrideFor returns the override whose Commodity exactly
// matches commodity. The first match in declared order wins; later
// duplicates are ignored.
func commodityOverrideFor(overrides []CommodityOverride, commodity string) *CommodityOverride {
	for i := range overrides {
		if overrides[i].Commodity == commodity {
			return &overrides[i]
		}
	}
	return nil
}
