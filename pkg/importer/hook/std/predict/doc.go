// Package predict is a learning classifier hook. It registers a [hook.Factory]
// under the kind "predict"; each factory call loads an existing ledger and
// produces one fully-configured [*Hook] that infers the counter account of
// single-leg transactions from the most similar prior entries.
//
// # Model
//
// Construction indexes the configured ledger into a k-NN / TF-IDF model — there
// is no training step beyond indexing, so the hook suits per-invocation CLI use.
// Each historical balanced 2-posting transaction yields a labeled example: the
// import-source leg (Assets/Liabilities) is the known side and the counter leg
// its label. Text (payee, narration, string metadata), the known account, and
// the amount sign become namespaced, weighted tokens; the absolute amount is
// matched separately as a re-ranking bonus for recurring entries. Each
// neighbor's contribution to ranking and voting decays exponentially with the
// age of its example date relative to the query, with a configurable half-life
// (default one year). Examples whose label is not currently open are dropped,
// so a closed account is never predicted.
//
// # Configuration
//
//	[[hook]]
//	kind = "predict"
//	name = "default"
//	ledger = "/path/to/main.beancount"   # required
//	min_confidence = 0.30                  # default 0.30
//	min_margin = 0.10                      # default 0.10
//	k = 10                                 # predictor neighbors (default 10)
//	exact_amount_bonus = 0.25              # default 0.25
//	min_support = 1                        # default 1
//	recency_half_life_days = 365           # default 365; 0 disables decay
//	  [hook.fields]                        # per-field weight overrides
//	  payee = 3.0
//	  narration = 1.5
//	  metadata = 0.5
//	  account = 0.75
//	  sign = 0.5
//
// # Apply semantics
//
//   - Non-Transaction directives, and transactions that are not single-leg with
//     an amount, pass through unchanged.
//   - A single-leg transaction is classified. When the prediction clears both
//     min_confidence and min_margin, a counterpart posting with the predicted
//     account is appended via
//     [github.com/yugui/go-beancount/pkg/importer/importerutil.BalanceWith].
//     Otherwise the transaction is left unbalanced and a [DiagAbstain] Warning
//     is emitted, so a downstream validation surfaces it to the user rather than
//     a low-confidence guess silently balancing it.
//
// # Concurrency
//
// A Hook's state is frozen at construction. Apply is safe for concurrent
// invocation on the same value with no external synchronisation.
package predict

import "github.com/yugui/go-beancount/pkg/importer/hook"

func init() {
	hook.RegisterFactory("predict", hook.FactoryFunc(newHook))
}
