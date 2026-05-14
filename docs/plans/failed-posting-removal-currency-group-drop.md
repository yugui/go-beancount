# Plan: pkg/inventory.Reducer.visitTxn の `TODO(failed-posting-removal)` を解消する

## Context — なぜこの変更を行うか

`pkg/inventory/reducer.go:246-250` に長らく残されている TODO は、go-beancount の partial-booking 意味論が上流の Python beancount と乖離していることを示している。

上流 beancount (`book_reductions` in `booking_full.py`) は、ある posting の booking が失敗すると、同じ **weight currency group**（cost / price / amount の precedence で決まる weight currency を共有する集合）に属する全 posting を `txn.Postings` から原子的に削除する。一方 Go 実装は失敗 posting 1 件だけを `addPreserved` でそのまま slice に残すため、partial-booking で生成される transaction の構造が上流と一致しない。

この差分は (a) `docs/plans/cost-holder-interface.md:46-49` に明記された上流挙動を将来 beancompat fixture で再現する際の障害になり、(b) 失敗を含む transaction の `txn.Postings` 構造が「中途半端な状態」になり下流 (printer / validator) で扱いにくい、という二つの問題を生む。

本計画は upstream の currency-group atomic semantics を Go 側で再現することを目的とする。実装範囲は `pkg/inventory/reducer.go` を中心とした internal 改修で、`bookOne` などの下層 API シグネチャは変えない。

## Goal

`Reducer.visitTxn` で `bookOne` (augment/reduce 問わず) が失敗した posting の weight currency group を、`txn.Postings` / `BookedPosting` 集合 / inventory state から原子的に取り除き、別 group の posting はそのまま保つ。Transaction 自体は引き続き emit する（残存 posting が 0 件でも emit する）。

## Scope

### In
- `pkg/inventory/reducer.go` の `visitTxn` (392-536)、`postingResolution` (200-371)、`stateTrace` (615-) 関連の改修
- bookOne 失敗時の inventory mutation rollback
- weight-currency による posting の group 識別（Pass 1 ＝ `PostingWeight`、Pass 2 ＝ `residual.Currency`）
- 既存テストの再合格と、新挙動に対する回帰テスト追加
- 必要に応じた Bazel/Gazelle 再生成

### Out
- `bookOne` / `bookReduce` / `bookAugment` 内部の改修や signature 変更
- `Inventory.Add` / `Inventory.Reduce` / `ResolveCost` の API 変更
- `solveResidual` / `flagAmbiguousUnknowns` の semantics 変更（unknown 解消失敗は別軸のエラーで、group drop の責務範囲外）
- `pkg/printer` / serializer / beancompat fixture の出力形式変更
- 新規診断コード追加（drop された posting 個別への info diagnostic 等は別 PR で検討）

## 確定設計方針（ユーザとの Q&A により決定）

- **D1**: `bookOne` が返す **すべての** エラー（augment の `Inventory.Add` 失敗 = `CodeMixedInventory` 等を含む）で group drop を発動する。例外: `CodeAugmentationRequiresCost` + cost number missing の defer 経路（Pass 2 で再試行されるので Pass 1 では drop にしない）。
- **D2**: ある group の全 posting が失敗して `txn.Postings` が空の transaction が emit されるケースを許容する（上流一致）。
- **D3**: Group identity は二本立て:
  - Pass 1: `PostingWeight(&pr.postings[i])` の `Currency`、エラー時は `p.Amount.Currency` にフォールバック（`p.Amount != nil` は構造的に保証）。**Unknown sentinel は導入しない**。
  - Pass 2: `residual.Currency` で確定。
- **D4**: `stateTrace` に group-level checkpoint/commit/rollback を追加し、failed group の inventory mutation を巻き戻す。
- **D5**: Posting の出力順は入力順を維持。Group はメタ情報のみで `txn.Postings` slice の並びには影響しない。
- **D6**: Pass 2 で auto-posting の residual currency が **drop 済み** group と一致する場合、auto-posting も同 group に join して drop する（upstream の `interpolate_group` と意味的に等価）。

## Steps（順序付き）

### Step 1: `stateTrace` に group checkpoint / rollback を導入

- **機能要件**:
  - 1 transaction 処理中に複数 group のうち一部のみ rollback できる。
  - `enterGroup(id) / commitGroup(id) / rollbackGroup(id)` の 3 メソッドを公開。
  - group commit が来るまで mutation は live state (`r.state`) に到達しない（per-group snapshot で `prepareForEdit` 時に lazy snapshot を取り、commit で破棄、rollback で snapshot を live に書き戻す）。
  - `before` map（visitor へ渡る既存契約）の値は最初の touch 時のものを維持。
- **影響モジュール**:
  - `/home/user/go-beancount/pkg/inventory/reducer.go`
    - `stateTrace` struct（615 行付近）にフィールド追加
    - `prepareForEdit`（636-649 付近）に group-snapshot logic を追加
    - 新規メソッド: `enterGroup` / `commitGroup` / `rollbackGroup`
- **検証**:
  - `reducer_test.go` に `stateTrace` 単体テスト追加（enter→mutate→rollback で live state 不変、enter→mutate→commit で反映、独立 group の干渉なし）
- **品質要件**:
  - Group が 1 つだけの大多数 txn ではオーバーヘッドが O(touched accounts) の clone 1 回相当に留まる
  - 既存の `before` map 契約は破らない

### Detailed Design

#### Contract

##### 新規メソッドのシグネチャ

`stateTrace` に以下 3 メソッドを追加する。`groupToken` は `stateTrace` が払い出す
opaque なハンドル型（comparable であること以外は内部実装）。

```go
// enterGroup opens a checkpoint scope. Until the matching
// commitGroup/rollbackGroup, every prepareForEdit call that touches an
// account for the first time *within this scope* records a restore
// snapshot for that account. enterGroup takes no group identifier:
// the weight currency that names the group is only known after bookOne
// runs, so it is supplied later, at commitGroup time. Returns an
// opaque token identifying the scope.
func (st *stateTrace) enterGroup() groupToken

// commitGroup closes the scope opened by tok and files the scope's
// snapshots under key (the group's weight currency string). The live
// mutations in st.state are NOT undone. Snapshots are retained, not
// discarded: a currency group may span several enter/commit cycles
// (one per posting in input order), and a later rollbackGroup(key)
// must still be able to undo all of them. Filing is first-touch-wins:
// if an account already has a snapshot under key (from an earlier
// cycle of the same group), the new scope's snapshot for that account
// is dropped, because the earlier one is closer to the group's true
// pre-state.
func (st *stateTrace) commitGroup(tok groupToken, key string)

// rollbackGroup undoes every mutation filed under key, restoring each
// affected account's inventory in st.state to the snapshot taken when
// that group first touched it. It is idempotent and safe to call with
// a key that was never committed (no-op). After rollbackGroup the
// group's snapshots under key are consumed.
func (st *stateTrace) rollbackGroup(key string)
```

##### ライフサイクル規則（ロック対象）

1. **enter → commit/rollback のペアリング**: `enterGroup` で開いた scope は、その
   token を引数に取る `commitGroup` で閉じる。呼び出し側（Step 3/4）は
   **1 posting につき `enterGroup` → `bookOne` → `commitGroup`** を必ず完結させる
   規約とする。失敗 posting も例外ではない: 失敗時も `commitGroup(tok, key)` を
   呼んで snapshot を key 配下に file し、その後 drop は `postingResolution` 側の
   mark で表現する（Step 2/5）。`stateTrace` は「token を直接 rollback する」
   経路を **提供しない** — rollback は常に key 単位（`rollbackGroup(key)`）。
2. **group の非ネスト性**: 同時に open 状態の scope は高々 1 つ。`enterGroup` を
   commit せずに再度 `enterGroup` するのは規約違反であり、`stateTrace` は
   panic してよい（misuse の早期検出）。
3. **二重 commit / 二重 rollback**: 同一 token に対する 2 回目の `commitGroup` は
   規約違反（panic 可）。同一 key に対する 2 回目以降の `rollbackGroup` は no-op。
4. **enter せずに commit**: 規約違反（panic 可）。
5. **scope 外の `prepareForEdit`**: `enterGroup` していない状態での
   `prepareForEdit` は **従来どおりの挙動**（`st.before` への lazy snapshot のみ、
   group snapshot は記録しない）。既存の `state_trace_test.go` のテストは
   この経路を通り、無改変で合格しなければならない。

##### `prepareForEdit` の externally observable な挙動

`prepareForEdit` の戻り値・`st.before` への副作用・冪等性は **一切変わらない**。
group scope が open 中は内部的に追加の group snapshot を取るが、これは
`prepareForEdit` の戻り値にも `st.before` の内容にも影響しない。

##### `before` map / `diff()` 契約の維持（ロック対象）

- `st.before` の各エントリは「そのアカウントを *transaction 内で* 最初に touch
  した時点」の値を保持する。`rollbackGroup` は `st.before` を **読みも書きもしない**。
  group rollback が起きても `before` の値（nil = 新規アカウントの signal を含む）は
  不変。
- `diff()` の `after` は `st.state` を clone して構築する。Step 5 は drop 対象の
  全 group について `rollbackGroup` を **`diff()` 呼び出しより前** に実行する
  規約なので、`after` は commit された（生存）group の mutation のみを反映する。
- あるアカウントを drop された group だけが touch していた場合、そのアカウントは
  `before`/`after` の key 集合から **除去されない**。`rollbackGroup` は
  `st.state` の map から key を削除せず、値を snapshot へ書き戻すだけ。
  `diff()` の既存の defensive 分岐（`st.state[acct] == nil` → 空 Inventory）が
  この境界を引き続きカバーする。

##### Step 3 / 4 / 5 がこの API をどう呼ぶか（cross-step coupling の固定点）

- **Step 3（Pass 1）**: 各明示 posting について
  `tok := trace.enterGroup()` → `inv := trace.prepareForEdit(p.Account)` →
  `bookOne(inv, …)` → 成否に応じて group key（成功時 `PostingWeight` の
  Currency、失敗時 `p.Amount.Currency` フォールバック）を決定 →
  `trace.commitGroup(tok, key)`。`addMultiLotReduction` の child posting は
  inventory を追加で mutate しないので追加の enter/commit は不要。
- **Step 4（Pass 2）**: auto-posting / deferred unknown について
  `tok := trace.enterGroup()` → `prepareForEdit` → `bookOne` →
  `commitGroup(tok, residual.Currency)`。residual currency が drop 済み group の
  key と一致する場合（D6）、その key への commit は同じ bucket にマージされ、
  Step 5 の `rollbackGroup(key)` で auto-posting の mutation も巻き戻る。
- **Step 5（finalize）**: `postingResolution` 上で drop マークされた各 key に
  ついて `trace.rollbackGroup(key)` を呼ぶ。これは `trace.diff()` を呼ぶ **前** に
  完了させる。mark 時即時か finalize 一括かは Step 5 の実装裁量。

##### 新規ファイルを増やさない

3 メソッド・`groupToken` 型・snapshot フィールドはすべて `reducer.go` 内に追加する。
Bazel/Gazelle の再生成は不要。

#### Suggested Internals

以下は generator が実装中に変更してよい助言。

##### snapshot のデータ構造（案 S1 推奨）

pending スロット 1 つ + key 配下 bucket:

```go
type stateTrace struct {
    state  map[ast.Account]*Inventory
    before map[ast.Account]*Inventory

    // pending holds the open scope's snapshots. nil when no group is
    // open. enterGroup allocates it, commitGroup files it into groups
    // and clears it back to nil.
    pending map[ast.Account]*Inventory
    // groups maps weight-currency key -> per-account restore snapshot,
    // accumulated across all commit cycles of that group.
    groups map[string]map[ast.Account]*Inventory
}
```

group がネストしないので `pending != nil` で「open scope は高々 1」を表現でき、
stack も map も要らない。代替案 S2（`enterGroup` が `*groupSnapshot` を確保）は
ネスト無しが確定方針のため不要な一般化。

##### lazy snapshot のタイミングと緊張1の解消

`prepareForEdit` 内、`st.before` の lazy snapshot ロジックの直後に
「`pending != nil` かつ `pending` に acct が未登録なら、この group がこの acct を
触る直前の state を `pending[acct]` に記録」を足す。snapshot を取る時点では key を
一切要求せず pending に匿名で溜め、key は `commitGroup` で初めて bind する。

##### 緊張2（1 group 複数 posting）の解消

`commitGroup` の **first-touch-wins マージ**で解決。posting A（key=USD）の commit が
`groups["USD"][acct]` を埋め、後続 posting B（同 key・同 acct）の commit 時は既存を
優先し B の pending snapshot を破棄する。`rollbackGroup("USD")` が `groups["USD"]`
全体を restore することで「A 成功 → B 失敗 → A も巻き戻す」が自然に成立。

##### snapshot のクローン回数最適化（性能要件）

`prepareForEdit` がそのアカウントの `st.before` エントリを **今まさに生成した**
（この group がそのアカウントの transaction 内初回 toucher）場合、`pending[acct]` は
`st.before[acct]` を **alias** してよい（clone しない）。`st.before` は read-only な
値であり `rollbackGroup` もそこから読むだけなので安全。これで single-group txn の
追加 clone は 0 になる。実装するかは generator 判断。

##### 緊張3（group をまたぐ同一アカウント mutation）の扱い — 既知の限界

`rollbackGroup` は「dropped group が触ったアカウントを、その group の snapshot へ
full restore」する。これは **「currency group の inventory 効果は互いに独立」** と
いう不変条件に依拠する。

- **独立が成り立つ通常ケース**: cost-bearing な reduction は同一 cost currency の
  lot しか match せず、augmentation は cost currency の lot を追加する。異なる
  weight group は通常、異なる commodity または異なる cost currency の Position slot
  を占めるため、ある group の rollback が別 group の Position を壊さない。
- **既知の反例**: cost も price も持たない plain cash posting（weight = amount
  currency）と、price 注釈付きで同じ commodity を扱う posting（weight = price
  currency）は、同一 cash Position row（commodity 一致・Cost nil）を共有しうる。
  例 `+100 USD @ 0.9 EUR`（EUR group）と `-100 USD`（USD group）。
- **反例を許容する根拠**: rollback は group が drop されたとき＝その group の
  posting が `bookOne` で失敗したときのみ発生する。上記反例の group は cash の
  augment/reduce のみで構成され、`Inventory.Add` は内部算術エラー以外を返さず、
  cash の `Reduce` は overdraft をエラーにしない。したがってこの種の group が
  `bookOne` で失敗し rollback 対象になることは実務上ほぼ起きない。さらに上流
  beancount は inventory を「生存 posting から再構築」する方式で incremental
  rollback を持たないため、本設計は incremental 方式の近似として既知の境界を
  1 つ持つに留まる。
- generator は、もしこの反例が現実のテストで問題化するなら「drop された group の
  アカウントを `st.before` から再構築 + 生存 posting を replay」する案（R3）へ
  切り替えてよい。

##### 単体テスト（`state_trace_test.go` に追加）

- `enterGroup` → `prepareForEdit` で mutate → `rollbackGroup(key)` で `st.state` が
  enter 前へ戻る。
- `enterGroup` → mutate → `commitGroup` → `rollbackGroup` しない → mutation が残る。
- 独立 2 group: group A が acct X を、group B が acct Y を touch →
  `rollbackGroup(A)` が Y を壊さない。
- 同一 group の 2 posting（2 回 enter/commit、同 key・同 acct）→
  `rollbackGroup(key)` で 1 つ目の posting の前まで戻る（first-touch-wins の検証）。
- `rollbackGroup` を未知 key で no-op、同 key 2 回で 2 回目 no-op。
- group scope 外の `prepareForEdit` が従来どおり動く。

#### Alternatives discussed

- **API 形状**: `enterGroup()` 引数なし + commit 時 key（採用）vs
  `enterGroup(provisionalKey)` + commit で re-key（却下: cost-bearing posting は
  provisional≠final で余計な状態遷移）vs token 無し単一フィールド（許容範囲だが
  誤用検出が弱い）。
- **rollback 正しさモデル**: R1 独立性不変条件 + per-(group,account) snapshot
  restore（採用）vs R2 deferred commit（却下: `bookOne` の in-place mutate
  シグネチャと衝突、C3 として却下済み）vs R3 drop 時 rebuild（却下: Step 3-5 の
  incremental rollback 前提と非整合、Step 1 の責務超過。ただし R1 が問題化時の
  退避先）。
- **snapshot 保持ポリシー**: commit でも保持（採用、緊張2の解）vs commit で破棄
  （却下: 先行成功 posting を巻き戻せない）。

#### Recommendation

採用: **A1（`enterGroup()` 引数なし + opaque token + `commitGroup(tok,key)`）＋
R1（独立性不変条件に依拠した per-(group,account) snapshot restore）＋ snapshot 保持
ポリシー＝保持 ＋ データ構造 S1（pending スロット + key 配下 bucket）**。

計画との差分（明示）: 計画 Step 1 スケッチの「`enterGroup(id)`」「commit で破棄」は
それぞれ A1・保持ポリシーに置き換える。いずれも計画が後段で挙げた緊張 1・2 を解く
ために必要な訂正であり、確定方針 C2/D4 や Step 3-5 の incremental rollback 構造とは
矛盾しない。R1 は通常ケースで正しく、唯一の反例（cash commodity を price-group と
plain-group が共有）は当該 group が `bookOne` で失敗しない＝rollback されないため
実務上問題化しない。

### Step 2: `postingResolution` を group-aware に再構築

- **機能要件**:
  - 内部に `groups map[string]*currencyGroupBucket` を追加（案 A1）。
  - 各 bucket は `postingsIdx []int` / `bookedIdx []int` / `unknownIdx []int` / `dropped bool` を保持。
  - `postings` / `booked` の flat slice はそのまま維持（既存の Source ポインタ bind ロジックを最小変更で再利用）。
  - `add*` メソッド群は group ID（weight currency 文字列）を引数に取り、対応 bucket の index list を更新。
  - `addPreserved` は新シグネチャで「group を drop マーク + posting を group-tracked preserved として記録」する（finalize で除外）。
- **影響モジュール**:
  - `/home/user/go-beancount/pkg/inventory/reducer.go`
    - `postingResolution` struct（200-216）
    - `newPostingResolution`（226-232）
    - `addUnknown` / `addPreserved` / `addAlreadyBooked` / `addLotAugmentation` / `addCashAugmentation` / `addSingleLotReduction` / `addMultiLotReduction`（237-354）
- **検証**:
  - 既存テスト全件再合格（`TestReducerWalk_DoesNotMutateInput` (`reducer_test.go:1489`) と `TestReducerRun_OutputIsFixedPoint` が入力順保持と Source ポインタ整合性の回帰検知器）
- **品質要件**:
  - Group が 1 つだけの txn では `txn.Postings` の slice 内容と順序が変わらない

### Detailed Design

#### Contract

##### Scope boundary of Step 2（緊張A / 緊張B の決着）

Step 2 は **behavior-preserving** なリファクタである。この step 完了後:

- どの currency group も drop されない。`finalize` は今日とまったく同じ `txn.Postings` と
  `[]BookedPosting` を同じ順序・同じ `Source`/unknown ポインタ束縛で返す。
- 既存の `pkg/inventory` テスト全件 — 特に `TestReducerWalk_DoesNotMutateInput`、
  `TestReducerRun_OutputIsFixedPoint`、`TestReducerRun_ClonesErrorsSlice` — が
  **無改変で合格**しなければならない。
- `visitTxn` Pass 1 は **provisional group key を posting ごとに計算して `add*` に渡す**
  目的でのみ変更する。Pass 1 は `trace.enterGroup` / `commitGroup` / `rollbackGroup` を
  **呼ばない**（その配線は Step 3 の責務）。Pass 1 はどの group も drop マークしない。
- `finalize` は drop を適用しない（`dropped` bucket を skip しない）。drop 適用と
  `txn.Postings` の rebuild は Step 5 の責務。Step 2 の `finalize` は Step 5 が拡張できる
  *構造的素地*を得るだけで、observable な挙動は今日と同一。

##### `postingResolution` の新フィールド

既存フィールド（`postings`, `booked`, `bookedAt`, `unknownAt`）を保持しつつ追加:

```go
// groups maps a weight-currency key to the bucket of postings,
// BookedPostings, and unknowns that belong to that currency group.
// The flat postings/booked slices remain the source of truth for
// ordering and Source-pointer binding; a bucket holds *indices* into
// them. Step 2 populates this map but never acts on a bucket's
// dropped flag; Step 5 consumes it.
groups map[string]*currencyGroupBucket
```

flat な `postings []ast.Posting` と `booked []BookedPosting` は順序の権威として残る
（D5: 入力順保持）。bucket は flat slice への *index* を持ち、コピーは持たない。

##### `currencyGroupBucket` 型 — externally observable surface

`reducer.go` に宣言。以下のメンバは Step 3/4/5 が依存するため **ロック対象**:

- group 単位の **`dropped bool`**（または同等の boolean state）。Step 3/4 が drop API 経由で
  set し、Step 5 が読む。
- group ごとに、どの flat-slice index が属するかを記録し、Step 5 が生存 group から
  `txn.Postings` と `BookedPosting` 集合を rebuild できるよう partition する。正確な
  フィールド名と partition（`postingsIdx` / `bookedIdx` / `unknownIdx`）は **Suggested
  Internals** であってロックしない（Step 5 の Phase 4 が rebuild 戦略を所有するため
  ここで過度に制約しない）。

ロックされるのは: `groups` map があれば、Step 5 は `pr.postings` の各エントリと
`pr.booked` の各エントリについて、どの group key に属し、その group が drop 済みか
判定できること。

##### `add*` メソッドのシグネチャ（ロック対象）

今日 `pr.postings` に append する全 routing メソッドは、末尾に `group string` 引数を
得る（call site の diff を最小化するため末尾）。戻り値型は **不変**（全て void のまま）。

```go
func (pr *postingResolution) addUnknown(p *ast.Posting)                                  // group 引数なし — unknown は group 未確定
func (pr *postingResolution) addPreserved(p *ast.Posting, group string)
func (pr *postingResolution) addAlreadyBooked(p *ast.Posting, lot *Lot, step *ReductionStep, group string)
func (pr *postingResolution) addLotAugmentation(p *ast.Posting, lot *Lot, group string)
func (pr *postingResolution) addCashAugmentation(p *ast.Posting, group string)
func (pr *postingResolution) addSingleLotReduction(p *ast.Posting, step ReductionStep, group string)
func (pr *postingResolution) addMultiLotReduction(p *ast.Posting, steps []ReductionStep)  // group 引数なし — child ごとに step.Lot.Currency へ振り分け
```

`addUnknown` は `group` 引数を **取らない**。`addUnknown` が呼ばれる時点（Pass 1）で
unknown の weight currency は未確定（D3: Pass 2 が `residual.Currency` で決定）。

`addMultiLotReduction` も `group` 引数を **取らない**（Step 2 レビューで判明した訂正）。
multi-lot reduction は **異なる cost currency の lot にまたがりうる**: 空 `{}` cost spec の
reduction は `IsEmpty()` な matcher を生み、`Inventory.Reduce` は commodity のみで candidate を
フィルタするため、例えば `-20 AAPL {}` は `AAPL{USD}` と `AAPL{EUR}` の両方を total match で
消費し、cost currency の異なる multi-step 結果を返しうる。各 child は `child.Cost =
step.Lot.Clone()` を持つので weight currency は `step.Lot.Currency`（child ごとに異なりうる）。
したがって `addMultiLotReduction` は child ごとに `step.Lot.Currency` をキーに bucket へ
振り分ける（`Inventory.Reduce` は multi-step 結果に cash sentinel を含めないので
`step.Lot.Currency` は常に非空）。

**Step 3 への申し送り（cross-step note）**: 1 つの posting の `bookOne` が複数 currency group
にまたがって inventory を mutate しうる（上記 multi-currency multi-lot reduction）。Step 3 の
Phase 4 detailed design は、`enterGroup`/`commitGroup`（Step 1 の Contract は「1 scope を
単一 key で commit」）を、こうした「1 posting・複数 group」のケースでどう使うか決めること。
Step 1 の `stateTrace` API 自体の変更が必要かもここで判断する。

##### `addPreserved` の新責務（ロック対象、observable）

`addPreserved(p, group)` は:

1. `*p` を `pr.postings` に **今日と同じく** append する（Step 2 が behavior-preserving で
   あるため — posting は依然 emit され、入力順も保たれる）。
2. 加えて、append された posting の index を `groups[group]` に登録し、その bucket を
   `dropped` マークする。

Step 2 時点の observable: `finalize` が `dropped` をまだ参照しないため、preserved posting
は依然 `txn.Postings` に現れ、`BookedPosting` を生まない — 今日と同一。`dropped` マークは
*Step 5 のために記録される*だけで、それまで効果はない。

##### `finalize` — Step 2 完了時点の externally observable な挙動（ロック対象）

`finalize` は現在のシグネチャ
`func (pr *postingResolution) finalize() (booked []BookedPosting, unknowns []*ast.Posting)`
と現在の observable contract を保持:

- `pr.booked` を、各エントリの `Source` を記録済み booked offset の `&pr.postings[off]` に
  束縛して返す。
- unknown ポインタの fresh な `[]*ast.Posting` を返す。各々 unknown の `pr.postings` 内の
  スロットを指す。
- 全 `add*` 呼び出し後に走る。`dropped` に基づいて posting を skip / reorder / 省略
  しない。drop が無いので返り値 `booked`/`unknowns` と最終 `pr.postings` は Step 2 前と
  同一。

内部的に offset を bucket から再導出するか、`bookedAt`/`unknownAt` をそのまま使うかは
Suggested Internals。*contract* は上記の戻り値とポインタ束縛のみ。

##### Unknown handling — Step 4 が依存する部分（緊張D、ロック対象）

unknown（auto-posting, deferred-augment）は `addUnknown` が **group key なし**で記録する。
weight currency が Pass 2 まで不明なため。構造は、Step 4 が `solveResidual` で
`residual.Currency` を得た後に、各 unknown をその currency key の group bucket に
*attach* できるようにすること（bucket が無ければ作成、生存/drop 済み bucket への join は
D6 に従う）。

ロック要件: Step 2 完了時点で unknown は `finalize` から今日どおり取得可能（`pr.postings`
への offset）であり、まだどの `groups` bucket のメンバでもない。Step 4 が join を所有する。
unknown offset を既存 `unknownAt` slice に置くか、予約 bucket に置くか、専用フィールドに
置くかは **Suggested Internals**。

##### `visitTxn` Pass 1 の変更（ロックされたスコープ）

Pass 1（415-463 行）は **以下のみ**変更:

- 各明示 posting について、`bookOne` 返却後に provisional group key 文字列を計算し、選択
  された `add*` メソッドに渡す。key は posting の weight currency: 成功時は
  `PostingWeight(p)` の `Currency`、`PostingWeight` エラー時は `p.Amount.Currency` に
  フォールバック（D3; このブランチでは `p.Amount != nil` が構造的に保証される）。
  `addPreserved`（`len(errs) > 0` ブランチ）の key も同様に `p` から計算。
- `addUnknown` の呼び出し箇所（auto-posting ブランチと `CodeAugmentationRequiresCost`
  defer ブランチ）は **不変** — key 引数なし。
- `enterGroup`/`commitGroup`/`rollbackGroup` 呼び出しは追加しない。`markForDrop` 呼び出しも
  追加しない。失敗時の `r.errs = append(...)` は不変。

weight-currency のタイミング注記: cost-bearing posting の weight currency は `add*` が
`*ast.Cost` を install した後でないと `p` から読めない。ただし Pass 1 は booking outcome
（`lot`/`steps`）を既に手元に持つので、key は `p` + booking outcome から install 前に
計算できる（augmentation は `lot.Currency`、reduction は `steps[0].Lot.Currency`、cash/plain
は `p.Amount.Currency`、price は `p.Price.Amount.Currency`）。正確な呼び出し順序はロック
しない。key が「posting の最終 weight currency（`p.Amount.Currency` フォールバック付き）」
に等しいことだけがロック。

##### 既存テスト（緊張E、ロック対象）

`bazel test //pkg/inventory/...` がテストファイル無改変で完全 green であること。
fixed-point テスト、does-not-mutate-input テスト、errors-slice-clone テストがこの保証の
回帰検知器。

##### 新規ファイルなし

全追加（`currencyGroupBucket` 型、`groups` フィールド、シグネチャ変更）は `reducer.go`
内。Step 2 では Gazelle 再生成不要。

#### Suggested Internals

以下は全て助言。generator は実装中の発見に応じて採用・改変・置換してよい。

##### `currencyGroupBucket` フィールド構成（案 I1 推奨）

```go
type currencyGroupBucket struct {
    postingsIdx []int  // indices into pr.postings owned by this group
    bookedIdx   []int  // indices into pr.booked owned by this group
    unknownIdx  []int  // indices into pr.postings for unknowns joined to this group (Step 4 で埋まる)
    dropped     bool
}
```

`postingsIdx` と `bookedIdx` を分けるのは、全 posting が `BookedPosting` を生むとは限らない
ため（preserved posting は `postingsIdx` エントリを持つが `bookedIdx` エントリを持たない；
multi-lot 親は両方を複数生む）。これは既存 `bookedAt` 機構の per-group partition。

##### 既存 `bookedAt` / `unknownAt` との関係（案 I-A 推奨）

`bookedAt` と `unknownAt` を現状維持し、`finalize` のポインタ束縛を今日どおり駆動する。
新しい `groups` bucket は `add*` メソッドが *並行して* populate する。Step 5 が後で bucket と
`bookedAt` をクロス参照する。これで Step 2 の `finalize` は文字どおり不変になり、
behavior-preserving が自明に検証できる。（案 I-B「bucket を single source of truth に」は
クリーンだが `finalize` の iteration 順序の決定性確保が必要で Step 2 にはリスク。Step 5 で
望むなら採用。）

##### Unknown storage（案 U1 推奨）

既存 `unknownAt []int` フィールドを維持。`addUnknown` は今日どおり append。bucket には
空の `unknownIdx` を出荷し、Step 4 が `residual.Currency` 算出後に
`groups[residual.Currency].unknownIdx` へ unknown index を file する（その helper は Step 4 で
追加、Step 2 ではない）。（案 U2「予約 `groups[""]` bucket」は "ungrouped" と実 key の混同を
招くため却下。）

##### `add*` 内部実装

各 `add*` は既存 append ロジックの後に
`pr.bucketFor(group).postingsIdx = append(..., idx)`、`pr.booked` にも append する箇所では
`...bookedIdx = append(..., len(pr.booked)-1)` を行う。小さな private helper:

```go
func (pr *postingResolution) bucketFor(group string) *currencyGroupBucket {
    b := pr.groups[group]
    if b == nil {
        b = &currencyGroupBucket{}
        if pr.groups == nil {
            pr.groups = map[string]*currencyGroupBucket{}
        }
        pr.groups[group] = b
    }
    return b
}
```

`addPreserved` は index 登録後に `b.dropped = true` も set する。

##### Multi-lot expansion の child 登録（緊張C）

`addMultiLotReduction` は N 個の child を `pr.postings` に、N 個のエントリを `pr.booked` に
append する。各 child は **その step の cost currency** の group に属する（child ごとに
異なりうる — 上記 Contract の `addMultiLotReduction` 注記参照）。既存の
`for _, step := range steps` ループ内で、各 child append 後にその `pr.postings` index を
`bucketFor(step.Lot.Currency).postingsIdx` に、`pr.booked` index を同 bucket の `bookedIdx` に
push する。`bookedAt` は従来どおり 1 親 posting に対し N offset を記録する（変更なし）。

##### Pass 1 の provisional group-key 計算（案 K1 推奨）

Pass 1 で key を計算し `add*` に渡す。cost-bearing outcome の場合 key は `lot.Currency`
（augmentation）/ `steps[0].Lot.Currency`（reduction）— どちらも Pass 1 で利用可能。
cash/plain は `p.Amount.Currency`、price は `p.Price.Amount.Currency`、already-`*ast.Cost`
は `PostingWeight(p)` が直接動く。（案 K2「各 `add*` が内部で key 計算」は Step 3 が Pass 1
で key を見る必要があるため却下。）

##### `newPostingResolution` の pre-sizing

`groups` は nil map のままにし `bucketFor` が lazy 生成。bucket の pre-size は不要。

#### Alternatives discussed

- **緊張A（Step 2 が Pass 1 をどこまで触るか）**: A1（Pass 1 で実 provisional key を算出して
  `add*` に渡す。stateTrace API 呼び出しと drop マークなし）を採用。A2（`""` sentinel group
  を渡し Step 3 で実 key を再配線）は却下 — grouping 作業を Step 3 に二重化し Step 3 の diff を
  near-rewrite にする。A3（Step 2 で `enterGroup`/`commitGroup` も配線）は却下 — Step 境界を
  侵犯し、rollback の無い snapshot 機構は Step 2 では純オーバーヘッド。
- **緊張B（`finalize` の責務分担）**: B1（Step 2 の `finalize` は observable に不変、bucket
  構造を記録するが `dropped` を参照しない）を採用。B2（Step 2 の `finalize` が `dropped`
  bucket を既に skip）は却下 — Step 5 の Phase 4 が設計すべき rebuild 戦略を先取りし、
  ゼロテストの未検証パスを追加する。
- **`add*` シグネチャ**: 末尾 positional `string` 引数を採用。「引数なし、各 `add*` が内部で
  key 計算」は却下（Step 3 が Pass 1 で key を見る必要）。「`groupKey` struct でラップ」は
  speculative generality として却下（D3 で key は plain currency string）。
- **Unknown storage**: U1（`unknownAt` 維持 + bucket に空 `unknownIdx`）を採用。U2（予約
  `groups[""]` bucket）は却下。

#### Recommendation

**A1 + B1 + 末尾 positional `string` 引数 + U1** を採用。

- **A1 over A2**: A2 は Step 2 の diff を最小化するように見えるが、grouping 作業を Step 3 に
  *再配置*しつつ call-site 編集を*二重化*するだけ。A1 は正しい provisional key を一度計算し、
  Step 3 の diff は純粋に additive（`enterGroup`/`commitGroup`/`markForDrop` 呼び出し）になり、
  レビューしやすく fixed-point テストを壊しにくい。
- **A1 over A3**: A3 は Step 3 に何の利益も与えず（両者とも Pass 1 で key が必要）、rollback の
  無い snapshot 機構に Step 2 の予算を費やし、計画の Step 1/Step 3 の group API 所有を侵犯する。
- **B1 over B2**: B2 は `finalize` を、計画が意図的に Step 5 の Phase 4 に委ねた drop 適用戦略に
  先行コミットさせる。B1 は `finalize` の observable contract を凍結したまま保ち、fixed-point と
  does-not-mutate-input テストが Step 2 commit を無料で検証し、Step 5 が設計余地を保つ。
- **末尾 positional `string` 引数**: D3 で group identity は sentinel なしの plain weight-currency
  文字列に固定されているのでラップするものがなく、末尾引数が Step 2/Step 3 双方の diff の
  ノイズを最小化する。
- **U1**: `unknownAt` 再利用は Step 2 を構造的に behavior-preserving に保ち、Step 4 に
  クリーンで明示的な join point（`bucket.unknownIdx`）を sentinel key を発明せずに与える。

### Step 3: Pass 1 — bookOne 結果を group へ振り分け、失敗時に mark-for-drop

- **機能要件**:
  - 各 posting に対し `enterGroup(provisional)` → `bookOne` → 成功時は `PostingWeight(&pr.postings[i])` から currency 取得し正式 group に bind、失敗時は `PostingWeight(p)` → エラーなら `p.Amount.Currency` で group を決定し、その group を `markForDrop`。
  - `CodeAugmentationRequiresCost` + cost number missing の defer 経路は **従来どおり** `addUnknown` で Pass 2 へ。
  - multi-lot expand（`addMultiLotReduction`）の child posting は親と同じ group bucket に登録。
- **影響モジュール**:
  - `/home/user/go-beancount/pkg/inventory/reducer.go`
    - `visitTxn` Pass 1 loop（415-463）
    - 新規 helper: `groupCurrencyFor(p *ast.Posting, succeeded bool) string`
- **再利用**:
  - `PostingWeight` (`/home/user/go-beancount/pkg/inventory/weight.go:43-73`) を流用。Cost 設定後に呼ぶことで cost-bearing posting も正しい weight currency を返す。
- **検証**:
  - `TestReducerWalk_FailedReductionDropsGroup`: 2 currency group の txn（USD reduction lot 不足 + JPY balanced）で USD group が drop、JPY group が残る
  - `TestReducerWalk_FailedReductionRollsBackInventory`: 同 group で先行成功した augment の lot が rollback される
  - `TestReducerWalk_FailureInOneGroupPreservesOther`: 別 group の inventory mutation が live state に commit される
- **品質要件**:
  - 失敗 posting に対する `r.errs` 追加は現状と同等（失敗ごと 1 件）。drop された他 posting への追加 diagnostic は出さない（upstream 準拠）

### Detailed Design

#### Contract

##### 緊張1 の決着: 「1 posting・複数 group」の扱い — Step 1 API は変更しない

`bookOne` が複数 currency group にまたがって inventory を mutate しうるのは `addMultiLotReduction`
経路のみ（空 `{}` cost spec の reduction が `AAPL{USD}` と `AAPL{EUR}` の lot を両方消費する等）。
これを **Step 1 の `stateTrace` API を一切変更せず**に扱う。決定:

- **1 posting は引き続き 1 つの `enterGroup` scope で開始する。** `bookOne` 実行中に open な
  scope は常に高々 1（Step 1 の非ネスト規約を維持）。
- `bookOne` 成功後、その posting が生んだ **distinct な weight currency key の集合**を確定する:
  - augmentation (`lot != nil`): `{ lot.Currency }`（単一）
  - cash augmentation (`len(steps) == 0`): `{ cashGroupKey(p) }`（単一）
  - single-lot reduction (`len(steps) == 1`): `{ reductionGroupKey(p, steps[0]) }`（単一）
  - already-booked: `{ weightCurrencyFallback(p) }`（単一）
  - multi-lot reduction (`len(steps) > 1`): `{ step.Lot.Currency for each step }` の **重複排除した集合**（1 個以上）
- **複数 key が出た場合の commit 規約（ロック対象）**: open scope を `commitGroupMulti(tok, keys)`
  で閉じ、その posting が touch したアカウントの snapshot を **各 distinct key の bucket に
  行き渡らせる**（実装手段は Suggested Internals）。単一 key の場合は従来どおり
  `commitGroup(tok, key)`。
- **Contract としてロックするのは 1 点のみ**: *multi-lot reduction が複数 currency にまたがる
  場合、drop 対象になりうる **各** distinct currency key について、`rollbackGroup(key)` を
  呼べばその key の lot 消費が `st.state` から巻き戻る状態が、Pass 1 完了時点で成立して
  いること。* この不変条件を満たす限り、配分の実装手段は Suggested Internals。
- **`stateTrace` への追加（ロック対象）**: `commitGroupMulti(tok groupToken, keys []string)` を
  `stateTrace` に追加する。これは Step 1 の既存 3 メソッド（`enterGroup`/`commitGroup`/
  `rollbackGroup`）を**変更しない**追加であり、Step 1 単体テストは無改変で合格する。
  シグネチャ: `func (st *stateTrace) commitGroupMulti(tok groupToken, keys []string)` —
  open scope を閉じ、その scope の pending snapshot を `keys` の各 key 配下に（first-touch-wins
  で）file する。`keys` が単一要素なら `commitGroup` と等価。

##### `markForDrop` のシグネチャと配置（ロック対象）

drop マークは **`postingResolution` 側**に置く。`stateTrace` は drop の概念を持たない
（rollback 対象 key は Step 5 が `postingResolution` から読んで `stateTrace.rollbackGroup` に渡す）。
`reducer.go` に追加:

```go
// markForDrop marks the currency group identified by weightCurrency as
// dropped, creating the bucket if it does not yet exist. Unlike
// addPreserved it appends no posting: it is the entry point for marking
// a group whose drop was decided by something other than a freshly
// preserved posting — e.g. Pass 2 joining an auto-posting onto an
// already-failed group (D6). Marking is idempotent.
func (pr *postingResolution) markForDrop(weightCurrency string)
```

実装は実質 `pr.bucketFor(weightCurrency).dropped = true` の 1 行。**Step 3 自身は
`markForDrop` を Pass 1 から呼ばない**（`addPreserved` がすでに `dropped = true` を立てる）。
Step 3 で導入する理由は Step 4 の依存 surface を先に固定し、Step 4 の diff を純 additive に
するため。ロックされる surface: シグネチャと「呼ぶと `pr.groups[weightCurrency].dropped ==
true` になる」「冪等」「posting を append しない」。

##### `visitTxn` Pass 1 配線後の構造（ロック対象）

Pass 1 ループの各 posting イテレーションは配線後、以下の経路を取る:

- **auto-posting (`p.Amount == nil`)**: 現状どおり `pr.addUnknown(p); continue`。
  **`enterGroup`/`commitGroup` で囲まない**（`prepareForEdit` を呼ばないので snapshot 対象なし）。
- **deferred-unknown (`CodeAugmentationRequiresCost` + `costNumberMissing`)**: `enterGroup` →
  `prepareForEdit` → `bookOne` → 失敗判定までは scope に入る。deferred と判明したら
  `pr.addUnknown(p)` してから **`commitGroup(tok, weightCurrencyFallback(p))` で scope を閉じる**
  （drop は **マークしない**）→ `continue`。根拠: `ResolveCost` 失敗時点で `inv.Add` 未到達 →
  inventory 未 mutate。`commitGroup` は scope を正しく閉じるためだけ。
- **失敗 posting (`len(errs) > 0`、上記 deferred を除く)**: `r.errs = append(...)`（現状不変）→
  `key := weightCurrencyFallback(p)` → `pr.addPreserved(p, key)`（posting append +
  `bucket.dropped = true`）→ **`commitGroup(tok, key)`** → `continue`。`key` を 1 回計算して
  `addPreserved` と `commitGroup` の **両方に同一文字列**を渡すこと。
- **already-booked multi-step invariant violation（`CodeInternalError` 経路）**: 失敗扱い。
  `r.errs` append → `pr.addPreserved(p, key)` → `commitGroup(tok, key)` → `continue`。
- **成功 posting（`switch` の各 case）**: 既存の `add*` 呼び出しはそのまま。**追加で
  `commitGroup`/`commitGroupMulti` を呼ぶ**:
  - augmentation / cash augmentation / single-lot reduction / already-booked:
    `commitGroup(tok, key)` を 1 回。`key` は対応する `add*` に渡したのと **同じ文字列**。
  - multi-lot reduction: `commitGroupMulti(tok, distinctStepCurrencies(steps))`。

**`enterGroup` の呼び出し位置（ロック対象）**: `tok := trace.enterGroup()` は
`inv := trace.prepareForEdit(p.Account)` の直前に置く。auto-posting 経路は `enterGroup` の前に
`continue` するので scope に入らない。

**ループ終了時の不変条件（ロック対象）**: Pass 1 ループを抜けた時点で **open な scope は
存在しない**（全 posting が `commitGroup`/`commitGroupMulti` で scope を閉じる、または scope に
入らない）。これは Pass 2（Step 4）が自分の `enterGroup` を安全に呼べる前提。

##### Step 3 完了時点の externally observable な挙動（ロック対象）

**Step 3 は behavior-preserving。** `markForDrop`/`addPreserved` が `bucket.dropped` を立て、
`commitGroup`/`commitGroupMulti` が snapshot を `stateTrace.groups` に file するが、`finalize` は
まだ `dropped` を参照せず、**`rollbackGroup` は Step 3 では呼ばれない**。drop された group の
mutation は Step 3 完了時点ではまだ live state に残る。`txn.Postings`、返却される
`[]BookedPosting`、`before`/`after` map、`r.errs` はすべて Step 3 前と同一。

**既存テストへの影響（ロック対象）**: `bazel test //pkg/inventory/...` がテストファイル無改変で
完全 green。`commitGroup`/`commitGroupMulti` の追加呼び出しは `stateTrace.groups` を populate
するだけで `st.state` にも `st.before` にも影響しない。

**計画 Step 3 の検証セクションとの差分（明示）**: 計画は `TestReducerWalk_FailedReductionDropsGroup`
/ `TestReducerWalk_FailedReductionRollsBackInventory` / `TestReducerWalk_FailureInOneGroupPreservesOther`
を Step 3 の検証に挙げているが、これらは **drop が実際に適用されないと green にならない** =
Step 5 完了後でないと意味を持たない。**Step 3 commit 時点の検証は下記「検証方法」に置き換える。**
計画の 3 テストは Step 5（または Step 6）で追加する。

##### Step 4 / Step 5 への cross-step coupling 固定点（ロック対象）

- **Step 4 が依存するもの**:
  - `pr.markForDrop(weightCurrency string)` — drop 済み group に join した auto-posting を drop に
    巻き込む (D6) ための entry point。
  - Pass 1 ループ終了後に **open な scope が無い**こと（Step 4 が自分の `enterGroup` を呼べる）。
  - `pr.groups[key]` が `nil` でも `pr.bucketFor(key)` / `pr.markForDrop(key)` が安全に bucket を
    lazy 生成すること。
  - `pr.groups[key].dropped` を読んで「この residual currency の group はすでに drop 済みか」を
    判定できること（D6 の生存/drop 分岐）。
- **Step 5 が依存するもの**:
  - `pr.groups` の各 `key` について `bucket.dropped` を読み、drop 済み key の集合を得られること。
  - その各 key について `trace.rollbackGroup(key)` を呼べば、Pass 1（および Step 4 の Pass 2）で
    その group が行った inventory mutation が `st.state` から巻き戻ること。multi-key posting も
    含め、drop 対象の各 key が独立に rollback 可能（緊張1 の Contract 不変条件）。
  - `bucket.postingsIdx` / `bucket.bookedIdx` で生存 group の posting/booked を partition できる
    こと（Step 2 で確定済み、Step 3 は触らない）。

##### 新規ファイルなし

`markForDrop` メソッド、Pass 1 の配線変更、`commitGroupMulti`、新規 helper
（`distinctStepCurrencies` 等）はすべて `reducer.go` 内。Gazelle 再生成不要。

#### Suggested Internals

以下は generator が実装中の発見に応じて採用・改変・置換してよい助言。

##### 緊張1 の配分: multi-key posting の snapshot を各 key に行き渡らせる（案 I-a 推奨）

`commitGroup` の first-touch-wins は「**異なる** posting が同 key を共有するとき」の正しさの
ためのものであって、「1 posting が複数 key を持つとき」には逆に働く（同一 `pending` を 2 回
commit しても 2 個目の key には入らない）。推奨実装:

`stateTrace` に `commitGroupMulti(tok groupToken, keys []string)` を 1 つ足す。最初の key には
`pending` をそのまま file（`commitGroup` と同様、ownership 移譲可）、2 個目以降の key には
各 `pending` snapshot の **clone** を first-touch-wins で file する。これは Step 1 の既存
メソッドを変えず追加するだけなので「Step 1 単体テストを壊さない」要件を満たす。`keys` が
単一要素なら `commitGroup` と同じ振る舞いになるよう実装してよい（Pass 1 から常に
`commitGroupMulti` を呼ぶ簡素化も可 — generator 判断）。

却下案: `commitGroup` を可変長 `keys ...string` に変更（コミット済み API の破壊的変更）。
multi-key posting を child ごとに別 scope（`bookOne` が atomic に 1 回実行されるため不可能）。
合成キー（D3 の plain-currency key モデルと Step 4/5 を壊す）。

実務的注記: multi-currency multi-lot reduction は空 `{}` cost spec の reduction が異なる cost
currency の lot を total-match で消費するケースに限られ稀。generator は `reducer_test.go` に
「`-20 AAPL {}` が `AAPL{USD}` と `AAPL{EUR}` を消費し、片方を rollback できる」白box テストを
1 本足して配分の正しさを pin すること。

##### 緊張2 の解消: 失敗 posting の rollback と weight currency

- **weight currency 決定**: 失敗 posting は `weightCurrencyFallback(p)` をそのまま使う（Step 2 で
  `addPreserved` に渡しているのと同一文字列）。`commitGroup(tok, key)` の `key` も **必ず同じ
  文字列**。`key` を 1 回計算して `addPreserved` と `commitGroup` の両方に渡す（変数に束ねる）。
- **失敗時の partial mutation rollback**: 現状コード調査の結論 — `bookAugment` の `ResolveCost`
  失敗 / `inv.Add` 失敗、`bookReduce` の通常エラー（`CodeNoMatchingLot` 等）はすべて mutation
  **前**に返る。`fillRealizedGain` のエラーと `Reduce` consumption ループ内 arithmetic error
  （`CodeInternalError` 系）のみ partial mutation を残しうる。**Step 3 は失敗時専用の即時
  rollback 経路を実装しない** — 失敗 posting も成功 posting と同じく `commitGroup` するだけ。
  `commitGroup` で snapshot を file しておけば mutation の有無にかかわらず Step 5 の
  `rollbackGroup` が正しく動く（mutation ゼロなら snapshot == 現 state で実質 no-op）。

##### 緊張3 の解消: enterGroup/commitGroup のライフサイクル配線（具体案）

Pass 1 ループ本体の推奨構造（擬似コード、generator は再構成可）:

```go
for i := range txn.Postings {
    p := &txn.Postings[i]
    if p.Amount == nil {
        pr.addUnknown(p)
        continue            // scope に入らない
    }
    tok := trace.enterGroup()
    inv := trace.prepareForEdit(p.Account)
    method := r.booking[p.Account]
    lot, steps, errs := bookOne(inv, p, method, txn.Date)

    if len(errs) == 1 && errs[0].Code == CodeAugmentationRequiresCost && costNumberMissing(p.Cost) {
        pr.addUnknown(p)
        trace.commitGroup(tok, weightCurrencyFallback(p)) // scope を閉じるだけ、drop しない
        continue
    }
    if len(errs) > 0 {
        r.errs = append(r.errs, errs...)
        key := weightCurrencyFallback(p)
        pr.addPreserved(p, key)            // bucket.dropped = true
        trace.commitGroup(tok, key)        // 同じ key で file
        continue
    }
    switch {
    case p.Cost != nil && p.Cost.IsBooked():
        key := weightCurrencyFallback(p)
        if len(steps) > 1 {
            r.errs = append(r.errs, Error{... CodeInternalError ...})
            pr.addPreserved(p, key)
            trace.commitGroup(tok, key)
            continue
        }
        pr.addAlreadyBooked(p, lot, firstStepOrNil(steps), key)
        trace.commitGroup(tok, key)
    case lot != nil:
        pr.addLotAugmentation(p, lot, lot.Currency)
        trace.commitGroup(tok, lot.Currency)
    case len(steps) == 0:
        key := cashGroupKey(p)
        pr.addCashAugmentation(p, key)
        trace.commitGroup(tok, key)
    case len(steps) == 1:
        key := reductionGroupKey(p, steps[0])
        pr.addSingleLotReduction(p, steps[0], key)
        trace.commitGroup(tok, key)
    default: // multi-lot reduction
        pr.addMultiLotReduction(p, steps)
        trace.commitGroupMulti(tok, distinctStepCurrencies(steps))
    }
}
```

- 各経路で `commitGroup`/`commitGroupMulti` が **必ず 1 回**呼ばれる（panic 回避: Step 1 は
  二重 commit / commit-without-enter を panic 可とする）。
- `key` を変数に束ねて `add*` と `commitGroup` で **同一文字列**を使うのが緊張2 の要。
- 小 helper `distinctStepCurrencies(steps []ReductionStep) []string`（`reducer.go` に追加、
  入力順保持で重複排除）を足すと multi-lot 経路が読みやすい。

##### helper の再利用

- `weightCurrencyFallback` / `cashGroupKey` / `reductionGroupKey`（Step 2 で導入済み）を
  そのまま再利用。**計画 Step 3 が挙げた新規 helper `groupCurrencyFor(p, succeeded)` は新設
  しない** — Step 2 の経路別 key helper と二重実装になる。
- `PostingWeight` は `weightCurrencyFallback` 経由で間接利用。Pass 1 から直接呼ばない。

#### Alternatives discussed

- **緊張1（1 posting・複数 group の commit）**: A1（posting は 1 scope で開始、multi-key 時は
  `commitGroupMulti` で per-key clone-file）を採用。A2（`commitGroup` を可変長引数に変更）は
  却下 — コミット済み API の破壊的変更で利得なし。A3（multi-currency reduction を Pass 1 で
  検出し currency 部分を別 posting 群に完全分離）は却下 — `bookOne`/`Inventory.Reduce` の
  atomic 実行モデルと両立せず `bookOne` 内部改修は Scope 外。A4（multi-key posting を合成
  キーの「代表 group」として posting 単位 drop 判定）は却下 — Step 4 の `residual.Currency`
  lookup と Step 5 の partition を壊し D3 と非整合。
- **`markForDrop` の配置**: B1（`postingResolution` に配置）を採用。B2（`stateTrace` に
  drop フラグ）は却下 — `stateTrace` が posting/booked slice を知らず drop 除外責務を果たせ
  ない。B3（両方に持たせる）は却下 — 状態の二重管理。
- **失敗時 rollback のタイミング**: C1（失敗 posting も成功 posting と同じく `commitGroup`
  のみ、実 rollback は Step 5 が key 単位で）を採用。C2（失敗判明直後に即時 rollback）は
  却下 — Step 1 は token 直接 rollback を提供せず、同一 group に成功+失敗 posting が混在する
  場合 group atomic な巻き戻しは「全 posting 処理後に key 単位で 1 回」が正しい意味論。

#### Recommendation

採用: **A1（posting は 1 scope で開始、multi-currency multi-lot reduction のみ
`commitGroupMulti` で per-key clone-file）＋ B1（`markForDrop` は `postingResolution`）＋
C1（失敗 posting も `commitGroup` のみ、実 rollback は Step 5 に集約）。**

- **A1**: 緊張1 の本質は「`bookOne` は atomic に複数 group を mutate しうるが、group identity
  （drop 単位・rollback 単位）は currency ごと」という非対称性。A1 は `stateTrace` に
  **追加のみ**（既存メソッド不変、Step 1 単体テスト無改変）で、Step 4 の plain-currency lookup
  と Step 5 の per-key rollback の両方を壊さない唯一の案。
- **B1**: drop は posting/booked slice の最終形に関する決定であり、その slice を所有する
  `postingResolution` が持つのが自然。`stateTrace` は inventory checkpoint 専任のまま。
- **C1**: Step 1 Contract が意図的に「rollback は key 単位、token 直接 rollback は無し」と
  設計しており、失敗時即時 rollback はこのモデルに逆らう。group atomic な巻き戻しは「全
  posting 処理後に key 単位で 1 回」が正しい意味論。

**計画 Step 3 との差分（明示）**:
1. 新規 helper `groupCurrencyFor(p, succeeded bool)` は **新設しない**（Step 2 の経路別 key
   helper と二重実装になる）。代わりに `distinctStepCurrencies(steps)` を multi-lot 経路用に足す。
2. 計画 Step 3 の検証テスト（`TestReducerWalk_FailedReductionDropsGroup` 等）は drop 適用後
   （Step 5）でないと green にならない。Step 3 の検証は下記に置き換える。
3. `stateTrace` に `commitGroupMulti` を追加（Step 1 Contract の拡張ではなく補完、既存
   メソッドは不変）。

**Step 3 完了時点の検証方法（計画の検証セクションを置き換え）**:
- 既存 `bazel test //pkg/inventory/...` 全件がテスト無改変で green（behavior-preserving の回帰検知）。
- 新規白box テスト（`reducer_test.go`）: 失敗 reduction を含む 2-currency-group txn を
  `visitTxn` に通し、`pr.groups[failedKey].dropped == true` かつ
  `pr.groups[survivingKey].dropped == false` を assert。`txn.Postings` と `booked` は drop 未適用
  なので従来どおりであることも併せて assert。
- 新規白box テスト: multi-currency multi-lot reduction（`-20 AAPL {}` が `AAPL{USD}` と
  `AAPL{EUR}` を消費）を通し、`trace.rollbackGroup("USD")` で USD lot のみが巻き戻り EUR が
  残ること（緊張1 の配分の正しさを pin）。
- `state_trace_test.go` に `commitGroupMulti` の単体テスト（複数 key に同 snapshot が file され、
  各 key を独立に rollback できる）。

### Step 4: Pass 2 — unknown を group へ join、失敗 group との接続

- **機能要件**:
  - `solveResidual` 成功後、`residual.Currency` で既存 group を lookup:
    - 生存 → unknown を join し `bookOne`。さらに失敗したらその group を `markForDrop`
    - drop 済み → unknown も同 group に join して drop マーク（D6）
    - 未存在 → 新 group を作成して単独で扱う
  - `flagAmbiguousUnknowns` / `solveResidual` 自体の挙動は不変。
- **影響モジュール**:
  - `/home/user/go-beancount/pkg/inventory/reducer.go`
    - `visitTxn` Pass 2（469-532）
- **検証**:
  - `TestReducerWalk_AutoPostingJoinsFailedGroup_Drops`: USD reduction 失敗 + USD residual の auto-posting が drop
  - `TestReducerWalk_AutoPostingJoinsSurvivingGroup`: USD reduction 失敗 + JPY balanced cash + JPY auto-posting → auto-posting は JPY group で生存
- **品質要件**:
  - `TestReducerWalk_AutoPostingResidualUsesBookedReductions` (`reducer_test.go:1362`) と `TestReducerWalk_AutoPostingZeroResidual` (`reducer_test.go:282`) は **変更なしで合格** すること

### Step 5: `finalize` で drop 適用、`Source` ポインタの整合性確保

- **機能要件**（Contract レベル — 「何を満たすか」のみ。「どう実現するか」は下記の通り Step 5 着手時に設計）:
  1. drop されない group の posting だけを集約して新 `txn.Postings` を入力順で構築
  2. drop されない group の `BookedPosting` だけを返却
  3. drop group の inventory mutation を `stateTrace.rollbackGroup` でロールバック（mark 時に即時 rollback でも、finalize で一括でも可。実装は generator 判断）
  4. 返却される全 `BookedPosting.Source` が、最終的に publish される `txn.Postings` slice 内の対応 posting を正しく指していること（drop により slice 長が変わっても整合）
- **設計未確定事項（Step 5 着手時に planner で detailed design する）**:
  - 上記 4 の「Source ポインタ整合性」をどう実現するか。候補は複数あり、高レベル計画では固定しない:
    - drop を考慮した上で `finalize` の bind ループを 1 回だけ回す（現行 `finalize` の bind ロジックの自然な拡張）
    - bucket が保持する index を、生存 group のみで再採番してから bind
    - その他、generator が実装中に発見するより単純な方法
  - いずれにせよ「旧 index → 新 index の対応 map を別途構築する」ことを前提化しない。過剰な内部状態になり得るため、Step 5 の Phase 4 で代替案を比較してから決める。
- **影響モジュール**:
  - `/home/user/go-beancount/pkg/inventory/reducer.go`
    - `postingResolution.finalize`（361-371）
    - `visitTxn` の `txn.Postings = pr.postings`（465）周辺の順序調整
- **検証**:
  - `TestReducerWalk_DroppedGroupOmittedFromTxnPostings`: drop された posting が物理的に `txn.Postings` から消える、surviving Source ポインタが新 slice の正しいアドレスを指す
  - `TestReducerWalk_AllPostingsDroppedEmitsEmptyTxn` (D2 検証): 全 posting drop で `txn.Postings == nil/[]` の transaction が emit される
- **品質要件**:
  - `TestReducerRun_OutputIsFixedPoint` (fixed-point 性) で Source ポインタ整合性を回帰検知

### Step 6: 既存テスト再合格 + 新規回帰テスト追加

- **機能要件**:
  - Step 1-5 の各検証テストを `reducer_test.go` に追加
  - 既存テストのうち、失敗系で `txn.Postings` の長さ / 順序を assert しているものがあれば新 semantics に合わせて更新（grep 上は確認できなかったが念のため目視確認）
- **影響モジュール**:
  - `/home/user/go-beancount/pkg/inventory/reducer_test.go`
- **検証**:
  - `bazel test //pkg/inventory/...` 全件 green
  - `bazel test //...` 全件 green（下流の validation 等で Source ポインタ整合性が壊れていないかを担保）
- **品質要件**:
  - 失敗を含まない大多数の transaction では出力が一切変わらないこと（fixed-point + does-not-mutate-input で担保）

### Step 7: Bazel/Gazelle 再生成と最終ビルド

- **機能要件**:
  - 新規 `.go` ファイルを追加した場合は `bazel run //:gazelle` を実行
  - 既存ファイル編集のみなら省略可だが、無害なので念のため実行
  - `bazel build //...` と `bazel test //...` を実行して全 green を確認
- **影響モジュール**:
  - BUILD.bazel（必要に応じて自動更新）
- **検証**:
  - 上記コマンドの exit 0

## 採用しなかった代替案（要約）

- **データ構造**: per-posting group ID 配列 (A2) と別 struct currencyGroup (A3) を検討したが、それぞれ「2 hop lookup」「入力順保持との衝突」で却下。
- **Group 確定タイミング**: 「Pass 1 完了後に reshuffle」(B2) を検討したが、reshuffle の状態管理コストを避けるため post-bookOne 即時確定 (B1) を採用。
- **Rollback 方式**: stack-based (C1) と deferred-commit (C3) を検討。前者は Pass 1/2 で group が非ネスト的なため過剰、後者は bookOne の現行 signature 前提と衝突する。per-group snapshot map (C2) を採用。
- **出力順**: group 連続配置 (E2) は既存 fixed-point テストと wire format 互換を破壊するため却下、入力順維持 (E1) を採用。

## Recommended approach + rationale

採用設計は **A1 + B1（+ Amount.Currency フォールバック）+ C2 + D6 + E1**。各々の選択理由はそれぞれ:

- **A1** (flat slice + per-currency bucket): 既存の Source ポインタ bind ロジックを最小変更で再利用でき、currency lookup が O(1) で upstream の dict 表現と直接対応する。
- **B1 + Amount.Currency フォールバック**: `PostingWeight` の precedence と整合し、Pass 1 では `p.Amount != nil` 保証で sentinel 不要。Pass 2 は `residual.Currency` で確定し、こちらも sentinel 不要。設計の対称性が良い。
- **C2** (per-group snapshot map): Pass 1 と Pass 2 で group identity の決まり方が違う（前者は posting ごと、後者は residual の事後決定）という非対称性に対応できる柔軟性を持つ。group が 1 つの大多数 txn でのオーバーヘッドが最小。
- **D6** (drop 済み group に join された auto-posting も drop): upstream の `interpolate_group` の意味論と一致。`solveResidual` の現行 single-currency semantics を変えずに済む。
- **E1** (入力順維持): 既存 fixed-point テストと wire format 契約を守る最小破壊路線。

## End-to-end 検証

1. 各 step 完了後に `bazel build //...` と `bazel test //...` を実行
2. 最終的に以下が all green であること:
   - `bazel test //pkg/inventory/...`（本変更の主戦場）
   - `bazel test //...`（下流回帰検知）
3. 手動確認用 fixture（オプション）: 2 currency group で片方が lot 不足で失敗する `.beancount` を作り、reducer に通して `txn.Postings` から失敗 group が消えること、残 group が `BookedPosting` を生成することを確認

## Critical files (再掲)

- `/home/user/go-beancount/pkg/inventory/reducer.go`
  - `postingResolution` struct（200-216）、`newPostingResolution`（226-232）、`add*` メソッド群（237-354）、`finalize`（361-371）
  - `visitTxn`（392-536）、Pass 1（415-463）、Pass 2（469-532）
  - `stateTrace` 関連（615 行付近）、`prepareForEdit`（636-649）
- `/home/user/go-beancount/pkg/inventory/weight.go:43-73`（`PostingWeight` を再利用）
- `/home/user/go-beancount/pkg/inventory/reducer_test.go`（テスト追加）
- `/home/user/go-beancount/pkg/inventory/booking.go`（`bookOne` 等は **変更しない**、ただし呼び出し側の挙動理解のため参照）
- `/home/user/go-beancount/docs/plans/cost-holder-interface.md:46-49`（上流挙動の根拠資料）

## 実行フロー（orchestration skill）

承認後は orchestration skill の Step ループ（Phase 4〜8）を Step 1 から順次実行する:

- 各 Step ごとに planner で `Detailed Design`（Contract + Suggested Internals）を確定 → generator が実装 → evaluator + go-code-reviewer の並列 review → 収束 → commit
- Commit message は CLAUDE.md の方針（why / 振る舞い / 設計意図、imperative subject）に従う
- Step 6 のテストは Step 1-5 と並走的に追加するが、各 Step の commit 時点で対応するテストが green になっているのが望ましい

各 Step 着手時の `### Detailed Design` 小節は、本ファイルの該当 Step 節の下に追記していく。
