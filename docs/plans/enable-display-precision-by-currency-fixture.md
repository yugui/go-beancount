# pkg/compat/beancompat: display_precision_by_currency.json を通す

## Phase 1 — Goal and scope (draft)

### Goal (作業目的)

`pkg/compat/beancompat` の parse-tier divergences denylist から `display_precision_by_currency` を外し、upstream beancompat の対応する fixture (`fixtures/parse/display_precision_by_currency.json`) が `bazel test //pkg/compat/beancompat/...` でグリーンになるようにする。同時にこのテストが要求する「per-currency display precision を観測 amount から推論する」基盤 (= upstream の `dcontext` 相当) と、parse-tier serializer の `options` envelope 出力を導入する。

### Scope (内訳・確定済み)

**含む:**

- AST または Loader 層に「観測した amount から currency 別の most-common 小数桁数を集計する」display-context 相当のデータ構造 (以降 `DisplayContext`) を新設する。
- 観測経路: parser 統合 or AST post-pass のいずれかで、`transaction` の posting amount, `balance` の amount, `price` の amount, `cost` の amount などを観測対象として `DisplayContext.update(decimal, currency)` を呼ぶ (具体的な観測対象 directive の範囲は Phase 2 で確定)。
- このデータ構造を `Ledger` から取得できる経路を作る (例: `Ledger.DisplayContext()`)。
- `pkg/format/` 側の amount フォーマッタを拡張し、DisplayContext を渡された場合は per-currency の most-common precision で amount を出力する経路を新設する。既存呼び出しの後方互換 (DisplayContext なし時は現状の `apd.Decimal.String()` ベース動作) を保つ。
- `pkg/compat/beancompat/serialize.go` の `SerializeParsed` から `Result.Options` 出力経路を新設し、最低限 `display_precision_by_currency: {CCY: int}` を埋める。
- `pkg/compat/beancompat/denylist.go` の Go 側 denylist と `pkg/compat/beancompat/pyharness/denylist.py` の Python 側 denylist の両方から `display_precision_by_currency` エントリを削除する。
- `parse_fixtures_test` と `pyharness/test_fixtures` の両方で当該 fixture が通ることを確認する。

**明示的に含まない (将来別タスク):**

- `options_coverage.json` を通すこと (~30 keys の options serialization と map 型 option 値サポート)。
- `option "display_precision" "CCY:0.01"` 形式の override (dcontext を option で固定する機能)。本 fixture の source に登場しないので未対応のまま通る。将来追加できる余地は残す。
- 文字列→整数 map 型 / 文字列→Decimal map 型の option 値を `OptionValues` registry に追加すること。本 fixture では不要。
- `display_precision_by_currency` 以外の derived options envelope key (`commodities`, `plugin`, `documents`, etc.)。

### Code findings — 現状把握

**現状:**

- `pkg/compat/beancompat/serialize.go:82-103` で options 直書きが明示的に skip されている。コメントに「Once the AST gains an options-retention mechanism, a separate plan (Plan A) will introduce options serialization」とあり、本プランがその "Plan A" にあたる。
- `pkg/ast/ledger.go:19-32` の `Ledger.Options *OptionValues` は load 後に populate されており、`pkg/ast/load.go:77-78` で `ParseOptions(ledger)` から得たものをセットしている。
- `pkg/ast/optvalues.go` の registry は現状 4 種類のみサポート: `kindString`, `kindBool`, `kindDecimal`, `kindStringList`。map 型は未サポート。
- printer / formatter (`pkg/format/`, `internal/formatopt/`) には dcontext 相当の概念は無い。現在は `apd.Decimal.String()` で source 由来の precision をそのまま出している。
- `pkg/compat/beancompat/denylist.go:25-28` と `pkg/compat/beancompat/pyharness/denylist.py` の両方に同名で divergence が登録されている (Go/Python 二層 denylist policy)。

**Fixture の中身 (upstream beancompat e4f805b より取得):**

`display_precision_by_currency.json` の source は option directive を含まない。USD (2 dp) と JPY (0 dp) の amount をいくつか持つ普通の trade のみ。description が明言: *"Asserts display_precision_by_currency is derived from dcontext (inferred from transaction amounts)"*。期待値は `options.display_precision_by_currency: {"JPY": 0, "USD": 2}` のみ（他に option エントリは無し）。

**Upstream `dcontext` の正体 (要約):**

- 純粋な統計集計器。parser が transaction / balance / price の各 amount を観測するごとに `dcontext.update(decimal, currency)` を呼ぶ。
- currency 別に「小数桁数の出現頻度分布 (`fractional_dist: map[int]int`)」を保持し、`Precision.MOST_COMMON` または `Precision.MAXIMUM` で問い合わせ可能。
- option `display_precision` (string→Decimal map 型) が指定された場合は `set_fixed_precision()` で当該通貨の `_CurrencyContext` を `_FixedPrecisionContext` で上書き。
- 「`display_precision_by_currency` という名前の option」は upstream には**存在しない**。それは serializer 側で dcontext.build().fmtstrings を「currency → fractional_digits」の dict に変換した derived view (fava 1.30.12 由来の命名)。
- 上流のソース: `beancount/core/display_context.py`, `beancount/parser/grammar.py` (Builder の Amount 観測時 callback), `beancount/parser/options.py` の `display_precision` option 定義, `beancount/parser/printer.py` の `dformat.format()` 利用。

### User-supplied ideas catalog (raw — Phase 1 では評価しない)

ユーザーが原文で挙げた問題点（そのまま転記）:

1. 現在 `serialize.go` は ledger 内の option value を serialize しない。
2. こういう string-to-int map をサポートする option value は実装されていない。
3. upstream の `dcontext` 概念は今の printer や format には存在しない。upstream の dcontext を確認して仕様を決め、計画的にコードを拡張する必要。
4. dcontext は名目的に options に入れられているとは言え、option directive 以外の directive に応じて定まる仕様と思われる。option parser の仕様と一致しないかも。

→ §"Upstream `dcontext` の正体" の結論として、(4) は的中。`display_precision_by_currency` は option directive とは独立の derived value で、observed amount からの統計推論で決まる。`option "display_precision"` 自体は別に存在するが、本 fixture の source には含まれない。
→ よって (2) の string-to-int map 型サポートは、本 fixture を通すためだけなら**実装不要**の可能性が高い (Q3 参照)。
→ (1) と (3) はどちらも必要。

### Red flags

- **upstream の option 名と fixture が示す key 名が一致しない**: upstream の option は `display_precision` (string→Decimal map)、本 fixture が要求するのは derived な `display_precision_by_currency` (string→int map)。実装側で「これは option ではない、dcontext から導出する derived value だ」という設計上の区別を最初から明示しないと、二項間の混同が後々のメンテで事故る。
- **dcontext は parser 統合が前提**: upstream では parser の Amount コンストラクションで callback している。go-beancount 側で同等のことをするには、parser から AST 構築時に dcontext を update するか、AST 走査の post-pass にするか、設計判断が要る。後者の方が parser を汚さない（一方で同じ走査を毎回繰り返すコストはある）。
- **option envelope に何を出すかの方針が未定**: 本 fixture だけなら `display_precision_by_currency` 一個出せばよいが、`options_coverage.json` も視野に入れるなら同じ envelope に ~30 keys 並べる必要があり、構造設計が変わる。先に scope を確定する必要あり。
- **Match セマンティクスは containment**: `pkg/compat/beancompat/match.go` の Options 比較は JSON tree containment。つまり「期待値より多く出す」のは許容される。逆に「期待値に無いキーを多く出して通す」ことは可能だが、テスト網羅性の意味で逆効果なので過剰出力は避けたい。

### Open questions resolved

- Q1 (scope): `display_precision_by_currency.json` のみ。`options_coverage.json` は別タスク。
- Q2 (populate 方式): AST post-pass (planner 推奨、§ Phase 2 alt 1 参照)。
- Q3 (`option "display_precision"` override): 今回含めない。fixture source に登場せず未対応で通る。将来追加できる余地は残す設計とする。
- Q4 (printer 統合): 今回含める。`pkg/format/` の amount フォーマットに DisplayContext を反映する opt-in を追加する。

---

## Phase 2 — High-level design (planner 提案 + ユーザー refinement)

### 名前の分離 (ユーザー refinement)

`DisplayContext` という単一の名前で「観測 amount からの精度統計」と「formatter が消費するコンテキスト」の両方を表すと、将来 `option "display_precision"` による override を追加したときに「これは観測統計か、option 由来 override か」が型名から見えなくなる。よって命名と責務を分離する:

```
internal/formatopt.DisplayContext   ─ interface (formatter consumer contract)
                ▲ implements
pkg/ast.PrecisionProfile             ─ struct (observed-amount statistics — bookkeeping 上の実態)
                ▲ wraps / composites (将来の別 step)
???.OverrideContext (将来追加)        ─ option "display_precision" による per-currency override
                                       を PrecisionProfile に decorate or composite した
                                       DisplayContext 実装。今回はスコープ外。
```

`pkg/ast.PrecisionProfile` は「観測結果のプロファイル」というニュートラルな命名にし、override や fixed precision の意味合いを持たせない。formatter 側の interface は `DisplayContext` のままで、これは将来の override 実装も同じ interface に乗せて consumer 側コードを変えずに切り替え可能にする拡張ポイントとなる。

### Steps (5 件)

1. **`ast.PrecisionProfile` 型の導入** (純粋なデータ構造 + 単体テスト)。observation/query 操作のみ、integration は無し。
2. **`Ledger.PrecisionProfile` の populate** を `loader.finish()` の AST post-pass で行う。`Options` と同様に load 時にセット、mutator では更新しない。
3. **`pkg/format/` に `WithDisplayContext` opt-in を追加**。`internal/formatopt.DisplayContext` interface を新設し、`*ast.PrecisionProfile` が構造的に満たす形にする。DisplayContext を渡された通貨のみ per-currency precision で量子化する。デフォルト動作は完全に保持。
4. **`SerializeParsed` から `display_precision_by_currency` 出力**。専用ヘルパが `ledger.PrecisionProfile` から `Result.Options` を組み立て、観測ゼロなら `nil` のまま (omitempty)。
5. **denylist エントリ削除** (Go + Python 両方) + `conftest_smoke_test.py` のピン更新。

### Critical design decisions (alternatives 評価済み)

| # | 論点 | 推奨 | 主な却下案 |
|---|---|---|---|
| Alt 1 | PrecisionProfile の populate 経路 | **AST post-pass at `loader.finish()`** | parser 統合は upstream issue #678 (included file の amount 未観測) を継承するので却下 |
| Alt 2 | Ledger 上の置き方 | **公開フィールド** (`Ledger.PrecisionProfile`、`Options` と同形) | lazy accessor は concurrency hazard と一貫性破壊で却下 |
| Alt 3 | 観測対象 directive の範囲 | **Documented set: Transaction posting amount + Balance amount + Price amount** | Minimal (Tx のみ) は将来 fixture で爆発、Full (cost 含む) は upstream と divergence |
| Alt 4 | `pkg/format/` 統合形態 | **`internal/formatopt` 側に `DisplayContext` interface 定義 + `formatopt.Options` に opt-in field + `pkg/format/option.go` に `WithDisplayContext` 関数オプション**。`*ast.PrecisionProfile` が interface を構造的に満たす | constructor 引数化は全 caller 破壊、別エントリ追加は API 二重化、`internal/formatopt` から `pkg/ast` を直接 import するのは leaf-status 破壊 |
| Alt 5 | serializer ヘルパ形 | **`serializeOptions(*ast.Ledger) (json.RawMessage, error)` 専用ヘルパ**。観測ゼロ時は `Result.Options` nil のまま (`omitempty`) | inline 化は `serialize` を更に肥大化 |

### Locked contracts (Phase 4 で詳細詰めるが既に固めた点)

- **命名の責務分離**: `pkg/ast.PrecisionProfile` は「観測統計」のみを表現する concrete struct。`internal/formatopt.DisplayContext` は consumer 側 interface。両者は別概念で別名前にする (将来の override 実装余地を残すため)。
- `PrecisionProfile.MostCommon` の tie-break: **最高 precision が勝つ** (upstream の振る舞いに合わせる)。
- 量子化挙動: DisplayContext が渡された通貨については**常に量子化** (`MostCommon` が `ok=true` を返す currency 限定)。`"曖昧な時だけ量子化"` モードは意味論不明として却下。
- 丸めモード: **banker's rounding (half-even)** (upstream に一致)。
- `PrecisionProfile` の nil-safe: nil receiver で `(0, false)` を返す。
- 整数の JSON 表現: bare int (`2`, `0`)。decimal や文字列にしない。
- 観測ゼロ時の挙動: `options` envelope 全体を JSON から省略 (`omitempty` に従う)。空 map `{}` は出さない。

### Per-step detail (要点のみ — Phase 4 で詳細化)

#### Step 1 — `ast.PrecisionProfile` 型導入
- **Files**: 新規 `pkg/ast/precision_profile.go`, `pkg/ast/precision_profile_test.go`
- **Public surface**: `NewPrecisionProfile()`, `(*PrecisionProfile).Update(d *apd.Decimal, currency string)`, `.MostCommon(currency string) (int, bool)`, `.Currencies() []string`
- **Verification**: 単体テスト (exported surface のみ)。empty, mixed exponents, tie-break, deterministic ordering, nil-receiver
- **Dependency**: none

#### Step 2 — `Ledger.PrecisionProfile` populate
- **Files**: `pkg/ast/ledger.go` (フィールド追加), `pkg/ast/load.go` (`loader.finish()` で populate), 新規 `pkg/ast/precision_profile_load_test.go`
- **Verification**: `Load(src)` 経由で USD 2dp + JPY 0dp の amount を持つ source を読み込み、`MostCommon` の結果を確認
- **Dependency**: Step 1

#### Step 3 — formatter 統合
- **Files**:
  - `internal/formatopt/display_context.go` (新規) — `DisplayContext` interface 定義 (single method: `MostCommon(currency string) (int, bool)`)
  - `internal/formatopt/options.go` — `DisplayContext DisplayContext` field 追加 (interface 型で受ける)
  - `internal/formatopt/number.go` — quantizer helper を追加
  - `pkg/format/option.go` — `WithDisplayContext(formatopt.DisplayContext) Option` (引数は interface 型なので `*ast.PrecisionProfile` がそのまま渡せる)
  - `pkg/format/format.go` — `formatDirective` で `formatCommaGrouping` の前に量子化 pass を挟む
  - 新規 `pkg/format/format_displaycontext_test.go`
- **Verification**: 既存の format テスト (consistency, integration, diff など) が改変ゼロで pass する点を regression として確認。新規テストで pad / truncate / pass-through を網羅
- **Dependency**: Step 1 (型と interface 充足)。Step 2 は不要 (callers が手動で PrecisionProfile を渡す経路でも動く)

#### Step 4 — `SerializeParsed` から options 出力
- **Files**: `pkg/compat/beancompat/serialize.go` (新ヘルパ + dispatcher 修正、既存 82-85 行のコメント書き換え), `pkg/compat/beancompat/serialize_test.go` (新規 subtest)
- **Verification**: 単体テストで shape (sorted key, int value), 観測ゼロ時の nil 維持を確認。`parse_fixtures_test` は Step 5 まで SKIP のままを維持
- **Dependency**: Step 2

#### Step 5 — denylist 削除
- **Files**: `pkg/compat/beancompat/denylist.go`, `pkg/compat/beancompat/pyharness/denylist.py`, `pkg/compat/beancompat/pyharness/conftest_smoke_test.py`
- **Verification**: `bazel test //pkg/compat/beancompat/...` で当該 fixture が SKIP→PASS。`options_coverage` は SKIP のまま維持。`bazel test //...` で regression なし
- **Dependency**: Step 1, 2, 4 (Step 3 はスコープ合意上必須だが、fixture 自体は通る)

### Cross-cutting quality requirements

- 全ての exported symbol に terse な godoc (project CLAUDE.md 準拠)
- 単体テストは exported surface のみを対象
- 各 Go ファイル追加後に `bazel run //:gazelle && bazel build //... && bazel test //...`
- コミットメッセージは「何故」と「実現される behavior」を語る、内部実装の機械的描写は書かない

### Verification (end-to-end)

最終的に以下が green になることを確認:

```
bazel run //:gazelle
bazel build //...
bazel test //...                                              # 全件
bazel test //pkg/compat/beancompat:parse_fixtures_test        # display_precision_by_currency が PASS、options_coverage は SKIP のまま
bazel test //pkg/compat/beancompat/pyharness:test_fixtures    # 同上 (Python 側)
```


