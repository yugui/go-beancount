# Plan: `Ledger.Options` への option 取り扱いの集約

## Goal

option 値の typed パースを `*ast.Ledger` 構築時に 1 回だけ行い、`Ledger.Options *OptionValues` フィールドに保持する。プラグインは `api.Input.Options` 経由でこの typed view を直接受け取り、再パースしない。option パースエラーは `Ledger.Diagnostics` に 1 度だけ Span 付きで記録される。

## Scope

### In scope

- `Values` / `newDefaultRegistry` / 関連 parser / `Parse` / `ParseError` を `pkg/ast` に移動 (`OptionValues`, `OptionParseError`, `ParseOptions` にリネーム)。
- `pkg/ast.Ledger` に `Options *OptionValues` フィールド追加。`loader.finish()` で populate。
- `api.Input.Options` の型変更 (`map[string]string` → `*ast.OptionValues`)。
- `loader` / `postproc.Apply` の `BuildRaw` 呼び出し削除。
- 3 つのバリデーション plugin (`balance`, `validations`, `pad`) から再パースロジックを削除。
- `plugin_processing_mode` を `kindString` で登録。
- 既存 `internal/options` パッケージ削除 (`FromRaw` / `BuildRaw` も併せて削除)。

### Out of scope

- プラグインローカルの option 登録機構 (default registry は package-level singleton のまま)。
- `Ledger.Options` の動的再計算 / 後付け option 反映 (snapshot セマンティクスで固定)。
- `internal/options` のリネーム (削除一択)。

## Background

現状の問題点:

1. **再パースの無駄**: `pad`, `balance`, `validations` の各プラグインが独立に `options.FromRaw(in.Options)` を呼ぶ。標準パイプライン 1 回の実行で同じ option map が複数回パースされる。
2. **診断の重複**: malformed option があると各プラグインが同じ `invalid-option` 診断を出すため、利用者には同一エラーが N 件並ぶ。
3. **アーキテクチャの不整合**: `*ast.Ledger` 構築時に 1 度パースして保持すべき情報が、消費側で都度復元される構造になっている。`api.Input.Options` も raw `map[string]string` のままで、typed view を欲しいプラグインは自前で復元している。

## 設計判断

ユーザーとの対話で以下を確定済み:

- **循環依存の解消**: `Values` と `newDefaultRegistry` (および不可分な `kind`, `spec`, `registry`, accessors, parsers, `Parse`, `ParseError`) を `pkg/ast` に移動し、`internal/options` を削除する。`Parse` も同居するので `(*OptionValues).set(key, raw)` は private のまま維持。
- **`FromRaw` / `BuildRaw` の削除**: 本番経路から外れると同時に削除。テスト用は合成 `*ast.Ledger` + `ast.Parse(ledger)` 経由の test helper で代替。
- **Snapshot セマンティクス**: `Ledger.Options` は load 時の 1 度きりのスナップショット。`Insert*` / `ReplaceAll` で `*ast.Option` を後から挿入しても反映されない。godoc に明記。
- **Nil-safe accessor**: `*OptionValues` の 4 つの accessor は nil receiver で default を返す。テストフィクスチャで `api.Input{}` (Options 未設定) のままでも plugin が panic しない。

## 名前付け

| 旧 | 新 |
|---|---|
| `options.Values` | `ast.OptionValues` |
| `options.ParseError` | `ast.OptionParseError` |
| `options.Parse(*ast.Ledger)` | `ast.ParseOptions(*Ledger)` |
| `(*Values).set` | `(*OptionValues).set` (private 維持) |
| `options.NewValues()` / `newValues(reg)` | `ast.NewOptionValues()` (公開) |
| `options.defaultRegistry` | `ast.defaultRegistry` (private) |

## Steps

各ステップ完了時点で `bazel build //...` と `bazel test //...` が green であることを必須とする。

### Step 1: `pkg/ast` に option 機構を新設

**Functional requirements:**
- `pkg/ast` に新しいソースファイル (例: `optvalues.go`, `optvalues_kinds.go`, `optvalues_defaults.go` または 1 ファイルにまとめてもよい) を追加。
- `OptionValues` 型 (旧 `options.Values`) と nil-safe な公開 accessor (`String`/`Bool`/`Decimal`/`StringList`)。
- 内部の `kind`, `spec`, `registry`, `newRegistry`, `(*registry).register`, `(*OptionValues).set(key, raw) error`。
- `newDefaultRegistry()` と package-level `defaultRegistry`。既存の 3 option (`operating_currency`, `inferred_tolerance_multiplier`, `infer_tolerance_from_cost`) に加え、`plugin_processing_mode` (kindString, default `""`) と `title` (kindString, default `""`) を新規登録。`title` を入れるのは `TestApply_OptionsSnapshotLastWins` が利用するため。
- parser 群 (`parseStringOption`, `parseBoolOption`, `parseDecimalOption`, `parseCurrencyListItem`) を private で同居。
- `OptionParseError` 構造体 (`Key`, `Value` string, `Span` ast.Span, `Err` error) と `Error()`, `Unwrap()`。
- `ParseOptions(ledger *Ledger) (*OptionValues, []OptionParseError)`: ledger を walk して `*ast.Option` を `(*OptionValues).set` に流す。`nil` ledger は default Values + 空エラー。
- `NewOptionValues() *OptionValues`: registry default を持つ空の OptionValues。

**Modules / files:**
- 新規: `pkg/ast/optvalues.go` (+ 必要なら分割)
- 新規: `pkg/ast/optvalues_test.go` — 旧 `internal/options/options_test.go` の registry / kind / accessor / set 系テストを移植。nil-safe accessor の新規テストも追加。

**Verification:**
- `bazel run //:gazelle` 後に `bazel build //pkg/ast/...`
- `bazel test //pkg/ast:ast_test`

**Quality requirements:**
- `internal/options` は本ステップでは存続し、既存 plugin/loader 経路はそのまま動く (両者並存)。
- accessor の godoc に nil-safe な振る舞いを明記。
- `OptionValues` 構造体は zero-value 利用不可、`NewOptionValues()` 経由のみ。

---

### Step 2: `Ledger.Options` フィールド追加と load 時の populate

**Functional requirements:**
- `pkg/ast.Ledger` に `Options *OptionValues` フィールドを追加。godoc に "snapshot of typed option values built once at load time; subsequent mutations via Insert/InsertAll/ReplaceAll do not refresh this snapshot" の趣旨を明記。
- `pkg/ast/load.go` の `loader.finish()` 末尾で `ast.ParseOptions(ledger)` を呼び、結果を `ledger.Options` に格納。
- 返ってきた `[]OptionParseError` を `ast.Diagnostic{Code: "invalid-option", Span: err.Span, Message: fmt.Sprintf("invalid option %q: %v", err.Key, err.Err)}` として `ledger.Diagnostics` に append。メッセージフォーマットは既存プラグインの出力と一致させる。
- 空 ledger / option 無しの場合も `ledger.Options` は registry default を持つ非 nil `*OptionValues` になる。

**Modules / files:**
- 変更: `pkg/ast/ast.go` (Ledger 構造体に Options フィールド追加)
- 変更: `pkg/ast/load.go` (finish() で ParseOptions 呼び出しと Diagnostics 追加)
- 変更/新規: `pkg/ast/load_test.go` に `TestLoadOptionsBuildsValuesAndDiagnostics` を追加。正常 option / malformed option (1 件のみ Diagnostic) / 重複キーの last-wins / StringList の append シナリオを確認。

**Verification:**
- `bazel test //pkg/ast:ast_test --test_filter=TestLoadOptionsBuildsValuesAndDiagnostics`
- `bazel test //pkg/ast:ast_test`
- この段階では plugin 経路は依然として `internal/options.FromRaw` を呼ぶため、malformed option については Ledger 側と plugin 側で診断が **二重に** 出る一過性の状態になる (Step 3 で plugin 側を削除して解消)。`internal/options` の挙動は変えていないので既存 plugin / loader test は全て green を保つ。

**Quality requirements:**
- メッセージフォーマット `"invalid option %q: %v"` の維持 (下流テスト churn 抑制)。
- `Ledger.Options` の非 nil 不変条件を godoc に明記。

---

### Step 3: `api.Input.Options` 型変更 + plugin / loader / postproc 一括移行

このステップは型レベルで結合しているため複数パッケージを 1 コミットで変更する (個別に分けると build green を保てない)。

**Functional requirements:**
- `pkg/ext/postproc/api/plugin.go`: `Input.Options` を `map[string]string` → `*ast.OptionValues` に変更。godoc を "typed snapshot taken at ledger construction" に更新。
- `pkg/loader/loader.go`:
  - `options.BuildRaw(ledger)` 呼び出しを削除。
  - `runPipeline`: `ledger.Options.String("plugin_processing_mode") == "raw"` で raw mode 判定。
  - `runBuiltin`: `Options: ledger.Options` を渡す。
  - `internal/options` の import を削除。
- `pkg/ext/postproc/apply.go`:
  - `options.BuildRaw(ledger)` 呼び出しを削除。
  - 各 plugin 呼び出しに `Options: ledger.Options` を渡す。
  - `internal/options` の import を削除。
- `pkg/validation/balance/plugin.go`:
  - `parseOptions` ヘルパとその呼び出しを削除。
  - `in.Options` (now `*ast.OptionValues`) を直接 `checkBalance` に渡す。
  - `invalid-option` 生成ロジックを削除。
- `pkg/validation/validations/plugin.go`:
  - `options.FromRaw(in.Options)` と `optErrs` の `invalid-option` 出力ループを削除。
  - `newTransactionBalances(in.Options)` を直接呼ぶ。
- `pkg/validation/pad/plugin.go`:
  - "for parity" `options.FromRaw(in.Options)` を削除 (返り値が unused)。
- `pkg/validation/internal/tolerance/tolerance.go`: シグネチャ中の `*options.Values` を `*ast.OptionValues` に置換。
- `pkg/validation/validations/transaction_balances.go`: フィールド型を同様に置換。
- `pkg/validation/doc.go`: サンプルコードを `options.BuildRaw(ledger)` → `ledger.Options` 経由に書き換え。
- `pkg/validation/integration_test.go`: `runPipeline` ヘルパで `options.BuildRaw(ledger)` を削除し `ledger.Options` を直接渡す。
- 3 つの `TestPlugin_OptionsFromRawParseError` を `pkg/validation/{balance,pad,validations}/plugin_test.go` から削除 (同等の挙動は Step 4 で end-to-end test に集約)。
- `api.Input{Options: map[string]string{...}}` リテラルを使っているプラグイン test を test helper 経由に書き換え。

**Modules / files:**
- 変更: `pkg/ext/postproc/api/plugin.go`
- 変更: `pkg/loader/loader.go`
- 変更: `pkg/ext/postproc/apply.go`
- 変更: `pkg/validation/balance/plugin.go`, `pkg/validation/validations/plugin.go`, `pkg/validation/pad/plugin.go`
- 変更: `pkg/validation/internal/tolerance/tolerance.go`, `pkg/validation/validations/transaction_balances.go`
- 変更: `pkg/validation/doc.go`, `pkg/validation/integration_test.go`
- 変更: `pkg/validation/{balance,pad,validations}/plugin_test.go`
- 変更: `pkg/validation/internal/tolerance/tolerance_test.go`, `pkg/validation/validations/transaction_balances_test.go`
- 変更: `pkg/ext/postproc/apply_test.go` (TestApply_OptionsSnapshotLastWins の修正: `title` を Step 1 で登録済みなので `Options.String("title")` 経由でアサート)
- 新規 (推奨): `pkg/ast/asttest` または同等のテストヘルパパッケージ — `MustOptions(t *testing.T, raw map[string]string) *OptionValues` を提供。内部で合成 `*ast.Ledger` を組み立てて `ast.ParseOptions(ledger)` を呼ぶ。
- 関連 `BUILD.bazel` の `gazelle` 再生成。

**Verification:**
- `bazel run //:gazelle`
- `bazel build //...`
- `bazel test //...`
- 特に `bazel test //pkg/validation/... //pkg/loader/... //pkg/ext/postproc/...`

**Quality requirements:**
- plugin 側から `internal/options` の import が消えていること。
- nil-safe accessor のおかげで `api.Input{}` だけで動く plugin test が引き続き green であること。

---

### Step 4: end-to-end 回帰テスト追加

**Functional requirements:**
- `pkg/loader/loader_test.go` (推奨) または `pkg/ast/load_test.go` に新規テスト追加: malformed option (例: `inferred_tolerance_multiplier "not-a-number"`) を含む source を `loader.Load` で読み、最終的な `ledger.Diagnostics` に `Code: "invalid-option"` がちょうど 1 件だけあること、`Span` が directive 由来であること、を確認する。
- できれば良好な option を含むケースも合わせて確認 (`operating_currency` の append, `infer_tolerance_from_cost` の last-wins など)。

**Modules / files:**
- 変更: `pkg/loader/loader_test.go` または `pkg/ast/load_test.go`

**Verification:**
- 当該テストが green。`bazel test //pkg/loader:loader_test --test_filter=...` または同等。

**Quality requirements:**
- 「1 件だけ」のアサーションを明示する (重複していたら fail)。

---

### Step 5: `internal/options` パッケージ削除

**Functional requirements:**
- `internal/options/*` ファイルおよびディレクトリを削除。
- 残存する `//internal/options` への依存があれば `bazel build` がエラーを出すので、Gazelle で再生成して deps を更新。

**Modules / files:**
- 削除: `internal/options/options.go`, `kinds.go`, `defaults.go`, `parse.go`, `options_test.go`, `BUILD.bazel`
- 変更: 残存する `BUILD.bazel` の `deps` から `//internal/options` を Gazelle で除去

**Verification:**
- `bazel run //:gazelle`
- `bazel build //...` (失敗しなければ全参照が消えている)
- `bazel test //...`

**Quality requirements:**
- 移行漏れがないことを最終確認。

---

### Step 6: テスト棚卸し

**Functional requirements:**
- 旧 `internal/options/options_test.go` 由来のアサーション (kind ごとのパース成功/失敗、重複キーの last-wins / StringList append、unknown key の silent ignore など) が `pkg/ast/optvalues_test.go` および Step 4 の end-to-end test で全てカバーされているか棚卸し。
- 機械的な複製ではなく、契約として残すべきものだけを残す。
- 不要になった test helper や fixture を削除。

**Modules / files:**
- 変更: `pkg/ast/optvalues_test.go` (必要なら追加 / 削減)
- 削除: 不要となった旧テストヘルパ

**Verification:**
- `bazel test //...`

**Quality requirements:**
- 重複アサーションを残さない。1 アサーション 1 テスト点。

---

## Alternatives discussed

- **`internal/optionvalues` leaf を抽出する案**: pkg/ast から独立した data-only パッケージに `Values` を置き、`Parse` を `internal/options` に残す。最小限の API churn だが、削除しきれない `internal/options` が小さく残り、パッケージ数も増える。**却下**: `Parse` も同梱で pkg/ast に集約する方が単純で読みやすい。
- **`Ledger.Options` を mutation で再パースする案**: `Insert*` / `ReplaceAll` で `*ast.Option` が変わったら refresh。**却下**: snapshot で十分 (現在の plugin は Option を新規生成しない)。必要になれば後付けで `Ledger.RefreshOptions()` を足せばよい。
- **`*OptionValues` を interface 化して pkg/ast から見えなくする案**: `Ledger.Options` の循環依存を avoid。**却下**: アクセサ全て interface 化する unnecessary abstraction で、Go の style 的にも不自然。

## Recommendation

上記 Step 1-6 を順番に実装する。各 Step は単独で build/test を green に保ち、レビュー単位として独立。Step 3 のみ複数パッケージにまたがるが、これは型変更の必然 (個別分割は build green を保てない)。
