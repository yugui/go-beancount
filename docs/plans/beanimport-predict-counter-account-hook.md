# beanimport `predict` hook — 既存ledger学習による counter account 補完

## Goal

`cmd/beanimport` 向けに、既存 beancount 台帳に蓄積された類似取引から
unbalanced (single-leg) transaction の counter account を学習推論して補完する
新 hook kind `predict` を `pkg/importer/hook/std/predict/` に追加する。
CLI 都度実行のため invocation 時に台帳から軽量にモデルを組み、推論し、破棄する。

## Scope

### 含む (v1)
- 新 hook kind `predict`（既存 `classify` hook と同じ作法、hook chain で共存）。
- `Tokenizer` interface + 既定実装（NFKC → script-run 分割 → CJK 文字 n-gram /
  非CJK uax29 word segmentation、数値/参照番号正規化）。
- `Predictor` interface + 既定実装（k-NN / TF-IDF cosine）。
- namespace + フィールド重み feature 表現、金額符号 token + exact 一致ボーナス。
- df-pruning によるノイズ token 除去。
- 棄権 + Warning diagnostic（margin 棄権 / Open フィルタ / source・符号ドメイン制約 /
  determinism / min-support 込み）。
- TOML config（ledger path, weights, 閾値等）。
- table test + leave-one-out eval harness + e2e。

### 含まない (v1、interface のみ開放)
- Naive Bayes predictor、kagome 等形態素解析 tokenizer plugin、
  cross-script 表記揺れ alias テーブル、recency weighting、log-bucket 金額 token、
  モデルキャッシュ、n-leg(>2) txn からの学習。

## Alternatives discussed（高レベル、ユーザー合意済み）

- **学習方式**: SVM(smart_importer 路線) は invocation 毎の iterative 学習が
  「軽い学習」要件に反し Go に成熟ライブラリも無いため却下。memory-based k-NN を既定に、
  NB は interface で後続開放。
- **CJK tokenization**: kagome 同梱は数十MB辞書 + JA専用のため却下。辞書ゼロ・CJK横断の
  文字 n-gram を既定、形態素解析は plugin 開放。
- **feature**: plain text 結合は却下、namespace + フィールド重みを採用。
- **金額**: bag-of-words への混入は却下、符号 token + kNN exact 一致ボーナスを採用。
- **stop word**: 言語別 stop list 同梱は却下、df-pruning + 数値正規化 + IDF 任せを採用。
- **表記揺れ**: 台帳自己整合でカバーされるため v1 見送り。
- **ledger 指定**: hook config TOML キー。
- **低 confidence**: 棄権 + Warning（誤 balance 回避）。

## Recommended approach

新パッケージ `pkg/importer/hook/std/predict/` を `classify` のレイアウトに倣って構築。
`Tokenizer` / `Predictor` の 2 interface を拡張点とし、既定実装を同梱。Hook 本体は
single-leg txn を走査し、Predictor で counter account を推論、閾値/margin を満たせば
`importerutil.BalanceWith` で 2-leg 化、満たさなければ single-leg のまま Warning。
ledger は `loader.LoadFile` で読み学習例を抽出。`init()` + `hook.RegisterFactory` で登録。

### 再利用する既存資産
- `importerutil.BalanceWith`（`pkg/importer/importerutil/balancewith.go`）
- `loader.LoadFile` / `ast.Ledger`（`pkg/loader/loader.go`, `pkg/ast/ledger.go`）
- NFKC 正規化方針（`pkg/distribute/dedup/equality.go` `normalizeFreeText`）
- `clipperhouse/uax29/v2`（go.sum 既存 indirect）
- hook 登録パターン（`pkg/importer/hook/std/classify/doc.go`）
- test 規約（`classify_test.go` の go-cmp + Decimal/Time Comparer、`testdata/` glob）

---

## Step 1 — Tokenizer interface + 既定実装

### Functional requirements
- 文字列を token 列へ変換する `Tokenizer` interface を定義（plugin 拡張点）。
- 既定実装: NFKC 正規化 + casefold → CJK/非CJK script-run 分割 →
  CJK run は文字 n-gram(bigram + unigram)、非CJK run は uax29 word segmentation →
  全 digit / 長い英数字 ID token の正規化・除去。
- field 名を受け取り namespace prefix を付与できる形にする（feature 抽出と協調）。

### Modules
- `pkg/importer/hook/std/predict/tokenizer.go`（interface + 既定実装）
- `pkg/importer/hook/std/predict/tokenizer_test.go`

### Verification
- table test: CJK のみ / 非CJK のみ / 混在 / NFKC 揺れ（全角半角）/ 数値・参照番号 /
  空文字 の各ケースで期待 token 列。

### Quality requirements
- 決定的（同入力 → 同出力、順序安定）。exported symbol に doc comment（CLAUDE.md 準拠）。
- 既存 NFKC 方針との一貫性。uax29 / x/text を Bazel deps に追加。

### Detailed Design

#### Contract  (LOCKED)

Package `predict` at `pkg/importer/hook/std/predict/tokenizer.go`.

```go
// Tokenizer converts a free-text field value into an ordered, deterministic
// token slice for feature extraction. It is the extension point for
// alternative tokenization strategies (e.g. a morphological-analyzer plugin);
// the default implementation is dictionary-free and language-agnostic.
//
// Tokenize MUST be a pure function of its input: equal inputs yield slices
// that are equal element-wise and in the same order. Implementations must be
// safe for concurrent use. Tokens are namespace-agnostic raw terms; the caller
// is responsible for any field-namespacing or weighting.
type Tokenizer interface {
    // Tokenize returns the tokens of s in their order of first contribution
    // from the input. It returns a nil or empty slice for empty or
    // token-free input; it never returns an error.
    Tokenize(s string) []string
}

// NewDefaultTokenizer returns the dictionary-free default Tokenizer. With no
// options it emits, per script run: character bigrams plus unigrams for CJK
// runs, and Unicode word-segmented (UAX #29) lowercase tokens for non-CJK
// runs, after NFKC normalization and case folding. Pure-digit and long
// alphanumeric reference tokens collapse to a single placeholder token.
// The returned Tokenizer is immutable and safe for concurrent use.
func NewDefaultTokenizer(opts ...Option) Tokenizer

type Option func(*tokenizerConfig)

// WithCJKNGram sets contiguous character n-gram sizes for CJK runs (default {1,2}).
func WithCJKNGram(sizes ...int) Option
// WithNumberPlaceholder sets the placeholder for digit/reference tokens
// (default "#num"; "" drops such tokens entirely).
func WithNumberPlaceholder(tok string) Option
// WithDigitIDMinLen sets the min rune length at which a digit-bearing
// alphanumeric token is collapsed to the placeholder (default 6).
func WithDigitIDMinLen(n int) Option
```

**Determinism (LOCKED):** for a fixed option set, `Tokenize` is pure — no
dependence on map iteration, locale, scheduling, or global state. Token order
follows left-to-right script-run consumption.

**Cross-step coupling (LOCKED):** `Tokenize` takes only text and returns raw,
un-namespaced `[]string`. Namespacing/weighting is Step 2's job. This is the
single contract point Step 2 binds to.

#### Suggested Internals  (ADVISORY)

```go
type tokenizerConfig struct { cjkNGram []int; numPlace string; digitIDMin int }
type defaultTokenizer struct{ cfg tokenizerConfig }
```

Pipeline:
1. **NFKC + casefold**: `norm.NFKC.String(s)` (as `dedup.normalizeFreeText`) then
   `cases.Fold().String(s)` (`golang.org/x/text/cases`; stateless, concurrency-safe).
   NFKC before fold so fullwidth/compat forms collapse first.
2. **script-run segmentation**: classify each rune CJK / separator / other; flush
   maximal same-class runs. CJK via `unicode.In(r, cjkRanges...)` with
   `unicode.Han, Hiragana, Katakana, Hangul, Bopomofo`. CJK punctuation and
   `unicode.IsSpace/IsPunct/IsSymbol` → separator (run boundary, not emitted).
   Fullwidth Latin/digits already folded to ASCII → `classOther`.
3. **CJK run → n-gram**: for each size in `cfg.cjkNGram` ascending, emit every
   contiguous window (unigrams before bigrams). e.g. 東京駅 → 東,京,駅,東京,京駅.
4. **non-CJK run → uax29**: `github.com/clipperhouse/uax29/v2/words` segmenter
   (`words.FromString(run)` / `Next()` / `Value()`), keep word-class segments.
   IMPLEMENTER: confirm exact v2.2.0 iterator method names against module source
   when wiring Bazel dep; Contract does not depend on the call shape.
5. **digit/reference handling**: all-digit token → `numPlace` (or drop if "");
   token len ≥ `digitIDMin` that is alphanumeric AND contains a digit → `numPlace`;
   short digit-bearing tokens kept. TF meaningful → no de-dup of placeholders here.

Helpers (unexported): `normalizeFold`, `classOf`, `splitRuns`, `cjkNGrams`,
`wordTokens`, `canonNumeric`. No namespacing here — bare terms only.

#### Recommendation + rationale
Raw-`[]string` field-agnostic Tokenizer keeps Step 1 a single deterministic,
table-testable unit; NFKC reuses the repo's normalization; `cases.Fold()` gives
correct cross-script case-insensitive matching and is already in the dep closure;
namespacing deferred to Step 2 respects layering. Bazel: pulls
`x/text/unicode/norm`, `x/text/cases`, `uax29/v2/words`; `bazel run //:gazelle`.

**RESOLVED (orchestrator + user):**
- Tokenizer interface = `Tokenize(s string) []string`（field 非依存、raw token。
  namespacing は Step 2）。kagome 等 plugin はこの 1 メソッドのみ実装。
- 数値/参照番号 = `#num` placeholder 既定（全 digit、または len≥6 で digit を含む
  英数字 ID）。`WithNumberPlaceholder("")` で drop に切替可。Step 5 eval で再調整余地。

---

## Step 2 — feature 抽出 + 学習例抽出 (train)

### Functional requirements
- transaction → namespaced token 列 + 金額情報（符号・絶対値）への変換。
  namespace: `payee:` `narr:` `meta.<k>:` `acct:`(known account) `sign:`。
- フィールド別 weight を適用可能な形（token に重みを載せる or フィールド別 token 群を返す）。
- ledger(`*ast.Ledger`) から balanced 2-posting txn を走査し、順序対
  (known posting, other posting) → (features(txn, known), other.Account) の学習例を抽出。
- Open/Close を集計し「現在 Open な account 集合」を提供（Step 4 のフィルタ用）。

### Modules
- `pkg/importer/hook/std/predict/features.go`
- `pkg/importer/hook/std/predict/train.go`
- 対応する `_test.go`

### Verification
- table test: 各 namespace の token 生成、符号/金額抽出、2-posting からの学習例 2 件生成、
  n-leg/unbalanced txn のスキップ、Open account 集合の算出。

### Quality requirements
- 決定的。`Tokenizer` interface 経由でトークン化（Step 1 と疎結合）。
- 学習例抽出は exported な型/関数の最小面で表現（test は観測可能挙動を対象）。

### Detailed Design

#### Contract  (LOCKED — Step 3/4 bind to these types)

Package `predict`, files `features.go` + `train.go`.

```go
type Sign int8
const ( SignZero Sign = iota; SignDebit; SignCredit )

// Term: one namespaced feature token + accumulated weight (>0).
// Token = field prefix (payee: / narr: / meta.<k>: / acct: / sign:) + raw term.
// Weight = sum of per-occurrence field weights within one transaction.
type Term struct { Token string; Weight float64 }

// Features: deterministic namespaced weighted-token view of a txn from the
// vantage of one known posting. Terms sorted by Token, deduped (weights summed).
// Amount signal out-of-band: AmountAbs/Currency/Sign carried separately so the
// magnitude does not pollute the text vector (only the derived sign: token is in Terms).
type Features struct {
    Terms     []Term
    AmountAbs *apd.Decimal // |known posting amount|, nil if absent
    Currency  string
    Sign      Sign
}

type FieldWeights struct { Payee, Narration, Metadata, Account, Sign float64 }
func DefaultFieldWeights() FieldWeights // Payee 3.0, Narration 1.5, Metadata <see RESOLVED>, Account 0.75, Sign 0.5

// ExtractFeatures builds Features for txn from the posting at knownIdx
// (the account whose counterpart is predicted). Tokenizes Payee/Narration/
// MetaString via tok, namespaces + weights per fw, emits acct: <see RESOLVED
// granularity> + one sign: token. Panics if knownIdx out of range. No aliasing
// of txn memory; deterministic.
func ExtractFeatures(txn *ast.Transaction, knownIdx int, tok Tokenizer, fw FieldWeights) Features

// Example: one supervised instance. Date available to Step 3 for recency tie-break.
type Example struct { Features Features; Label ast.Account; Date time.Time }

// ExtractExamples walks l in canonical order. Eligible txn (v1) = exactly 2
// postings, both Amount non-nil (structural; no net-zero check). Orientation
// per RESOLVED policy. Other shapes skipped. Deterministic order (Ledger.All(),
// posting[0]-known first). nil when no eligible txn.
func ExtractExamples(l *ast.Ledger, tok Tokenizer, fw FieldWeights) []Example

// OpenAccounts: set of accounts Open and not Closed as of end of l, folding
// Open/Close in canonical order. Empty non-nil map for nil/empty ledger.
func OpenAccounts(l *ast.Ledger) map[ast.Account]bool
```

**Namespacing (LOCKED):** prefixes exactly `payee:` `narr:` `meta.<k>:` `acct:`
`sign:`. `sign:` ∈ {sign:debit, sign:credit, sign:zero}. Step 3 must tolerate
unknown prefixes (forward-compat). **Determinism (LOCKED):** Terms sorted byte
order, metadata keys sorted before tokenization, example order follows Ledger.All();
no map-iteration order observable.

#### Suggested Internals (ADVISORY)
- eligibility: `len==2 && both Amount!=nil`（pkg/inventory は使わない）。
- `isSourceLike(a)`: root==Assets||Liabilities。
- sign: `apd.Decimal.Sign()`（nil→SignZero）。AmountAbs: `apd.BaseContext.Abs` で新規確保（aliasしない）。
- 蓄積: 内部 `map[string]float64` で namespaced token を合算 → sorted `[]Term` に flush。
- metadata: `MetaString` のみ、key を sort してから、prefix `meta.<k>:`。Tags/Links/その他 kind は v1 対象外。
- OpenAccounts: `l.All()` 一巡、Open→set、Close→delete。

#### Alternatives & Recommendation
(a) orientation, (b) acct: 粒度, (c) []Term vs map, (d) balance厳密性 — planner は
低ノイズ側（source-side-only+fallback / ancestor-prefix / []Term / structural）を推奨。
詳細は planner 出力（本セクションで要約）。

**RESOLVED (orchestrator + user):**
- orientation = **source-side 優先 + fallback**: 2-posting txn で root が Assets/
  Liabilities の posting を known とし 1 例。両方 source-like（振替）or どちらも違う
  （Income↔Expenses 等）の時のみ両方向 2 例。
- acct: 粒度 = **全 ancestor prefix**（acct:Assets, acct:Assets:Bank,
  acct:Assets:Bank:Checking …）。
- `DefaultFieldWeights.Metadata = 0.5`（MetaString を低 weight で使用。config で 0 可）。
- (c) Features=[]Term sorted/dedup, (d) structural eligibility は orchestrator 判断で lock。

---

## Step 3 — k-NN / TF-IDF Predictor + 既定実装

### Functional requirements
- `Predictor` interface（学習済みモデル、推論で account 候補 + confidence + evidence を返す）。
- 既定実装: 学習例から TF-IDF ベクトル化（df-pruning 込み）、クエリと cosine 類似度、
  距離重み付き k-NN 投票で account を集計。
- exact 金額一致を類似度ボーナスとして加算。min-support による低頻度 account の減点。
- top-1 confidence と top-1/top-2 margin を返す（Hook 側で閾値判定）。
- evidence（根拠 txn / 類似度）を返し diagnostic 提示に使える。

### Modules
- `pkg/importer/hook/std/predict/predictor.go`（interface + kNN 実装 + TF-IDF）
- 対応する `_test.go`

### Verification
- table test: 明確分離した学習例で top-1 一致、df-pruning 効果、exact 金額ボーナス、
  margin 算出、min-support 減点、空コーパスでの abstain。

### Quality requirements
- 決定的 tie-break（account 名辞書順 + recency）。
- TF-IDF/cosine の数値は apd 不要（float64 で可、確率でなく順序が本質）。

### Detailed Design

#### Contract (LOCKED — Step 4 binds to these types)

Package `predict`, file `predictor.go`.

```go
// Predictor infers the counter account for a query Features built from the
// import's single known posting. It is the extension point for alternative
// learners (e.g. Naive Bayes). The default is k-NN over TF-IDF cosine.
type Predictor interface {
    // Predict returns the best counter-account candidate and ok=true, or
    // ok=false when no basis exists (empty corpus or query shares no in-vocab
    // term). The caller (hook) applies confidence/margin thresholds.
    Predict(q Features) (Prediction, bool)
}

// Prediction is the predictor's verdict for one query.
// Confidence and Margin are both in [0,1] and orthogonal: Confidence answers
// "is the closest past match similar enough?" (max cosine over the winning
// account's neighbors); Margin answers "is the winner clearly ahead?"
// (normalized weighted-vote gap between the top-1 and top-2 accounts).
type Prediction struct {
    Account    ast.Account
    Confidence float64
    Margin     float64
    Evidence   Evidence
}

// Evidence describes the closest supporting neighbor, for diagnostics.
type Evidence struct {
    Score float64   // cosine similarity of the closest supporting example
    Date  time.Time // that example's transaction date
}

func NewKNNPredictor(examples []Example, opts ...KNNOption) Predictor

type KNNOption func(*knnConfig)
func WithK(k int) KNNOption                  // neighbors considered (default 10)
func WithExactAmountBonus(b float64) KNNOption // sim bonus on exact |amount|+currency match (default 0.25)
func WithMinSupport(n int) KNNOption         // min examples for an account to be a candidate (default 1)
```

#### Suggested Internals (ADVISORY)
- **Vectorize**: vocab + df over all examples. `idf(t)=log((1+N)/(1+df))+1` (smoothed).
  Each example/query vector = map token→(Term.Weight × idf), then L2-normalize.
  Field weights already folded into Term.Weight (Step 2), so they scale similarity.
  No df-pruning (RESOLVED): IDF softly down-weights common tokens and Step-1 already
  collapses numeric/reference noise to `#num`; singletons are kept because they drive
  exact once-seen-merchant matches in k-NN.
- **Predict**: cosine = dot of normalized sparse vectors over shared tokens. Per example
  add exact-amount bonus when q.AmountAbs == ex.AmountAbs (apd Cmp==0) and currencies
  match. Take top-K by similarity; aggregate per-account distance-weighted vote
  (weight = similarity). Winner = max vote. Confidence = max raw cosine among winner's
  neighbors. Margin = (vote1−vote2)/vote1. Evidence = winner's closest neighbor.
- **min-support**: accounts with < minSupport total examples are not candidates.
- **Determinism**: stable tie-break — equal vote → more recent example Date, then
  account-name byte order. No map-iteration order observable (sort candidate accounts).
  Recency is tie-break ONLY (not a scoring weight; recency weighting is out of v1 scope).
- float64 throughout; apd only for the exact-amount equality check.

#### Recommendation + rationale
k-NN over TF-IDF cosine directly realizes "based on similar ledger data" and needs no
training beyond indexing. Confidence = best-match cosine keeps abstain conservative:
an exact-amount-only match with low text similarity will not clear the confidence
threshold, so the amount bonus only re-ranks already-similar candidates rather than
fabricating confidence. Margin guards against ties between plausible accounts.

**RESOLVED (orchestrator + user):** df-pruning dropped from v1 (no minDocFreq/maxDocFreq
options) — rely on IDF + Step-1 `#num` collapse; keep singletons for exact-merchant match.

---

## Step 4 — Hook 本体 + config + 登録

### Functional requirements
- `Hook`（`hook.Hook` 実装）: single-leg txn を走査し Predictor で推論。
  閾値 + margin を満たせば `importerutil.BalanceWith` で 2-leg 化、
  満たさなければ single-leg のまま `DiagPredict*` Warning。
- Open フィルタ（Close 済み account を提案しない）、source account/符号のドメイン制約 boost。
- TOML config: `ledger`, `min_confidence`, `min_margin`, フィールド weight, alias 等。
  Factory(`New`) で ledger を `loader.LoadFile` し Predictor を構築。
- `init()` + `hook.RegisterFactory("predict", ...)` 登録。`cmd/beanimport` で有効化。

### Modules
- `pkg/importer/hook/std/predict/predict.go`（Hook + Apply）
- `pkg/importer/hook/std/predict/config.go`（TOML config + Factory）
- `pkg/importer/hook/std/predict/doc.go`（init 登録 + パッケージ godoc）
- `cmd/beanimport` 側の blank import（`classify` の取り込まれ方を踏襲）
- 対応する `_test.go`

### Verification
- table test: 学習済み counter での 2-leg 化、低 confidence/margin での single-leg + Warning、
  Close account 除外、ドメイン制約 boost、config decode、ctx cancel。

### Quality requirements
- `Apply` は in.Directives を mutate しない（hook ABI 準拠）。並行呼び出し安全。
- 決定的出力。exported symbol に doc comment。

---

## Step 5 — eval harness + e2e + Bazel/Gazelle 整備

### Functional requirements
- leave-one-out eval test: testdata 台帳に対し各 txn を除外して予測 → accuracy / abstain 率を出す。
- e2e: `cmd/beanimport` 経由で predict hook が single-leg を 2-leg 化、未知は single-leg+Warning。
- determinism: 同一入力 2 回で byte 一致。

### Modules
- `pkg/importer/hook/std/predict/eval_test.go`
- `pkg/importer/hook/std/predict/testdata/`（学習台帳・import 入力・期待出力）
- `cmd/beanimport/testdata/` への predict 用 fixture（必要なら）
- 全 `BUILD.bazel`（Gazelle 生成）

### Verification
- `bazel run //:gazelle`（必要なら `update-repos -from_file=go.mod`）→
  `bazel build //...` → `bazel test //...` 全緑。
- e2e: `bazel run //cmd/beanimport -- -config <toml> -hook predict <csv>` 目視。

### Quality requirements
- eval は閾値/weight チューニングの土台として再利用可能な形。
- testdata は最小・自己説明的。

---

## 開発ブランチ
`claude/beancount-unbalanced-tx-decorator-satkjp`
