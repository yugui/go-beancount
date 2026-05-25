# Phase 11 — LSP Server with Overlay/Session

## Context

PLAN.md の Phase 11 deliverable である `cmd/beancount-lsp` (LSP 3.17 サーバ) を
実装する。LSP は複数ファイルにまたがる ledger コンテキスト (include の
ワイルドカード解決、option のグローバル効果、postprocessor の任意変換) を
扱う必要があるため、エディタの未保存バッファをディスク値に優先させる
「オーバーレイ機構」と、長寿命の `Session` 型 (Snapshot 取得 + 粗粒度の
購読 API) を前提整備として `pkg/loader` / `pkg/ast` 周辺に整える。

fsnotify によるサーバ側ファイルシステム監視は Phase 10 (bean-daemon) に
延期し、Phase 11 では LSP プロトコルの `workspace/didChangeWatchedFiles`
でクライアント (VS Code, Neovim 等) にファイル監視を委譲する。Phase 10 は
本 Phase の `Session` の上に fsnotify アダプタを足すだけで再利用できる。

## Goal

go-beancount を編集する利用者に対して、エディタから次の機能を提供する:

- **Diagnostics**: 構文エラー + 検証エラーを didChange/didSave に追随して即時表示
- **Formatting**: 全文 + range (directive 単位) フォーマット
- **Completion**: account / commodity / keyword / flag / tag / link / payee /
  narration / metadata key / value
- **Go-to-definition**: include path / account / commodity
- **Hover**: account (メタ + 残高) / commodity (context date + price + メタ)
- **Document symbols**: ディレクティブ階層を SymbolKind で表現
- **Multi-file awareness**: include 解決済み ledger を全機能で利用

## Scope

### In scope

- 前提整備: pkg/ast loader の source-provider seam、`WithOverlay` LoadOption、
  新規 `pkg/session` パッケージ (Snapshot + Subscribe)
- `cmd/beancount-lsp` の LSP 3.17 実装 (上記 Goal の全項目)
- LSP ライブラリ依存追加 (`go.lsp.dev/protocol` + `go.lsp.dev/jsonrpc2` +
  `go.lsp.dev/uri`) を `go.mod` / `MODULE.bazel` に
- end-to-end smoke test + `docs/architecture/lsp-overview.md`

### Out of scope

- fsnotify ベースのサーバ側ファイル監視 (Phase 10 で同じ Session の上に実装)
- per-file パースキャッシュ等の性能最適化
- 細粒度の差分通知 API (どの directive が変わったか等)
- `pkg/format` の AST 単位フォーマット公開 API (Phase 11 範囲では不要)
- `pkg/ast` への lookup index API (Commodity / Open の O(1) 検索など)
- PLAN.md の更新

## Steps (順序付き)

依存関係:

- Step 2 は Step 1 に依存
- Step 3, 4 は Step 2 に依存
- Step 5 は Step 4 に依存
- Step 6 以降は Step 5 完了が前提
- Step 7–11c は Step 6 完了後に並列化可
- Step 9 と Step 10 (commodity) は `locate.go` を共有 — Step 9 で先に導入
- Step 11b, 11c は Step 11a の `classifyContext` 拡張で進める
- Step 12 は Step 5 以降の任意の時点で可
- Step 13 が最終

### Step 1 — pkg/ast loader に source-provider seam を入れる

**Functional requirements**: `pkg/ast/load.go` の `loader.loadFile` が
`syntax.ParseFile` をディスク経由で呼ぶのをやめ、内部インターフェース
`sourceProvider` (例: `Open(absPath string) (io.ReadCloser, error)`) を
経由する。既定実装は `os` 直読みで現状動作と同一。**公開 API は不変**。

**Touched modules**: `pkg/ast/load.go`、必要なら `pkg/ast/source.go` 新設、
`internal/loadopt/options.go` (provider をオプションに格納する素地)。

**Verification**: 既存 `pkg/ast` / `pkg/loader` のテスト全グリーン。
本ステップ単独のテストは追加不要 (Step 2 で新規パスが入る)。

**Quality requirements**: 既存公開シグネチャ不変。godoc は内部 seam の
ライフサイクル (loader 構造体と同寿命) を 1-2 行で明記。
`bazel build //... && bazel test //...` 通過。

### Detailed Design

#### Contract

- The exported surface of `pkg/ast` and `pkg/loader` is unchanged: signatures, doc comments, parameter semantics, return values, and diagnostic message wording for `Load`, `LoadReader`, `LoadFile`, `WithBaseDir`, `WithFilename`, and the `LoadOption` type alias remain bit-identical. No new exported symbol is added in this step.
- Externally observable behavior on disk-backed loading is unchanged: cycle-detection messages, missing-file diagnostics, glob expansion semantics, and span/filename recording must match the pre-refactor build line-for-line for every input.
- All existing tests under `pkg/ast/...` and `pkg/loader/...` pass without modification. The full `bazel build //...` and `bazel test //...` cycle is green after `bazel run //:gazelle`.
- The seam introduced in this step is **package-private**. It must not leak through any exported symbol, godoc, or `internal/loadopt.Options` field that is reachable from outside `pkg/ast`. Step 2 will widen visibility through a dedicated `WithOverlay` option; Step 1 leaves the mechanism unobservable.

#### Suggested Internals

The implementer may adopt, modify, or replace any of the following based on what surfaces during implementation. None of these decisions is locked.

**1. Seam shape — recommended: byte-oriented `read(absPath string) ([]byte, error)`.**

Define an unexported interface inside `pkg/ast`:

```go
type sourceProvider interface {
    read(absPath string) ([]byte, error)
}
```

Candidates considered:

- **A. `Open(absPath) (io.ReadCloser, error)`** — streams via `syntax.ParseReader`. Pro: no full materialization. Con: Step 2's overlay is `map[string][]byte`, so an overlay provider would wrap bytes in `io.NopCloser(bytes.NewReader(...))` on every call — pure ceremony for content already in memory.
- **B. `read(absPath) ([]byte, error)`** *(recommended)* — bytes in, parsed via `syntax.Parse(string(b))`. Matches Step 2's overlay storage exactly. The disk provider is a one-liner over `os.ReadFile`. Typical files are KB-to-hundreds-of-KB; full materialization is a non-issue at this scale.
- **C. `parse(absPath) (*syntax.File, error)`** — hides the parser entirely. Con: forces Step 2's overlay path to re-parse identical bytes on every reload; couples the seam to `pkg/syntax`'s API stability.

**2. Location — recommended: new `pkg/ast/source.go`.**

A new file keeps the seam discoverable and stops `load.go` from growing further. Inlining into `load.go` is acceptable if the implementer judges it small enough (well under 30 lines total).

**3. Default implementation — recommended: a tiny `osSourceProvider` struct or a `sourceProviderFunc` adapter.**

```go
type sourceProviderFunc func(string) ([]byte, error)
func (f sourceProviderFunc) read(p string) ([]byte, error) { return f(p) }

var defaultSource sourceProvider = sourceProviderFunc(os.ReadFile)
```

The function-adapter form makes Step 2's overlay-with-fallback composition trivial (closure over an overlay map + delegate).

**4. Wiring into `loader` — recommended: a `source sourceProvider` field on `loader`, set by `newLoader()`.**

```go
type loader struct {
    visited     map[string]bool
    files       []*File
    directives  []Directive
    diagnostics []Diagnostic
    source      sourceProvider
}

func newLoader() *loader {
    return &loader{
        visited: make(map[string]bool),
        source:  defaultSource,
    }
}
```

Threading the provider through `loadopt.Options` is an option but adds a field to a package outside `pkg/ast` for a mechanism that, in Step 1, is entirely internal — leave that to Step 2.

**5. `loadFile` rewrite.**

Replace

```go
cst, err := syntax.ParseFile(absPath)
```

with

```go
data, err := ld.source.read(absPath)
if err != nil {
    ld.diagnostics = append(ld.diagnostics, Diagnostic{
        Message:  fmt.Sprintf("reading file %s: %v", absPath, err),
        Severity: Error,
    })
    return
}
cst := syntax.Parse(string(data))
```

The existing diagnostic wording `"reading file %s: %v"` is **preserved verbatim** because tests and downstream callers may key off it. `os.ReadFile` returns an `*os.PathError` with the same underlying error as the old `os.Open` path, so `%v` formatting yields an equivalent message. Verify against `TestLoad_MissingInclude` and `TestLoad_FileNotFound`.

`syntax.ParseFile` and `syntax.ParseReader` remain exported and untouched — other packages may still use them; this refactor is scoped to `pkg/ast`'s internal load path.

**6. Visibility.**

Keep `sourceProvider`, the default provider symbol, and the `loader.source` field all unexported. Step 2 adds the public `WithOverlay` option that internally composes an overlay-aware provider.

**7. Verification approach.**

- No new tests in Step 1 — adding them now would couple to internals that Step 2 may reshape. Pre-existing tests (`pkg/ast/load_test.go`, `pkg/loader/loader_test.go`, and the broader `bazel test //...` set) are the safety net.
- Before pushing: `bazel run //:gazelle`, `bazel build //...`, `bazel test //...`. Sanity-check that `TestLoad_FileNotFound`, `TestLoad_MissingInclude`, and `TestLoadFile_GlobInclude` (especially `nomatch` and `selfsibling` rows) still pass.
- Resist adding a "default provider is os-backed" test — every existing disk-backed test already proves this.

#### Alternatives discussed

- **Seam shape A/B/C** (covered above). Recommend B.
- **Thread the provider through `internal/loadopt.Options` now vs. later**: adding the field in Step 1 prepares Step 2 but crosses a package boundary one step earlier than necessary, for a Step 2 design that may still want a `map[string][]byte` rather than a provider interface. Defer.
- **Push the seam into `pkg/syntax`**: rejected. `syntax` is "parse this text/stream"; widening its seam to know about file resolution mixes concerns. The seam belongs at the layer that already knows about absolute paths and includes — `pkg/ast`'s loader.
- **Skip Step 1 abstraction and add overlay directly in Step 2**: rejected because Step 1's purpose is to land a low-risk refactor with zero behavior change, separable from the user-visible overlay feature. Conflating produces a larger commit harder to revert if subtle behavior drift is discovered.

#### Recommendation + rationale

Adopt seam shape **B** (`read(absPath) ([]byte, error)`), defined as an unexported `sourceProvider` interface in a new `pkg/ast/source.go`, with a tiny default `os.ReadFile`-backed implementation, wired through a `source` field on `loader` set by `newLoader()`.

- **Matches Step 2's overlay storage shape.** Overlay is naturally `map[string][]byte`; byte-oriented seam needs no adapter.
- **Smaller delta than alternatives.** No new exported symbols, no changes to `internal/loadopt`, no changes to `pkg/syntax`. Diff is ~10 added + ~3 changed lines confined to `pkg/ast`.
- **Preserves diagnostic wording.** `os.ReadFile` failure formatted with `%v` yields the same message as the old path.
- **Performance is a non-concern** at typical input sizes.
- **Leaves Step 2 unconstrained.** Seam is private; Step 2 chooses how to express overlay injection without being boxed in.

### Step 2 — `WithOverlay` LoadOption の公開

**Functional requirements**:

- `func WithOverlay(overlay map[string][]byte) LoadOption` を `pkg/ast` と
  `pkg/loader` の両方に公開 (loader は ast の再エクスポート方式が既存
  パターン)。
- キーは **絶対パス** 限定。godoc に明記。
- include 解決時に overlay にヒットすれば overlay の bytes をパースする。
- glob 展開は **ディスクとオーバーレイの union** を返す
  (ディスクには存在せず overlay にだけある絶対パスもマッチ対象)。
- 絶対パスでない overlay キーは無視 + Warning diagnostic。

**Touched modules**: `pkg/ast/option.go`, `pkg/loader/option.go`,
`internal/loadopt/options.go`, `pkg/ast/load.go`, `pkg/ast/source.go`,
`pkg/ast/glob.go`。

**Verification**: 新規 `pkg/loader/overlay_test.go` で
(a) 単一ファイル overlay の diagnostics 追随、
(b) include 先の overlay 差し替え、
(c) glob で overlay-only ファイルがマッチ、
(d) overlay キーが相対パスのときの Warning 発出。

**Quality requirements**: `WithOverlay` の godoc に
「キー = 絶対パス、`[]byte` の所有権は呼び出し側、Load 中は変更しない」
contract を明記。

### Detailed Design

#### Contract

**Public signatures**

In `pkg/ast/option.go`:

```go
// WithOverlay supplies in-memory source bytes that take precedence over
// disk for matching absolute paths during Load, LoadReader, and LoadFile.
//
// Keys MUST be absolute paths in the OS-native form (filepath.IsAbs);
// non-absolute keys are ignored and produce a Warning diagnostic with
// Code "overlay-non-absolute-key". A nil or empty map is a no-op.
//
// The map and its []byte values are borrowed by the load: the caller
// must not mutate them until the corresponding Load* call returns. The
// loader does not copy values; ownership otherwise stays with the
// caller, which is free to reuse or discard the map after the call.
//
// WithOverlay composes with WithBaseDir and WithFilename. Passing
// WithOverlay multiple times replaces the previous overlay (last-wins,
// matching the existing option semantics).
func WithOverlay(overlay map[string][]byte) LoadOption
```

In `pkg/loader/option.go`:

```go
// WithOverlay re-exports ast.WithOverlay. See ast.WithOverlay for the
// full contract.
func WithOverlay(overlay map[string][]byte) Option
```

**Resolution semantics**

- For every file load (top-level and every include resolution that reaches `loader.loadFile`), the absolute path is looked up in the overlay first. On hit, the overlay bytes are parsed; on miss, the existing on-disk read path runs and its error semantics are preserved verbatim (including the `"reading file %s: %v"` diagnostic wording).
- Overlay lookup uses the **exact absolute path** the loader would otherwise pass to `os.ReadFile`. No path normalization beyond what callers already get through `filepath.Abs` / `filepath.Join` in `handleInclude`. Symlinks are not resolved, case folding is not performed.
- Cycle detection continues to key on the `filename` argument to `loadFile`; overlay does not change cycle semantics.

**Glob union semantics**

`expandGlob` results include, in addition to the on-disk matches, every overlay key (absolute path) that:

1. is itself absolute (already required by the overlay contract), and
2. matches the glob pattern under the same `matchDoubleStar` rules used for disk paths.

The union is deduplicated and returned sorted ascending. A glob that matches only overlay-only paths must NOT emit the "matched no files" Warning.

**Non-absolute key handling**

For each map entry with `!filepath.IsAbs(key)`, the load emits one Warning diagnostic and ignores the entry:

- `Code: "overlay-non-absolute-key"`
- `Severity: Warning`
- `Span: Span{}` (zero — no source location)
- `Message`: `overlay key %q is not an absolute path; ignored`

Empty-string keys are treated as non-absolute. Diagnostics are sorted by message for determinism across map-iteration runs.

**Independence and back-compat**

- `WithOverlay` composes orthogonally with `WithBaseDir` and `WithFilename`.
- Additive: no existing exported symbol changes shape or wording. Callers that do not use `WithOverlay` see byte-identical behavior. Pinned wordings remain stable: `"reading file %s: %v"`, `"circular include detected: %s"`, "matched no files" Warning, span filenames, existing diagnostic Codes.

**Required tests (must exist in this step)**

In a new `pkg/loader/overlay_test.go`:

1. `TestLoadFile_OverlayReplacesDisk` — disk file has `2024-01-01 open Assets:Bank USD`; overlay supplies the same absolute path with `2024-01-01 open Assets:Bank EUR`; assert the loaded Open's currency is `EUR`.
2. `TestLoadFile_OverlayIncludeRelative` — root file has `include "leaf.beancount"`; disk `leaf.beancount` is empty; overlay supplies the absolute path of `leaf.beancount` with one Open directive; assert that directive appears in the ledger.
3. `TestLoadFile_OverlayIncludeAbsolute` — root file has `include "<abs>/leaf.beancount"`; overlay supplies that absolute path; same assertion.
4. `TestLoadFile_OverlayGlobUnion` — root file has `include "*.beancount"`; disk has `a.beancount`; overlay supplies the absolute path of `b.beancount` (NOT on disk); assert both files contribute directives and no "matched no files" warning fires.
5. `TestLoadFile_OverlayGlobOverlayOnly` — root has `include "*.beancount"`; disk has only the root file; overlay supplies an overlay-only file matching the glob; assert the overlay file's directive is loaded.
6. `TestLoad_OverlayNonAbsoluteKeyWarning` — overlay contains `{"relative/path.beancount": ...}`; assert exactly one Warning diagnostic with `Code == "overlay-non-absolute-key"` and that the relative key has no effect.
7. `TestLoadFile_OverlayWithBaseDir` — `ast.Load(src, WithBaseDir(dir), WithOverlay(...))` where `src` contains a relative include and the overlay supplies the resolved absolute path; assert the include is satisfied from overlay and `WithBaseDir` is still honored.
8. `TestLoadFile_OverlayEmptyMap` — `WithOverlay(nil)` and `WithOverlay(map[string][]byte{})` are no-ops; existing disk-backed test passes through unchanged.

In a new `pkg/ast/overlay_test.go` (lighter unit coverage):

9. `TestLoad_OverlayPriorityOverDisk` — ast-layer mirror of (1) using `ast.LoadFile`, no plugin pipeline. Confirms overlay hit short-circuits disk read even when disk would succeed.
10. `TestLoad_OverlayMissingDiskFallback` — overlay key for a path the include never resolves to; disk-backed include still works.

Overlay-only files use `filepath.Join(t.TempDir(), name)` for the absolute path; the file is NOT written to disk.

#### Suggested Internals

1. **`internal/loadopt.Options` extension** — add `Overlay map[string][]byte` field (nil = no overlay). Keeps `Options` as a pure data carrier; alternative of storing a constructed `sourceReader` would require moving the interface out of `pkg/ast`.

2. **`WithOverlay` implementation** — trivial assignment, matching `WithBaseDir`/`WithFilename`:
   ```go
   func WithOverlay(overlay map[string][]byte) LoadOption {
       return func(o *loadopt.Options) { o.Overlay = overlay }
   }
   ```

3. **Overlay-aware `sourceReader`** — closure adapter built in `pkg/ast/load.go` after `loadopt.Resolve`:
   ```go
   func overlaySource(overlay map[string][]byte, fallback sourceReader) sourceReader {
       return sourceReaderFunc(func(p string) ([]byte, error) {
           if b, ok := overlay[p]; ok { return b, nil }
           return fallback.read(p)
       })
   }
   ```
   Returns map's stored slice directly — no copy.

4. **Non-absolute key diagnostic — emit at load start, once per load.** Sort by message for determinism. Append to `ld.diagnostics` before the first `loadFile` call.

5. **`glob.go` modification** — extend signature to `expandGlob(pattern string, extra []string) ([]string, error)` where `extra` is the sorted slice of absolute overlay keys. Match each `extra` against `matchDoubleStar(pattern, p)`, dedupe via `map[string]struct{}` or `slices.Sort`+`slices.Compact`. Caller (`handleInclude`) passes `ld.overlayPaths()` (cached sorted slice computed once per load).

6. **`pkg/loader/option.go` re-export** — one-liner: `func WithOverlay(overlay map[string][]byte) Option { return ast.WithOverlay(overlay) }`.

7. **Empty/nil map fast-path** — branch on `o.Overlay == nil || len(o.Overlay) == 0` at top of `Load*`; skip wrap + diagnostic helper. Avoids closure allocation in the common path.

8. **Bazel/Gazelle** — new test files in existing packages. Run `bazel run //:gazelle` after add. No `MODULE.bazel` changes.

#### Alternatives discussed

- **Overlay placement**: map on `loadopt.Options` (recommended) vs sourceReader on Options (would require moving interface) vs new internal package (over-architected).
- **Glob union**: extend `expandGlob` (single source of truth) vs union at `handleInclude` (duplicates matching logic).
- **Diagnostic timing**: load start (recommended, fires reliably) vs hit-time (would never fire — relative keys cannot match absolute lookups) vs call-time panic (violates functional-options pattern).
- **`[]byte` ownership**: borrow (recommended, zero-copy hot path) vs defensive copy (allocates on every load, unnecessary given documented mutation prohibition).

#### Recommendation + rationale

Adopt **map-on-Options + closure adapter + extended `expandGlob` + load-start diagnostic**.

- Smallest delta consistent with the Step 1 seam.
- No new package boundaries crossed — `sourceReader` remains private to `pkg/ast`; `internal/loadopt` stays a pure data carrier.
- Glob union semantics live where glob matching lives (single source of truth for `matchDoubleStar`).
- Diagnostics fire exactly when the user can act on them.
- Zero-copy ownership matches LSP reality (didChange events carry full-document bytes).

### Step 3 — `pkg/session` パッケージ (core)

**Functional requirements** (Contract):

```go
type Session struct{ /* unexported */ }
func New(rootPath string, opts ...loader.Option) (*Session, error)
func (s *Session) Snapshot(ctx context.Context) (*ast.Ledger, error)
func (s *Session) SetOverlay(absPath string, content []byte) error
func (s *Session) ClearOverlay(absPath string) error
func (s *Session) Overlays() map[string][]byte
func (s *Session) Reload(ctx context.Context) (*ast.Ledger, error)
func (s *Session) Close() error
```

- `Snapshot` は cache 済み ledger を返す。cache が無効なら同期 reload。
- `SetOverlay`/`ClearOverlay` は cache を invalidate。実際の再ロードは
  次の `Snapshot` 呼出か `Reload` で行う。
- 並行安全: 全メソッドは複数 goroutine から呼べる。Reload はシリアル
  (同時 2 つ走らない)。

**Touched modules**: 新規 `pkg/session/session.go`, `pkg/session/doc.go`,
`pkg/session/BUILD.bazel` (Gazelle 生成)。

**Verification**: `pkg/session/session_test.go` で
(a) `New` → `Snapshot` でディスク内容ロード、
(b) `SetOverlay` → `Snapshot` 反映、
(c) `ClearOverlay` 後のディスク復帰、
(d) 並列 `Snapshot` 100 goroutine 安全性。

**Quality requirements**: パッケージ godoc に Session lifecycle
(`New` → 多数の `Snapshot`/`SetOverlay`/`Reload` → `Close`) と並行性契約
を明文化。`Close` の冪等性も書く。

### Step 4 — Session 変更通知 API

**Functional requirements**:

```go
func (s *Session) Subscribe() (<-chan *ast.Ledger, cancel func())
```

- 各 Reload 成功時に、生きている購読チャネルへ最新 ledger を
  ノンブロッキング送信。
- チャネル容量は 1、満タンなら古い値を捨てて新しい値に置き換える
  ("latest-wins" セマンティクス)。
- `cancel()` 呼出で購読解除 + チャネル close。複数回呼んでも安全
  (sync.Once)。
- `Close()` は全購読者のチャネルを close。

**Touched modules**: `pkg/session/session.go`, `pkg/session/subscribe_test.go`。

**Verification**:
(a) `Subscribe` → `Reload` で値が届く、
(b) 2 連続 `Reload` で古い値が捨てられ最新だけ取れる、
(c) `cancel()` 後の `Reload` で goroutine リーク無し、
(d) `Close()` 後の receive が `ok == false` を返す。

**Quality requirements**: godoc に latest-wins セマンティクスと cancel の
sync.Once 安全性を明記。

### Step 5 — `cmd/beancount-lsp` scaffold

**Functional requirements**:

- stdio LSP サーバ起動。`initialize` / `initialized` / `shutdown` / `exit` を実装。
- capabilities は実装済み機能のみ true 宣言。
- `textDocument/didOpen|didChange|didClose|didSave` を受けて
  `Session.SetOverlay`/`ClearOverlay` を呼ぶ (diagnostics は Step 6)。
- root 解決: `initialize` の `workspaceFolders` 優先、フォールバックで
  最初の didOpen ファイルのディレクトリ。
- 起動ログ (stderr) と panic recovery。
- テスト容易化のため `Server` 構造体に `clock func() time.Time` を持たせる
  (Step 10 hover の context date 決定で利用)。

**Touched modules**: 新規 `cmd/beancount-lsp/main.go`, `server.go`,
`docsync.go`, `BUILD.bazel`、`go.mod` 更新、`MODULE.bazel` の `use_repo()`
追加。

**Verification**: in-process JSON-RPC で `initialize` リクエストペアの
ハンドリングを `bytes.Buffer` ベースに単体テスト。手動 VS Code/Neovim 接続
確認 (CI 必須ではない)。

**Quality requirements**: capability は実装したものだけ true。
`bazel run //:gazelle -- update-repos -from_file=go.mod` 後ビルド可。

### Step 6 — Diagnostics pipeline

**Functional requirements**:

- `Subscribe()` で受け取った ledger の `Diagnostics` をファイル別に集約し
  `textDocument/publishDiagnostics` で送出。
- Snapshot 完了ごとに対象ファイル全てを再送 (解消したファイルは空配列で
  クリア)。
- `ast.Position` (Line=1-based, Column=rune-based) → LSP `Position`
  (Line=0-based, Character=UTF-16) への変換ヘルパを新規モジュール内に置く。
- `didChange` を 100 ms debounce して `Session.SetOverlay` + 非同期 reload。

**Touched modules**: `cmd/beancount-lsp/diagnostics.go`,
`cmd/beancount-lsp/position.go` (UTF-16 変換)。

**Verification**:
(a) position 変換のテーブルテスト (ASCII / 3-byte UTF-8 / surrogate pair
(4-byte UTF-8) / TAB)、
(b) ledger を mock した publishDiagnostics ペイロードのスナップショット、
(c) in-process LSP クライアントで didOpen → didChange → publishDiagnostics
往復 1 ケース。

**Quality requirements**: UTF-16 変換は **ファイルごとに line offset
テーブルを 1 回構築、line 内は on-demand** (precompute と on-demand の
ハイブリッド)。

### Step 7 — `textDocument/formatting` + `textDocument/rangeFormatting`

**Functional requirements**:

- `documentFormattingProvider: true`, `documentRangeFormattingProvider: true`
  を宣言。
- documentFormatting: 全文置換。`format.Format(currentText)` の結果を 1 つの
  `TextEdit` にして返す。
- rangeFormatting:
  1. クライアント range (UTF-16) を byte offset に変換。
  2. 最新ソースを `syntax.Parse` し、トップレベル directive を列挙。
  3. byte range と重なる全 directive を抽出し、`[firstDir.Span.Start,
     lastDir.Span.End)` を edit 対象 range として確定 (union to whole-
     directive boundaries)。
  4. 部分文字列を `format.Format()` に通し、結果を 1 つの `TextEdit` で返す。
  5. 重なる directive が無ければ空配列 (no-op)。
  6. フォーマット結果が入力部分と同一なら空配列。
- `syntax.ErrorNode` を含む directive はパススルー (format.Format が既に
  そう挙動する)。明示的なスキップはしない。
- `pkg/format` 公開 API は不変。

**Touched modules**: `cmd/beancount-lsp/formatting.go`, `server.go`,
`position.go` (byte offset → LSP Position 逆変換)。

**Verification**: `cmd/beancount-lsp/formatting_test.go` で
(a) document fmt (既存 Step の継承)、
(b) range が 1 directive をピンポイント指定、
(c) range が 2 directive をまたぐ、
(d) range が directive 途中で切れる → union 拡張確認、
(e) range が空白行のみ → 空配列、
(f) range が ErrorNode 含む → 他 directive は整形・エラー directive は
  パススルー、
(g) フォーマット済みソースに対する range request → 空配列。

**Quality requirements**: range 拡張ポリシー (「重なる全 directive の
union」) を `formatting.go` ファイル冒頭に 2-3 行で記す。

### Step 8 — `textDocument/documentSymbol`

**Functional requirements**:

- 開いている file URI を持つ `ast.File` を ledger から特定し、`Directives`
  を SymbolKind に対応付けて返す。
- 階層: Transaction を `DocumentSymbol` ツリーのルートとし、Posting を子に。
- ラベル: Open → account 名、Transaction → narration (or payee)、Include →
  パス、Commodity → currency、等。

**Touched modules**: `cmd/beancount-lsp/symbol.go`。

**Verification**: ledger を fixture から構築し、期待 symbol ツリーと
`cmp.Diff` する単体テスト。

**Quality requirements**: SymbolKind 割当は LSP 仕様の妥当な近似。
`docs/architecture/lsp-overview.md` に 1 表に整理する (Step 13)。

### Step 9 — `textDocument/definition` (include / account / commodity)

**Functional requirements**:

- `definitionProvider: true` 宣言。
- リクエスト処理:
  1. UTF-16 position → byte offset。
  2. `locate.go` の `LocateAt(file *syntax.File, offset int) Located` で
     cursor 下の最も内側の token とその所属 directive を取得。
  3. token 種別で分岐:
     - **include path**: 所属 directive が `IncludeDirective` でかつ token
       が string リテラル → ledger の include 解決結果から対応する絶対 path
       を引き、`file://` URI で `Location` を返す。
     - **ACCOUNT token**: ledger 全体から `*Open` を線形検索 (account 名
       一致、canonical 順で最初)。
     - **CURRENCY token**: ledger 全体から `*Commodity` を線形検索
       (`Currency` 一致、canonical 順で最初)。
- declaration 無しは空配列。

**Touched modules**: 新規 `cmd/beancount-lsp/locate.go`, `definition.go`。

**Verification**: `definition_test.go`, `locate_test.go` のテーブルテスト:
include / account (open 側・使用側・無宣言) / currency (commodity 側・使用
側・cost spec 内・price annotation 内・無宣言)。

**Quality requirements**: 線形検索は今ステップでは許容 (5k directive で
sub-ms)。index 化は future work。token 引き当ては `offset == token.End`
を inclusive にする (godoc に 1 行記載)。

### Step 10 — `textDocument/hover` (account / commodity with context date)

**Functional requirements**:

- `hoverProvider: true`。
- リクエスト処理は `locate.go` で cursor 下 token と所属 directive を取得して分岐。

#### Account hover

- 表示: `Open.Meta`、`Currencies`、Booking method、context date 時点の
  inventory 残高 (`pkg/inventory` で算出)。
- context date は所属 directive の `DirDate()`、無ければ
  `server.clock()` (Step 5 で注入)。

#### Commodity hover (currency token)

1. Context date を決定:
   - 所属 directive が dated (`DirDate()` 非 zero) → その値
   - 所属 directive が dateless (Option, Plugin, Include, Pushtag, Poptag) →
     `server.clock()`
   - directive 外 → `server.clock()`
2. `*Commodity` directive を引いて `Meta` を取得 (無ければ Meta セクション
   省略)。
3. context date 時点で有効な最新 price を引く: `Ledger.All()` 全走査で
   `*Price` のうち `Commodity == cursor通貨` かつ `Date <= contextDate`
   のうち最大 `Date` のもの (同日内は canonical 順で最後)。base 側マッチのみ
   (quote 側の逆数表示はしない)。
4. Markdown で表示:
   ```
   **USD** (commodity)

   As of YYYY-MM-DD: 110.50 JPY  *(price from YYYY-MM-DD)*

   *Metadata*
   - name: "US Dollar"
   ```
   価格無し: `No price recorded as of YYYY-MM-DD.`
   Meta 空: Metadata セクション省略。
- 他の token kind (NUMBER, STRING, DATE 等) → null。

**Touched modules**: `cmd/beancount-lsp/hover.go`, `locate.go` を Step 9 と共有。

**Verification**: `hover_test.go` で account (既存テスト継承)、commodity
(txn posting / price directive 自身 / commodity directive 自身 / price なし
/ Meta 空 / dateless directive 内) のテーブルテスト。

**Quality requirements**: price 検索は線形 O(N)、5k directive で問題なければ
許容。Markdown は `MarkupContent{Kind: Markdown}` で返す。
context date 決定ルールを hover.go の commodity 分岐に 3-4 行で記す。

### Step 11a — Completion 基盤 + static (account / currency / keyword / flag / tag / link)

**Functional requirements**:

- `func (s *Server) Completion(ctx, params) (*lsp.CompletionList, error)` を実装。
- Snapshot 取得 → UTF-16 position → byte offset → 該当行 current text →
  `classifyContext(line[:col])` で `ContextKind` を判定 (account / currency
  / keyword / flag / tag / link / unknown / inString)。
- 各 ContextKind の候補ソース:
  - **account**: `*Open.Account` 集合 (close 済みも含む)
  - **currency**: `*Commodity.Currency` ∪ `*Open.Currencies` の和
  - **keyword**: 静的リスト (open/close/commodity/balance/pad/note/document/
    event/query/price/txn/option/plugin/include/pushtag/poptag/pushmeta/
    popmeta/custom)、先頭日付有無で directive キーワード集合と header 集合を切替
  - **flag**: `*`, `!`
  - **tag**: 全 directive `Tags` (Transaction / Note / Document)
  - **link**: 全 directive `Links`
  - **inString**: 空配列
- `triggerCharacters`: `[":", "#", "^"]` のみ。`"`, `*`, `!` は誤起動回避で
  除外、英数字入力時はクライアント自動補完に任せる。

**Touched modules**: 新規 `cmd/beancount-lsp/completion.go`,
`completion_context.go`。`server.go` で capability + router 登録。

**Verification**: `completion_context_test.go` (classify 15-20 ケース)、
`completion_test.go` (各 ContextKind 1-2 ケース合計 8-10 ケース、Snapshot
fixture はメモリ上構築)。

**Quality requirements**: 1000 directive で 1 リクエスト < 5 ms 目標 (実測、
fail にはしない)。重複排除は `map[string]struct{}` + `slices.Sorted` で
安定順序。

### Step 11b — Completion: payee / narration

**Functional requirements**:

- `classifyContext` を拡張: cursor が transaction header 行
  (`YYYY-MM-DD [txn|*|!] ...`) にいるとき、トークン位置から
  `payeeContext` / `narrationContext` を判定 (`"` の対のうち何個目かで分岐)。
- `payeeSource(snapshot) []string`: 全 `*Transaction` から `Payee != ""` を
  集めて頻度降順 → 同頻度はアルファベット順。
- `narrationSource(snapshot, currentPayee, currentFile, currentAccounts)`:
  優先順位は **ユーザー指定** に従い、Group 1 > 2 > 3 を sortText で表現:
  1. `currentPayee != ""` のときその payee の transaction の narration
  2. **同一アカウント** (`currentAccounts`) に触る transaction の narration
  3. **同一ファイル** (`currentFile`) の transaction の narration
- 同一文字列は最も優先度の高い group のみで出す (sortText prefix `0/1/2`)。
- 空 payee グループ (Payee 未指定 transaction の narration) は currentPayee=""
  時の Group 1 には **しない** (Group 2/3 に降格、候補爆発防止)。
- `currentPayee` / `currentFile` / `currentAccounts` の抽出は新設
  `findEnclosingTransaction(snapshot, uri, pos)` で。

**Touched modules**: `completion.go`, `completion_context.go`, 新規
`completion_sources.go`, `enclosing.go`。

**Verification**:
(a) header 行内位置判定 (date 直後 / flag 直後 / 1 つ目の `"` 中 / 2 つ目の
`"` 中 / `"` の閉じた後) 6-8 ケース。
(b) payee 補完 2 ケース。
(c) narration 補完 4 ケース (currentPayee 一致 / 同一アカウント fallback
(優先) / 同一ファイル fallback / 全該当して group 順序確認)。

**Quality requirements**: 同一 payee フィルタは線形 (10k transaction で
1 ms)。実測必要時のみ最適化。

### Step 11c — Completion: metadata key / value

**Functional requirements**:

- `classifyContext` を拡張: 行頭がインデント + `[a-z][a-z0-9_-]*:?` の
  パターンなら metadata 行。`:` の前なら `metaKeyContext`、後なら
  `metaValueContext`。
- **metaKey**: ledger 全体の `Metadata.Props` (Transaction レベル +
  Posting レベル + Open/Close 他) からキー集合を作り頻度降順。先頭マッチは
  LSP クライアント側 filter に任せる (CompletionItem.filterText に key)。
- **metaValue**: 行先頭から `^\s+([a-z][a-z0-9_-]*):` で `currentKey` を
  抽出 → そのキーで使われた `MetaValue` の文字列表現を集合化:
  - `MetaString` はクォート付きで挿入 (`"value"`)。`inString` フラグ時は
    内側のみ。
  - `MetaAccount`, `MetaCurrency`, `MetaTag`, `MetaLink` はそのまま挿入
  - `MetaNumber`, `MetaAmount`, `MetaDate`, `MetaBool` は候補に出さない
    (誤挿入リスク高、補完価値低)
- 重複排除 + 頻度降順 → 文字列順。

**Touched modules**: `completion.go`, `completion_context.go`,
`completion_sources.go`。

**Verification**: metadata 行検出 4-6 ケース、metaKey 2 ケース、
metaValue 3 ケース (string / account / 候補なし kind)。Transaction.Meta と
Posting.Meta の両方から候補が出ることを 1 ケースで確認。

**Quality requirements**: 値補完で `MetaString` をクォート付き挿入する際、
cursor が既に `"` の中なら裸の `value` を挿入 (insertText の `"` 重複防止)。

### Step 12 — `workspace/didChangeWatchedFiles`

**Functional requirements**:

- `initialize` 応答で
  `workspace.didChangeWatchedFiles.dynamicRegistration: true` を宣言。
- `initialized` 受信時に `**/*.beancount` 監視を登録。
- クライアントイベントを Session 状態にマッピング:
  - `Created` / `Changed` (overlay 無しのファイル) → `Session.Reload` トリガ
  - `Changed` (overlay あり) → overlay 優先のまま何もしない (godoc 化)
  - `Deleted` → overlay クリア + reload

**Touched modules**: 新規 `cmd/beancount-lsp/watch.go`, server 初期化部。

**Verification**: in-process JSON-RPC テストでイベント送信 → `Session.Reload`
が呼ばれることを確認 (Session を inject 可能にしておく)。

**Quality requirements**: fsnotify はリンクしない (依存ゼロ)。Phase 10 で
同じ Session API を再利用する旨を watch.go の冒頭 godoc に明記。

### Step 13 — Smoke test + `docs/architecture/lsp-overview.md`

**Functional requirements**:

- end-to-end smoke test: 一時ディレクトリに 2 ファイル ledger を書き、
  `cmd/beancount-lsp` を pipe で起動し、initialize → didOpen →
  publishDiagnostics 受信 → format リクエスト → 結果検証。
- `docs/architecture/lsp-overview.md` に以下を記載: overlay と Session の
  関係 / UTF-16 変換 / 再ロード戦略 / fsnotify を避ける理由 / Document
  symbol の SymbolKind 割当表。PLAN.md は触らない。

**Touched modules**: `cmd/beancount-lsp/smoke_test.go`,
`docs/architecture/lsp-overview.md`。

**Verification**: smoke test が CI でグリーン、タイムアウト 30s 以内。

**Quality requirements**: CLAUDE.md の Go style に従い、architecture doc は
簡潔に。

## Alternatives discussed (key decisions)

### LSP ライブラリ: `go.lsp.dev/protocol` + `go.lsp.dev/jsonrpc2` (採用)

- **却下**: `sourcegraph/jsonrpc2` + 自前 LSP 型 → LSP 3.17 型を全部書く
  メンテ負担が Phase 11 のスコープを超える。
- **却下**: 完全自前 → 数 KLOC、メンテが永続化、差別化に貢献せず。
- **不可**: `golang.org/x/tools/gopls/internal/lsp/protocol` → internal。

### Session の置き場所: 新規 `pkg/session` パッケージ (採用)

- **却下**: `pkg/loader` の中 → loader の 1-shot Load* 契約を濁す。
- **却下**: `cmd/beancount-lsp` に閉じる → Phase 10 で再利用不可。

### オーバーレイの実装層: pkg/ast loader に seam (採用)

- **却下**: `pkg/loader` 上層に被せる → include 解決を再実装する致命的負債
  (handleInclude / expandGlob / 循環検出の二重管理)。
- **却下**: `fs.FS` 抽象を流す → 絶対パス前提の既存セマンティクスと不整合。

### オーバーレイ注入: `WithOverlay` LoadOption (採用)

- **却下**: Session メソッドにのみ持たせる → テスト/CLI 用途で過剰拘束。
- **却下**: `fs.FS` 流入 → 上記同様。

### 変更通知 API: `Subscribe() (<-chan, cancel)` latest-wins (採用)

- **却下**: `OnUpdate(callback)` → callback が block すると Session が固まる
  契約上の濁り。LSP は `select { case <-sub: ... case <-ctx.Done(): ... }`
  で書きたい場面が多い。
- **却下**: 両方提供 → API 表面が膨らみ Phase 11 単独要件には不要。

### UTF-16 変換: LSP サーバ内に局所化 (採用)

- **却下**: `pkg/ast.Position` に `LSPLine/LSPColumn` を追加 → CLI 用途で
  無駄、`Position` の契約が二股に。
- **却下**: 完全 on-demand → 大ファイルの hover/definition が遅い。
- 採用案: 行 offset テーブル precompute + 列方向 on-demand のハイブリッド。

### 再ロード戦略: debounce → Session シリアル reload → Subscribe 配信 (採用)

- **却下**: リクエスト毎 ad-hoc 再構築 → hover/definition/completion が頻発
  で体感性能が劣悪。
- **却下**: per-file パースキャッシュ → AST 差分マージという別問題、
  Phase 11 のスコープを大幅超過。

### Range formatting 実装方式: クライアント range を directive 境界に union 拡張 (採用)

- **却下**: `pkg/format` に `FormatDirective(d ast.Directive)` を追加 →
  CST 経由でないと trivia 復元不可、波及範囲大、overengineering。
- **却下**: 全文 fmt の diff → range 外を勝手に変えるのは LSP の rangeFmt
  意味論違反。

### Completion コンテキスト判定: AST + 簡易レキシカル (採用)

- **却下**: CST 直接利用 → 不完全入力時の CST 挙動が固まっておらず、
  Phase 11 で lifecycle を背負うのは過剰。
- **却下**: 専用 partial parser → 保守コスト増。
- 採用案 (簡易レキシカル) は誤判定時に「無補完」へ倒れる failure mode で
  LSP として行儀が良い。

### Completion インデックス: 都度走査 (採用)

- **却下**: Snapshot に補完用 derived data → `pkg/session` に LSP の都合が
  漏れる layering 違反。
- **却下**: LSP 層で LRU キャッシュ → 性能要件が出てから導入で良い。

### Narration 優先順位: 同一 payee → 同一アカウント → 同一ファイル (採用)

- **ユーザー指定で採用**: planner の初回案 (同一ファイル → 同一アカウント)
  はユーザー判断により逆順に変更。

### Commodity hover の context date: dateless / orphan は `time.Now()` (採用)

- **却下**: ledger 最終日フォールバック → 過去日 ledger 編集中の dateless
  位置で誤解を招く。
- 採用案: `server.clock()` でテスト容易性確保 (`clock func() time.Time` を
  Step 5 で server に注入)。

### Commodity hover の通貨ペア方向: base 側マッチのみ (採用)

- **却下**: quote 側の逆数表示 → 精度問題と意味論的混乱を呼ぶ。Beancount の
  Price directive は方向を持つ非対称関係。

### Trigger characters: `[":", "#", "^"]` (採用)

- **却下** (planner の元案からの修正): `"`, `*`, `!` を含む → 普通の文字列
  入力や flag 切替で誤起動。クライアントの英数字自動補完で実用上十分。

### Range formatting で ErrorNode を含む directive: パススルー (採用)

- **却下**: 明示スキップ → silent surprise (ユーザーが format 要求して
  特定 directive だけ整形されないのは UX 上良くない)。
- 採用案: `format.Format` の既存パススルー挙動をそのまま継承。

## Recommended approach (summary)

15 ステップ (Step 1-13、ただし 11 は 11a/b/c の 3 サブステップ) を上記順序
で実装する。前提整備 (Steps 1-4) を最初にコミット可能な単位で出し、LSP
スキャフォールド (Step 5) → Diagnostics (Step 6) で「動く LSP」の最初の
出荷可能状態を作る。以降 Step 7-12 は機能ごとに並列化可能。最後に
Step 13 で smoke test + アーキテクチャ doc を入れる。

各ステップは 1 generator セッションで完了できる粒度。Phase 4 で各ステップの
内部詳細設計 (`#### Contract` + `#### Suggested Internals`) を順次詰める。

## Verification (end-to-end)

- `bazel build //...` および `bazel test //...` が全ステップ後にグリーン。
- `bazel run //:gazelle` および (新依存追加時) `bazel run //:gazelle --
  update-repos -from_file=go.mod` を実行。
- Step 13 の smoke test を CI で実行 (タイムアウト 30s)。
- VS Code または Neovim LSP クライアントで手動接続し、各機能 (diagnostics,
  formatting, completion, definition, hover, document symbols) が機能する
  ことを確認 (Phase 11 完了時の受入確認)。
- 開発ブランチ: `claude/intelligent-hawking-KgHRk`。
