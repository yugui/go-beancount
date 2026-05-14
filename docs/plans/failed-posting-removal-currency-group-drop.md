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
