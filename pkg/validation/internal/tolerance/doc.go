// Package tolerance derives per-currency residual tolerances for
// transaction balancing and balance assertions.
//
// The units-based rule: for each residual currency, the tolerance is
// derived from the maximum exponent (i.e. *least* precise
// least-significant digit) among contributing explicit postings in
// that currency, scaled by the ledger option
// inferred_tolerance_multiplier. This matches upstream beancount's
// infer_tolerances called with mode="max", which the upstream booking
// pass uses for balance verification because balance checks favor the
// looser/larger tolerance: a high-precision posting alongside a
// coarsely-written one should not tighten the bound below what the
// user authored. When no posting contributes to a currency (e.g. it
// arose purely from a price conversion), the tolerance for that
// currency is zero.
//
// The cost-based augmentation: when the ledger option
// infer_tolerance_from_cost is enabled, each posting with a cost
// spec additionally contributes |units| * (multiplier * 10^costExp)
// to its cost currency. Per currency the largest such contribution
// is retained, then combined with the units-based tolerance by
// taking the per-currency maximum of the two values. This formula
// intentionally differs from upstream beancount, which propagates
// units-number imprecision through cost magnitude rather than
// cost-number imprecision through units count; see the inline comment
// in Infer for the rationale and the comparison.
//
// ForAmount is the single-amount variant of the units-based rule,
// used by Balance-directive tolerance inference.
package tolerance
