# Fix booked-cost weight on partial reductions + introduce `inventory.Lot` type

## Context

下記の ledger は upstream `bean-check` を通過するが `cmd/beancheck` で失敗する:

```beancount
1970-01-01 open Income:Gain
1970-01-01 open Assets:A "STRICT"
1970-01-01 open Assets:B "STRICT"
1970-01-01 open Assets:Cash "STRICT"

1970-01-01 * "txn"
  Assets:A          10 A { }
  Assets:Cash       -100 JPY

1970-01-01  * "txn"
  Assets:B          10 B { }
  Assets:Cash       -1.00 USD

1970-01-02 * "sell"
  Assets:A          -5 A {}
  Assets:B          -5 B {}
  Assets:Cash        150 JPY
  Assets:Cash        0.50 USD
  Income:Gain
```

実際のエラー:
- `transaction does not balance: non-zero residual in JPY, USD [unbalanced-transaction]`
- `Income:Gain: residual spans 2 currencies [JPY USD] but a single unknown can only absorb one [unresolvable-interpolation]`

### 根本原因

買い側 `{}` は reducer Pass 2 で `synthesizeCostSpec` (`pkg/inventory/reducer.go:1089`) により残差を吸う `CostSpec{Total:100, Currency:JPY}` に補完される。`ResolveCost` (`pkg/inventory/cost.go:51`) は canonical な per-unit を `Number=10` として埋めるが、user 由来の literal を round-trip 用に保持するため、inventory に保存される lot は

```
Cost{Number: 10, Currency: JPY, PerUnit: nil, Total: {100, JPY}}
```

の形になる。`Total` は printer のための presentation provenance (`pkg/ast/cost.go:124-156` の Cost doc 参照)。

売り `-5 A {}` のとき `addSingleLotReduction` (`reducer.go:278`) が `step.Lot.Clone()` をそのまま reducing posting の `Cost` に install するため、Total provenance も付いてくる。`PostingWeight` (`pkg/inventory/weight.go:32`) → `(*ast.Posting).TotalCost()` (`pkg/ast/cost.go:258`) は `PerUnit==nil && Total!=nil` を見て `case total != nil:` 枝に落ち、

```
weight = sign(units) × |Total| = sign(-5) × |100| = -100 JPY   ← BUG
正しい値: units × Number = -5 × 10 = -50 JPY
```

を返す。B 側も同様で `-1.00 USD` (正しくは `-0.50 USD`)。結果として sell の per-currency 残差が JPY=+50, USD=-0.50 となり、`Income:Gain` 単一 auto-posting が両通貨を吸えず `resolveFreeResiduals` (`reducer.go:1049`) が `unresolvable-interpolation` を出し、続いて `transaction_balances` が `unbalanced-transaction` を出す。

upstream beancount の `Cost` 型は `(number, currency, date, label)` のみで provenance を持たず、常に `units × cost.number` で weight を計算するため同じ問題は起きない。

### 単純化案がなぜダメか

「booked Cost なら一律 `units × Number` を使う」と TotalCost を直すだけだと `TestLoad_TotalCostAugmentationBalances` (`pkg/loader/loader_test.go:177`) を壊す。同テストは

```
2025-01-01 * "txn"
  Assets:A           -4.1 STOCK {{   4.2 JPY }}
  Assets:A         -100   STOCK {{ 100 JPY }}
  Assets:B          104.1 STOCK {{ 104.2 JPY }}
```

を「user-written Total が ±合計で exact 0 JPY だから balance する」性質に依存し、`T/|units|` の非循環有理数を経由しない divide-free な Total-form weight 計算を pin している。`units × Number` だと apd の 34 桁精度で微小残差が出て tolerance.Infer が JPY tolerance を ~10⁻³⁴ に narrow し reject される。

したがって **augmenting posting では Total-form の divide-free 計算が必要、reducing posting では `units × Number` が必要** という二重要求があり、どこかで「reducing 側にだけ provenance を持ち込まない」運用が要る。

### 採用方針: 案3改

`pkg/inventory` に **`Lot` 型を新設** し、これを「provenance を持たない、inventory tier 専用の lot identity」型とする。inventory 内部で保持・運搬される lot 情報は全てこの `Lot` 型に置き換える。AST tier の `*ast.Cost` (provenance を持つ型) は augmenting posting の round-trip 用にのみ残す。reducing posting に install する `*ast.Cost` は `Lot` から変換して作る (=必然的に provenance が nil)。

メリット:
- 「inventory tier には provenance を残さない」という規律が **型シグネチャで強制される** (PerUnit/Total フィールドが存在しないのでアクセス自体がコンパイルエラー)
- API 契約が単純化: `func (i *Inventory) Get(ccy string) []Position` の戻り値の `Position.Cost *Lot` を見れば provenance なしが自明
- 将来の reducer 改修・経路追加に対して safe (inventory boundary で守られる)
- upstream beancount の `Cost(number, currency, date, label)` と一対一の対応
- メモリ節約 (大量 lot を扱う場合)

論点B (printer 出力): reducing posting の printer 出力が `{{T CUR}}` から `{N CUR, Date}` に変わる件は許容済み。upstream beancount に揃う方向の矯正。

論点C (関連バグ): `pkg/ext/postproc/std/implicitprices` 等の booked Cost の Total から per-unit を逆算しているコードは partial reduction で潜在的に壊れている可能性が plan agent から指摘されており、本タスクの最終ステップで現状確認して対応する。

## 実装ステップ

### Step 1: `inventory.Lot` 型の新設と `ast.Lot` alias の削除

**ゴール**: `pkg/inventory` に独立した `Lot` 型を導入し、`pkg/ast/cost.go:160` の `type Lot = Cost` alias を削除する。

**変更**:
- `pkg/inventory/cost.go`: `type Lot = ast.Lot` (alias) を削除し、独立した struct を新設:
  ```go
  type Lot struct {
      Number   apd.Decimal
      Currency string
      Date     time.Time
      Label    string
  }
  ```
  `Equal`, `Clone` メソッドを追加 (`ast.Cost.Equal` / `Clone` と同じロジックだが provenance フィールドを持たないので簡潔)。
- `pkg/ast/cost.go:160` の `type Lot = Cost` alias を削除。
- `ast.Lot` を参照している箇所を grep で洗い出し、`inventory.Lot` または `*ast.Cost` に置換 (この時点では既存型を維持するために `inventory.Lot = ast.Cost` の混在は許容しないので、ast 側で `ast.Lot` を使っていた箇所は全て `ast.Cost` に書き換える)。

**テスト**: 既存テストが pass すること。新規テストは不要 (型変更が中心)。

### Detailed Design (Step 1)

#### Contract

**New type `inventory.Lot`** (exported, defined in `pkg/inventory`):

```go
type Lot struct {
    Number   apd.Decimal
    Currency string
    Date     time.Time
    Label    string
}
```

- `Number` is a value `apd.Decimal` (not `*apd.Decimal`), matching `ast.Cost.Number` so future Step 2 transfers `Position.Cost.Number → Lot.Number` are a direct field copy with no nil-handling discontinuity.
- `Currency string`, `Label string`, `Date time.Time` — all value types, all carrying the same semantics as the identically-named fields on `ast.Cost`.
- The struct has **no** `PerUnit`, `Total`, `Span`, or any other provenance/source-location field. Their absence is the type's whole purpose: inventory-tier code cannot reach for them.

**Methods on `*inventory.Lot`** (exported):

- `func (l *Lot) Equal(o *Lot) bool` — nil-safe both sides; two nils are equal, nil-vs-non-nil is unequal. Equality is by-value over `(Number via apd.Decimal.Cmp == 0, Currency, Date via time.Time.Equal, Label)`. Mirrors `ast.Cost.Equal` semantics exactly (which is already provenance-independent).
- `func (l *Lot) Clone() *Lot` — nil-safe; returns nil for a nil receiver. Number is deep-copied via `ast.CloneDecimal`; the other fields are value-copied. The clone owns its coefficient buffer.

**Sealed-union membership**: `inventory.Lot` does **not** implement `ast.CostHolder`. The CostHolder interface is the AST-tier union of parse-form (`*ast.CostSpec`) and booked-form (`*ast.Cost`); inventory-internal lot values are deliberately outside that vocabulary. Generator must not add `GetPerUnit`/`GetTotal`/`isCostHolder`/etc. methods.

**Removals**:

- `pkg/ast/cost.go` lines 158-160 (the `type Lot = Cost` alias and its 2-line godoc) are deleted in their entirety. After Step 1, the identifier `ast.Lot` does not exist.
- `pkg/inventory/cost.go:22-24` (the `type Lot = ast.Lot` alias and its 2-line godoc) is deleted. The replacement is the new struct definition described above (whose home is `Suggested Internals`).
- `type Cost = ast.Cost` at `pkg/inventory/cost.go:18-20` is **not** touched. `ResolveCost`'s signature `(*Cost, *ast.Diagnostic, error)` is **not** touched. `Position.Cost *Cost`, `ReductionStep.Lot Cost`, `BookedPosting.Lot *Lot` are **not** retyped here — all of these are Step 2.

**Cross-step coupling**:

- After Step 1, `inventory.Lot` and `inventory.Cost` (=`ast.Cost`) are two **distinct types** that happen to share four field names (`Number`, `Currency`, `Date`, `Label`). The codebase still uses `*Cost` everywhere lots flow; `inventory.Lot` is unreferenced by any other production code until Step 2 wires `Position.Cost`/`ReductionStep.Lot`/`BookedPosting.Lot`/`ResolveCost` to it.
- The only behavioral assertion at the end of Step 1 is that the existing test suite (specifically `pkg/inventory/...`, `pkg/ast/...`, `pkg/loader/...`, `pkg/printer/...`, `pkg/validation/...`, `pkg/ext/...`) all build and pass with no diffs in observed behavior. New tests added in Step 1 are unit tests for `Lot.Equal` and `Lot.Clone` only.

**Test obligations added in Step 1** (in `pkg/inventory/lot_test.go`, new file):

- `TestLot_Equal` — covers: identical lots equal; differing Number unequal; differing Currency unequal; differing Date (same wall-clock different `Location`) handled per `time.Time.Equal`; differing Label unequal; nil/nil equal; nil/non-nil unequal.
- `TestLot_Clone` — covers: nil receiver returns nil; clone is deeply independent (mutating clone's `Number` coefficient does not affect original; Currency/Label string equal).

These two tests are the only new tests. No existing test is rewritten or expected to change.

**BUILD/Gazelle**: after editing files, `bazel run //:gazelle` regenerates `pkg/inventory/BUILD.bazel` and `pkg/ast/BUILD.bazel`. No manual BUILD edits required.

#### Suggested Internals

These are advisory; the implementer may choose differently if a discovered constraint argues for it.

**File placement**: `pkg/inventory/lot.go` (new file). Rationale: the project's one-concept-one-file pattern (`position.go`, `matcher.go`, `weight.go`, etc.) makes a dedicated `lot.go` discoverable. Alternative: place the struct in the existing `cost.go` directly below the `type Cost = ast.Cost` line; this keeps "cost-shaped types" colocated and avoids a new file. The dedicated file is suggested because Step 2 will add `costToLot` / `Lot.ToCost` conversion helpers that read more naturally near the type definition than mixed with `ResolveCost`.

**Imports in the new file**: `time` and `github.com/cockroachdb/apd/v3` for the field types; `github.com/yugui/go-beancount/pkg/ast` for `ast.CloneDecimal` inside `Clone`. (Alternative: avoid the `ast` import by inlining the `apd.Decimal` clone, but `ast.CloneDecimal` is the project's single-source helper and re-using it is consistent.)

**`Equal` implementation style**: mirror `ast.Cost.Equal` directly — early-return nil checks, then field comparisons in the order Currency, Label (string equality, cheap), Date (`time.Time.Equal`), Number (`apd.Decimal.Cmp`). The reason to mirror exactly is that future readers comparing the two types should see identical structure.

**`Clone` implementation style**: mirror `ast.Cost.Clone` but drop the two provenance branches:

```go
func (l *Lot) Clone() *Lot {
    if l == nil {
        return nil
    }
    return &Lot{
        Number:   *ast.CloneDecimal(&l.Number),
        Currency: l.Currency,
        Date:     l.Date,
        Label:    l.Label,
    }
}
```

Alternative: write a value-receiver `Clone() Lot` since the type is small and value-copyable. This is rejected as a suggestion because `Position.Cost` will be `*Lot` after Step 2 and pointer-receiver `Clone() *Lot` composes more naturally with nil-bearing fields; matching the `*ast.Cost.Clone() *ast.Cost` shape also reduces friction at conversion-helper sites in Step 2.

**Godoc tone**: short — a 2-3 line type doc on `Lot` plus 1-2 line contracts on `Equal` and `Clone`, in the project's brevity-first style. Suggested type doc emphasizes (a) provenance-free, (b) inventory-tier, (c) future role: pivot type for `Position`/`ReductionStep` once Step 2 lands. The `Position.Cost` field doc on `pkg/inventory/position.go:13` may optionally gain a one-line forward reference, but this is non-essential at Step 1.

**Where to place tests**: `pkg/inventory/lot_test.go`. Use `package inventory_test` (consistent with most existing tests in the package per `inventory_test.go` etc.) so the tests exercise the exported surface only.

**Removal of the orphan alias**: after deleting `ast.Lot`, the godoc text on `ast.Cost` (lines 124-156) still mentions "Cost" only; no surviving comment references "Lot" as a type. A quick grep for `Lot` in `pkg/ast/cost.go` after the edit should return no results other than incidental prose (none expected). The implementer should run that grep and adjust if anything turns up.

#### Alternatives

**A. Keep `inventory.Lot` as a type alias for the new struct and not introduce a new identifier** — rejected; aliases for in-package types add indirection with no benefit (the existing `type Cost = ast.Cost` alias is justified by cross-package retargeting only).

**B. Define `inventory.Lot` with pointer-typed `Number *apd.Decimal`** — rejected; `ast.Cost.Number` is value-typed at the booked tier where the number is always concrete, and matching that shape keeps Step 2's `costToLot` a trivial field assignment. Also nil-decimal handling is a known source of bugs.

**C. Define `inventory.Lot` to satisfy `ast.CostHolder`** — rejected; `CostHolder` is explicitly sealed via the unexported `isCostHolder()` marker (`pkg/ast/cost.go:28-30`), making `Lot` a `CostHolder` re-merges the two tiers at the interface level, and no Step 1 callsite needs it.

**D. Defer alias removal: keep `type Lot = Cost` in `ast`** — rejected; the grep shows `ast.Lot` has exactly one usage in the entire repo (the dual alias in `pkg/inventory/cost.go:24` deleted in this same step), so there is no blast-radius risk to defer for.

**E. Co-locate the struct in `pkg/inventory/cost.go`** — left as the explicit alternative in Suggested Internals; the implementer may pick based on what feels cleanest when they actually edit the file.

#### Recommendation + rationale

Recommended path:

1. Create `pkg/inventory/lot.go` with the new `Lot` struct, `Equal`, and `Clone`. Imports: `time`, `github.com/cockroachdb/apd/v3`, `github.com/yugui/go-beancount/pkg/ast` (for `CloneDecimal`).
2. Delete the alias block at `pkg/inventory/cost.go:22-24` (godoc + `type Lot = ast.Lot`).
3. Delete the alias block at `pkg/ast/cost.go:158-160` (godoc + `type Lot = Cost`).
4. Create `pkg/inventory/lot_test.go` with `TestLot_Equal` and `TestLot_Clone`.
5. Run `bazel run //:gazelle` then `bazel test //...` and confirm green.

Rationale highlights:

- **Why a fresh struct, not a renamed/repurposed `ast.Cost`** (vs Alternative C): the entire premise of the bug fix in later steps is that inventory-tier code must not be able to reach for `PerUnit`/`Total`. Type identity is the project's chosen enforcement mechanism.
- **Why value-typed `Number`** (vs Alternative B): symmetry with `ast.Cost.Number` makes the Step 2 conversion `costToLot` a one-line field copy.
- **Why a dedicated file** (vs Alternative E): Step 2's conversion helpers will land near the type definition, and an already-named `lot.go` is the natural home. Implementer is explicitly free to overrule.
- **Why delete `ast.Lot` immediately** (vs Alternative D): grep shows zero blast radius.

### Step 2: inventory 内部の `Position.Cost`, `ReductionStep.Lot`, `BookedPosting.Lot` を `*Lot` に移行

**ゴール**: inventory tier で運搬される lot 情報の型を全て新しい `inventory.Lot` に変更する。

**変更**:
- `pkg/inventory/position.go`: `Position.Cost` の型を `*Cost` (=`*ast.Cost`) から `*Lot` に変更。
- `pkg/inventory/reducer.go`: `BookedPosting.Lot *Lot`、`ReductionStep.Lot Lot` に変更。
- `pkg/inventory/cost.go`: `ResolveCost` の戻り値型を `*Cost` から `*Lot` に変更 (provenance を捨てる: `out.PerUnit = nil`, `out.Total = nil` 相当を新型では「フィールドが無い」で実現)。
- `pkg/inventory/inventory.go`: `Inventory.Add(Position)`、`Inventory.Get` 等の API の `Position.Cost` 利用箇所を Lot 対応に。`Inventory.Reduce` の `step.Lot = *p.Cost.Clone()` (`inventory.go:262`) も `step.Lot = *p.Cost` (Lot 値) に。
- `ast.Cost` ↔ `inventory.Lot` の変換ヘルパー:
  - `func costToLot(c *ast.Cost) *Lot` — augmenting posting の install 直前で AST Cost から Lot を作る (provenance を捨てる)
  - `func (l *Lot) ToCost() *ast.Cost` — reducing posting に Cost を install するため Lot から AST Cost を作る (PerUnit/Total は nil で構築)

**テスト**: 既存 inventory テスト・統合テストが全て pass すること。`BookedPosting.Reduction.Lot.Number` などフィールド名アクセスは互換のまま。`*ast.Cost` を期待していた箇所は変換ヘルパーで吸収。

### Step 3: reducer の `step.Lot` install を Lot → Cost 変換経由に変更し、reducing posting の Cost から provenance を排除

**ゴール**: reducing posting の AST `Cost` が provenance を持たない `*ast.Cost` で install されるようにする。

**変更**:
- `pkg/inventory/reducer.go` 内の三箇所:
  - `addSingleLotReduction` (~line 282): `pr.postings[i].Cost = step.Lot.Clone()` → `pr.postings[i].Cost = step.Lot.ToCost()`
  - `addMultiLotReduction` (~line 316): `child.Cost = step.Lot.Clone()` → `child.Cost = step.Lot.ToCost()`
  - `promoteSingleLotReduction` (~line 504): `p.Cost = step.Lot.Clone()` → `p.Cost = step.Lot.ToCost()`
- `promoteLotAugmentation` (~line 462) は augmenting 経路なので **触らない**。
- `pr.addLotAugmentation` (~line 248) の `pr.postings[i].Cost = lot.Clone()` も augmenting 経路なので触らないが、`lot` の型が `*Lot` (Step 2 で変更済み) なので、ここは Lot → Cost 変換が必要。ただし augmenting posting には provenance を残したいので、Pass 2 で `synthesizeCostSpec` から `ResolveCost` を経由した `Cost` (provenance 込み) が AST に install されるルートを別途用意するか、もしくは `ast.CostSpec` を直接 install して `ResolveCost` は Lot だけ返すように re-design する。

**注意点**: Step 2 で `ResolveCost` の戻り値が Lot になったため、AST 側 augmenting posting の `Cost` install フローが変わる。augmenting posting には ResolveCost 入力の `CostSpec` から provenance を保持した `ast.Cost` を別ルートで作る必要がある。具体的には以下の選択肢:
  - (a) `ResolveCost` を二段に分ける: `ResolveCost` は Lot を返し、`ast.Cost` の生成は呼び出し側 (`bookAugment`) で行う。bookAugment は CostSpec から PerUnit/Total を読んで AST Cost に詰める。
  - (b) `ResolveCost` を `(*Lot, *ast.Cost, *ast.Diagnostic, error)` のような複数戻り値にする。
  - (a) の方が単一責任原則に沿うので推奨。

**テスト**: 既存 reducer テスト全 pass。新規テスト: reducing posting の `*ast.Cost` の `PerUnit==nil && Total==nil` を assert する unit test を `pkg/inventory/reducer_test.go` に追加。

### Step 4: `PostingWeight` に booked Cost without provenance の fallback を追加

**ゴール**: AST 上の booked `*ast.Cost` が PerUnit/Total を両方持たない場合に、weight = `units × Number` (in `Cost.Currency`) を返すよう `PostingWeight` を拡張する。

**変更**:
- `pkg/inventory/weight.go`: `PostingWeight` の `cost == nil` 判定後 (TotalCost が nil を返した後)、unbooked CostSpec の error 分岐の後に、booked Cost で provenance 無しの場合の分岐を追加:
  ```go
  if c, ok := p.Cost.(*ast.Cost); ok && c != nil {
      out := new(apd.Decimal)
      if _, err := apd.BaseContext.Mul(out, &p.Amount.Number, &c.Number); err != nil {
          return nil, err
      }
      return &ast.Amount{Number: *out, Currency: c.Currency}, nil
  }
  ```
- `PostingWeight` の doc コメントに「booked `*ast.Cost` で PerUnit/Total が両方 nil の場合は canonical `Number` を用いて `units × Number` を返す」と追記。

**テスト**: `pkg/inventory/weight_test.go` に新規ケース:
- booked Cost (Number=10, Currency=JPY, PerUnit=nil, Total=nil), units=-5 A → weight=-50 JPY
- booked Cost 同上、units=+10 A → weight=+100 JPY

### Step 5: 統合テストの追加 (本タスクの主目標である bug の regression test)

**ゴール**: 本 issue の ledger を loader 統合テストとして追加し、修正が有効である事を pin する。

**変更**:
- `pkg/loader/loader_test.go` に `TestLoad_PartialReductionOfDeferredCostMultiCurrency` を追加。ledger は本 issue の src そのまま。assert:
  - Diagnostics の severity Error が 0 件
  - sell transaction の `Income:Gain` posting の解決後 `Amount` が `{-100, JPY}` であること

**テスト**: 上記新規テストが pass すること。`TestLoad_TotalCostAugmentationBalances` 等 augmenting 経路の既存テストが pass し続けること。

### Step 6: 論点C の現状確認と対応

**ゴール**: plan agent が指摘した「`pkg/ext/postproc/std/implicitprices` 等の booked Cost の Total から per-unit を逆算するコード」を確認し、partial reduction 等で類似 bug を起こしていないか調査する。問題があれば修正、なければ本 fix の整合性が取れていることを確認する。

**手順**:
- `grep -rn 'Total\|PerUnit' pkg/ext/postproc/std/` で provenance フィールドを参照するコードを特定
- 該当箇所の意味を読み、partial reduction や非整数 lot で破綻するケースを構築できるか検討
- 破綻するなら別 commit で修正、または follow-up issue として記録

**判断基準**: 本 fix のスコープに含めるかは現状確認後の規模次第。問題が単純なら同 PR、複雑なら別 issue にする。

## 検証

実装後の最終確認:

1. `bazel test //pkg/inventory/... //pkg/validation/... //pkg/loader/... //pkg/ast/... //pkg/printer/... //pkg/ext/...`
2. 本 issue の ledger を保存して `bazel run //cmd/beancheck -- /path/to/ledger.beancount` → diagnostic 無し
3. `bazel run //cmd/beanfmt -- /path/to/ledger.beancount` で sell 側 posting が per-unit 形式 `{10 JPY, 1970-01-01}` で出力されることを確認 (論点B合意済み)
4. `TestLoad_TotalCostAugmentationWithAutoPostingBalances`, `TestLoad_TotalCostAugmentationBalances`, `TestLoad_StrictTotalCostReducingPostingMatchesByDerivedPerUnit` 等の augmenting Total-form テストが pass し続けること
5. step 5 の新規 regression test が pass すること

## 重要ファイル一覧

- `pkg/ast/cost.go` (alias 削除、TotalCost は触らない)
- `pkg/inventory/cost.go` (Lot 型新設、ResolveCost のリファクタ)
- `pkg/inventory/position.go` (Position.Cost 型変更)
- `pkg/inventory/inventory.go` (Add/Get/Reduce の lot 取り扱い)
- `pkg/inventory/reducer.go` (BookedPosting/ReductionStep の型、install 三箇所)
- `pkg/inventory/weight.go` (PostingWeight の fallback)
- `pkg/loader/loader_test.go` (regression test 追加)
- `pkg/inventory/reducer_test.go` (Step 3 検証 test)
- `pkg/inventory/weight_test.go` (Step 4 検証 test)
- `pkg/ext/postproc/std/implicitprices/` 周辺 (Step 6 で調査)
