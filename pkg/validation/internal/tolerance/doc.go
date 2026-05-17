// Package tolerance derives per-currency residual tolerances for
// transaction balancing and balance assertions.
//
// The units-based rule: for each residual currency, the tolerance is
// derived from the maximum exponent (i.e. *least* precise
// least-significant digit) among contributing explicit postings in
// that currency, scaled by the ledger option tolerance_multiplier.
// Setting the deprecated alias inferred_tolerance_multiplier is also
// accepted and reaches the same canonical slot via a write redirect
// at parse time. This matches upstream beancount's
// infer_tolerances called with mode="max", which the upstream booking
// pass uses for balance verification because balance checks favor the
// looser/larger tolerance: a high-precision posting alongside a
// coarsely-written one should not tighten the bound below what the
// user authored. When no posting contributes to a currency (e.g. it
// arose purely from a price conversion), the tolerance for that
// currency is zero unless inferred_tolerance_default provides an entry
// for it (see "Per-currency default" below).
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
//
// Per-currency default: when a residual currency has no contributing
// posting (e.g. it arose purely from a price conversion), Infer
// consults the ledger option inferred_tolerance_default before falling
// back to zero. The precedence chain is: posting-level inference >
// per-currency inferred_tolerance_default entry > zero. A zero or
// negative entry in inferred_tolerance_default is treated as absent
// and falls through to zero; this avoids treating a likely typo as an
// intentional "exact match" requirement.
//
// This package diverges from upstream beancount's get_balance_tolerance
// (beancount/ops/balance.py): upstream consults the per-currency
// inferred_tolerance_default for balance assertions whose asserted
// amount has zero fractional digits, even when posting-level inference
// is computable. This package does not replicate that nuance: the
// precedence chain above is applied uniformly across Infer,
// ForAmount, and ForBalanceAssertion — posting-level inference is
// never overridden by the per-currency default. This divergence is
// intentional; it can be revisited in a follow-up if a real consumer
// reports the difference.
package tolerance
