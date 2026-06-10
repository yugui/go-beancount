# Plan: `textDocument/references` for beancount-lsp

## Goal

`cmd/beancount-lsp` に LSP `textDocument/references` を追加し、カーソル下の **tag / link /
account / commodity** に対して、それを参照している箇所をレッジャ全体（＋現在の文書）から見つけられるようにする。
ユーザーの目的は「リンク・タグに合致する directive を確認する」「ある account に触れている directive を確認する」導線。

## Scope

- **IN**: tag, link, account, commodity の References。
- **OUT**: workspace symbol、call hierarchy、その他 references 以外。

## ロック済み設計判断（要件・再オープンしない）

1. 対象は tag / link / account / commodity の4種。
2. account マッチは **完全一致のみ**（rename の階層マッチは使わない）。
3. tag / link は **AST セマンティック `.Tags`/`.Links`（Transaction/Note/Document）を真実の源**とし、
   `pushtag`/`poptag` 暗黙付与の transaction も含める。粒度は **directive の先頭行**。
   **位置健全性ガード必須**: `span.Start.Filename != "" && s.sourceBytesFor(...) != nil` の時のみ emit
   （`Snapshot` は booking + plugin まで走るため synthetic directive が実在しうる）。
4. account / commodity は **各ソースファイルを再パースした CST トークン走査（完全一致）**。推論補完値・plugin
   生成 directive は字句が無く構造的に除外（＝飛ぶ先の無い物は返さない、正しい挙動）。
   `ReferenceContext.IncludeDeclaration` を account（OpenDirective）/ commodity（CommodityDirective）で honor。
   tag/link では no-op。
5. `renameTarget`（rename.go:112）をカーソル→(kind,name) 分類に再利用。`renameFileSet`（rename.go:180）を
   共有 `ledgerFileSet` に一般化し rename と references の双方から使う。
6. 最終 Location は (filename, start offset) でソートし (filename, range) で dedup。
7. 開発ブランチ: `claude/beancount-lsp-features-5tjilw`。

## 設計の中核原則（位置の健全性）

`SessionAPI.Snapshot` は lowering 止まりではなく `pkg/loader` の `runPipeline`→`applyDefault`
（loader.go:144-173）で **booking → user plugins → pad/balance/validations** まで実行する。よって ledger に
推論補完値・plugin 生成 directive が実在しうる（synthetic な Posting の Amount/Cost は Span を持たない:
`inventory/reducer.go:322`、pad 合成 transaction はトリガ Pad の Span をコピー: `pad/plugin.go:390`）。

**原則: References は実ソース位置に裏打ちされた出現だけを返す。**
- account/commodity は CST トークン走査なので推論補完・synthetic を構造的に除外（追加ガード不要）。
- tag/link (A 系統) のみ AST を使うので上記の位置健全性ガードで synthetic directive を弾く。

**却下した代替案**: pushtag/poptag スコープを CST から自前再計算して全てを位置付きにする案 → `ast/lower.go` の
タグマージ再実装になり重複・脆弱。実装済みの `.Tags` 利用＋ガードを採る。

## 再利用する既存資産

- `LocateAt` / `findToken`（locate.go）: カーソル位置のトークン特定。
- `renameTarget`（rename.go:112）: TAG/LINK→sigil除去名、ACCOUNT/CURRENCY→Raw に分類。**そのまま流用**。
- `renameFileSet`（rename.go:180）→ `ledgerFileSet` に一般化。
- position 変換群（position.go）: `computeLineOffsets` / `lspPositionToByte` / `byteOffsetToLSP`、行末算出は
  `lineBytes`（position.go:154）のロジックを利用。
- `s.sourceBytesFor(path)`: overlay/ディスクからソース取得。
- `syntax.Node.Tokens()`（node.go）、`syntax.Node.Kind` と NodeKind 定数（node_kind.go:
  `PushtagDirective`/`PoptagDirective`/`OpenDirective`/`CommodityDirective`）。
- AST: `Transaction/Note/Document.Tags/.Links`、`*.Account`（directives.go）、`Ledger.All()`/`Ledger.Files`
  （ledger.go）、`ast.Position.Offset`（ast.go:13, `directiveHeaderRange` のバイトオフセット源）。

---

## Steps

### Step 1 — `ledgerFileSet` 抽出（振る舞い不変リファクタ）

**Functional requirements**
- 共有 unexported メソッド `(s *Server) ledgerFileSet(ctx, current string) []string` が、現 `renameFileSet`
  と同一挙動（`current` を先頭、続いてスナップショットの `ledger.Files[].Filename`、dedup、スナップショット
  エラーはログして非致命）を持つ。
- `handleRename` は `ledgerFileSet` を呼ぶ。rename の caller-observable な挙動は不変。

**Modules / files**
- `cmd/beancount-lsp/rename.go`（メソッド改名、呼び出し箇所 rename.go:94 更新、ログ接頭辞をハンドラ非依存に）。

**Verification**
- `bazel run //:gazelle` → `bazel build //...` → `bazel test //cmd/beancount-lsp/...`。
  既存 rename テストが緑のままであることがこのステップの全証明。

**Quality requirements**
- スナップショットエラーのログ経路（rename.go:197-199）を保持し握り潰さない。

### Step 2 — `textDocument/references` 実装

**Functional requirements**
- `dispatch`（recover.go）が `"textDocument/references"` を `s.handleReferences` に振り分け、
  `ServerCapabilities.ReferencesProvider = true`（handlers.go）。
- `handleReferences` は `protocol.ReferenceParams` を unmarshal し、definition.go の nil/empty 契約を踏襲
  （`sourceBytesFor` nil / token nil / `renameTarget` !ok / session・snapshot nil → `[]protocol.Location{}`）、
  それ以外はソート＆dedup 済み `[]protocol.Location` を返す。null やエラーは返さない。
- **account / commodity**（ACCOUNT/CURRENCY）: `ledgerFileSet` 全ファイルの CST 再走査で `tok.Raw == name`
  完全一致。`Assets:Cash` 指定時 `Assets:Cash:Sub` は除外。`IncludeDeclaration=false` で宣言 directive
  （account=OpenDirective, commodity=CommodityDirective）内の当該トークンを除外。
- **tag / link**（TAG/LINK）: (A) `*Transaction`/`*Note`/`*Document` で `.Tags`/`.Links` が `name` を含む物を
  directive 先頭行レンジで1件ずつ（位置健全性ガード適用）。(B) `PushtagDirective`/`PoptagDirective` ノード内の
  TAG/LINK トークン（`Raw[1:]==name`）を CST 走査で1件ずつ。`IncludeDeclaration` は no-op（godoc 明記）。
  A と B は集合が交わらない（A は Transaction 等の span、B は pushtag/poptag ノードのみ）。
- 一致なしは常に空リスト。

**Modules / files**
- 新規 `cmd/beancount-lsp/references.go`: `handleReferences` / `referencesForToken` / `referencesForTagLink` /
  小ヘルパ `tokenRange`・`directiveHeaderRange`（`directiveHeaderRange` は `span.Start.Offset` から直接組み、
  行末は `lineBytes` 相当ロジックで算出）。
- `cmd/beancount-lsp/recover.go`: dispatch に1 case。
- `cmd/beancount-lsp/handlers.go`: `ReferencesProvider: true`。
- 新規 `cmd/beancount-lsp/references_test.go`。

**Verification**
- `bazel run //:gazelle` → `bazel build //...` → `bazel test //cmd/beancount-lsp/...`。
- テストシナリオ（`definition_test.go`/`rename_test.go` のパターン: `newSymbolServer`・temp ファイル・`await*`
  リトライ、`include` 込みの複数ファイル fixture）:
  - **account 完全一致**: `Assets:Cash` が自身の open・balance・該当 posting 行にヒット、`Assets:Cash:Sub` は
    含まれない、`IncludeDeclaration=false` で open トークン除外。
  - **commodity**: `USD` の全トークン出現、`IncludeDeclaration=false` で `commodity USD` 行のトークン除外。
  - **tag（インライン＋暗黙）**: `pushtag #trip` 配下のインラインタグ無し transaction 2件＋インライン `#trip`
    1件 → (A) から directive 先頭行3件、(B) から pushtag/poptag トークン件。件数とレンジを厳密に検証
    （A/B の排他性 + dedup を証明）。
  - **link**: tag と同様。
  - **カーソルが記号上にない**（空白/NUMBER）→ 空。
  - **synthetic 除外（commodity）**: booking が第2 posting の通貨を推論する balanced transaction → その通貨の
    references はソース記載分のみ（推論分は出ない）。
  - **synthetic 除外（pad）**: `pad`/`balance` ペア → pad 合成 transaction が account/commodity の Location に
    寄与しない。
  - **複数ファイル**: `include` 先の `^link`/account が正しいファイル URI で出る。

**Quality requirements**
- 整形済みリクエストに対し parse 不能/欠損でも null・エラーを返さず `[]protocol.Location{}`（definition.go 準拠）。
  `sourceBytesFor` nil のファイルは中断せずスキップ（rename.go:96 準拠）。
- スナップショットエラーは `s.logger.Printf` でログ（既存ハンドラと一貫）、トークン単位のノイズログはしない。
- 出力は (filename, start offset) ソート後 (filename, range) dedup で決定的に。
- doc コメント（CLAUDE.md 準拠）: `handleReferences` に外部契約（4種・account 完全一致・tag/link の
  IncludeDeclaration no-op・実ソース裏打ち出現のみ）。`tokenRange`/`directiveHeaderRange` は1行の不変条件のみ。

---

## Alternatives discussed（planner）

- **`directiveHeaderRange` の形**: 先頭行全体（採用） vs date トークンのみ vs ゼロ幅。先頭行全体が良いクリック
  ターゲットかつ `span.Start.Offset`＋既存行末ロジックで最小実装。date トークンのみは AST 経路で CST 再パースが
  必要になり本末転倒、ゼロ幅は UX 不良。
- **directive 種別の判定方法**: per-directive CST walk（採用） vs flat `Tokens()`＋別途種別逆引き。前者は
  `node.Kind` を1回読むだけで IncludeDeclaration 除外と pushtag/poptag 選別を同一パスで解決。
- **IncludeDeclaration 対応の是非**: honor（採用, =ロック判断） vs 常時 include。per-directive walk で
  ほぼ無コストなので honor。
- **`ledgerFileSet` の配置**: `rename.go` に unexported メソッドのまま（採用） vs 新ファイル。25行ヘルパに新規
  ファイルは過剰。
- **tag/link スコープ**: AST `.Tags` 利用（採用） vs CST から pushtag スタック再計算（lowering 再実装で却下）。

## Recommended approach

2ステップ（リファクタ→機能実装）。`directiveHeaderRange` は `span.Start.Offset` 直接利用、per-directive CST
walk で IncludeDeclaration を honor、`ledgerFileSet` は `rename.go` 据え置き。tag/link は AST `.Tags`/`.Links`
＋位置健全性ガード、account/commodity は CST トークン完全一致走査。
