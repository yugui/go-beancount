# `predict` hook — design rationale

The `predict` hook (`pkg/importer/hook/std/predict`) completes the counter
account of unbalanced single-leg transactions produced by importers, learning
from the user's existing ledger. This note records the cross-cutting design
decisions and the alternatives that were rejected — the contract itself lives in
the package godoc and the exported symbols.

## Constraint that shaped everything: per-invocation, no heavy pre-training

`beanimport` runs once per file from the command line. The model must be built,
used, and discarded within a single invocation, over a personal-scale ledger
(10³–10⁵ transactions). This rules out anything with a meaningful training cost
and favors memory-based methods whose "training" is just indexing.

## Learning method: k-NN / TF-IDF, not SVM

upstream `smart_importer` (beangulp) uses an sklearn SVM pipeline. Rejected:
an SVM re-trains (iterative optimization) on every invocation — the opposite of
the constraint above — and Go has no mature SVM library, so it would mean a
hand-rolled SGD with its own tuning surface. On personal-scale data its accuracy
edge over simpler methods is not assured.

Chosen: **k-NN over TF-IDF cosine.** Indexing is the only "training"; it directly
realizes "predict from similar past entries" and is interpretable (the matched
neighbor is reportable as evidence). The `Predictor` interface is the extension
point — Naive Bayes or an external learner can be added later without touching
the hook.

## Tokenization: character n-grams for CJK, not a morphological analyzer

East-Asian text has no whitespace word boundaries, so naive splitting fails.
Rejected as the default: a Japanese morphological analyzer (e.g. kagome) bundles
a multi-megabyte dictionary into the binary and is Japanese-only — it would not
help Chinese or Korean while bloating every build.

Chosen: NFKC-normalize and case-fold, split into maximal CJK / non-CJK script
runs, emit **character n-grams (unigram+bigram) for CJK runs** and UAX #29 word
segmentation (`clipperhouse/uax29`) for non-CJK runs. This is dictionary-free,
language-agnostic across CJK, and strong on the short, repetitive merchant
strings that dominate transaction text. CJK prolonged-sound/iteration marks
(`ー`, `々`, …) are category Lm and absent from the script range tables, so they
are explicitly bound into adjacent CJK runs; otherwise common words like コーヒー
would fragment and their bigrams never form. The `Tokenizer` interface lets a
user plug in a morphological analyzer when they want one.

## Features: namespaced and weighted, amount kept out of band

Tokens are namespaced by field (`payee:`, `narr:`, `meta.<k>:`, `acct:`,
`sign:`) with per-field weights, so the same term means different things in
different fields and fields can be tuned independently. Account features expand
to **every ancestor prefix** (`acct:Assets`, `acct:Assets:Bank`, …) so a new
sub-account inherits its parent's learned priors while the full path still
distinguishes exact matches; IDF discounts the broad prefixes automatically.

The amount magnitude is **not** a bag-of-words token — it would explode the
vocabulary and overfit. Instead `Features` carries the absolute amount and
currency out of band, used only as an exact-match re-ranking bonus (recurring
subscriptions/rent land on their usual account), plus a single `sign:` direction
token. Crucially the reported confidence is pure text-cosine, so an
exact-amount-only match with low text similarity does not clear the threshold —
the bonus re-ranks already-similar candidates rather than fabricating confidence.

## Training pairs: import-source orientation

The importer always knows the bank/card leg and predicts the other. So a
balanced 2-posting transaction yields one example with the Assets/Liabilities
leg as the known side. Emitting both orientations always was rejected: the
reverse direction never occurs at query time and only dilutes IDF and adds
noise. Both orientations are emitted only when neither side is source-like
(a transfer, or an income/expense-only entry) where "source" is undefined.

## No df-pruning

Considered (and initially planned) for noise removal. Dropped: IDF already
softly discounts common tokens, and the tokenizer already collapses
numeric/reference-number noise to a single `#num` placeholder. In a personal
ledger many merchants appear once, and those **singleton tokens are exactly what
makes an exact once-seen-merchant match work in k-NN** — a low document-frequency
cut would throw them away. The remaining noise df-pruning would catch is
marginal, so it is omitted.

## Abstain on low confidence, never silently mis-balance

Filling a wrong counter account silently balances a bad guess, which is worse
than leaving the transaction unbalanced. So the hook fills only when both
**confidence** (best-match cosine) and **margin** (weighted-vote gap between the
top two accounts) clear configurable thresholds (defaults 0.30 / 0.10, retunable
via the leave-one-out eval). Otherwise it leaves the transaction single-leg and
emits a `predict-abstain` Warning, letting downstream validation surface it to a
human. The two metrics are orthogonal: confidence guards weak similarity, margin
guards a close call between plausible accounts.

## Dropped: explicit domain-constraint boost

An explicit rule to boost, say, `Expenses:*` for a credit-card charge was
considered and dropped as redundant: the `sign:` and `acct:` features already let
the predictor learn direction-appropriate accounts from the data, and a
hand-coded boost would risk fighting that learned signal. Revisit only if eval
shows systematic direction errors.

## Closed accounts are never predicted

Realized at the corpus level: training examples whose label is not currently
open (per the ledger's Open/Close directives) are dropped before indexing, so a
closed account can never be a candidate.

## Determinism

Re-importing the same input must yield byte-identical output (version-controlled
ledgers depend on it). All stages avoid observable map-iteration order: feature
terms are sorted, tie-breaks are explicit (equal vote → more recent example date
→ account-name byte order). Recency is used only as a tie-breaker, not as a
scoring weight (recency weighting is left to a future iteration).
