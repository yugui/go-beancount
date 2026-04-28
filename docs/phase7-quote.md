# Phase 7 (`pkg/quote`) 高レベル設計

## Context

go-beancount の Phase 1〜6a は完了し、Phase 6 のプラグイン基盤は最近のリストラ
クチャリング (commit `d302d7d`) で `pkg/ext/postproc` (postprocessor) と
`pkg/ext/goplug` (汎用 `.so` ローダ) に再編された。`goplug.Load(path)` は
プラグインの `InitPlugin func() error` を呼び、プラグイン側がそれぞれの
ホストレジストリへ自身を登録する設計で、postproc 以外のレジストリ
(quote / importer など) へも同じ機構をそのまま流用できる。

Phase 7 はこの基盤の最初の実応用レイヤである。目的は、外部マーケット/為替
ソースから価格を取得し、`ast.Price` ディレクティブとして流通可能な形で
出力するライブラリと CLI を提供すること。Beancount 上流の `bean-price`
ツールに対する syntactic compatibility は明示的に放棄し、Go 側で素直な
型ベース API を構築する。本フェーズの成果物がそのまま Phase 12
`beansprout quote` サブコマンドの土台になる。

中核の設計課題は「quote source ごとに自然なバッチング軸が異なる」
こと: (date × commodity) の 2 次元行列に対し、source ごとに単セル/行/列/
部分行列/最新値のみ、と取得粒度が違う。フレームワークは source 著者に
自然な軸だけ実装させ、ユーザのリクエスト形状にドライバが適合させる。

## ロックイン済み設計判断

- **`ast.Price` をそのまま流通型として使う** (`pkg/ast/directives.go:221`)。
  source 帰属・取得時刻などの追加情報は各 quoter が `Price.Meta` に書き込む
  個別責務とし、framework は強制しない。
- **`pkg/ext/goplug` を再利用** する。`pkg/quote` 専用ローダは作らない。
- **Phase 6c (extproc) は Phase 7 のスコープ外**。external-process Quoter は
  Phase 6c 完成後に別途。
- **bean-price の `price` meta 文法と互換性を保つ**。`Commodity` ディレクティブ
  上で同じ文字列が解釈できる。これにより以下が結果として確定する:
  - quote (denomination) 通貨はメタ値の中で必須指定 (`USD:source/SYM` の `USD`)。
    CLI 側に `--quote` の上書きフラグは作らない。
  - 同一ペア (commodity × quote 通貨) に対して複数 source を優先順で並べた
    fallback チェーンを記述できる (`USD:yahoo/X,google/X`)。
  - 同一 commodity が異なる quote 通貨ごとに独立したペア・チェーンを持てる
    (`"USD:yahoo/X JPY:yahoo/XJPY"`)。
- **pricedb は `FormatStream` のみ**。`MergeInto` 等のファイル書き戻しは
  Phase 10 (bean-daemon) の責任領域とし、Phase 7 では作らない。
- **`--date` の TZ 解釈は TZ なしカレンダー日付** (UTC 0:00 で内部表現)。
  source 固有 TZ への射影は quoter 著者の責務。
- **7a で動かすリファレンス source は ECB FX rates**。無認証・公開・stable・
  Range natively 可能で CI が再現可能。
- **CLI の繰り返し指定は flag 反復方式**。`,` 区切りは meta 内の fallback
  チェーン記法 (bean-price 互換) のためだけに使い、CLI の複数値には使わない。
- **既定の global concurrency は 32**。多くの source が 1 req/s 級の
  rate limit を持ち goroutine が `RateLimit` トークン待ちで安価にブロック
  すること、goroutine 自体が軽量であることを踏まえ、CPU 並列度 4 級の
  保守値を採らない。source 固有の上限は `sourceutil.RateLimit` /
  `sourceutil.Concurrency` で内側からさらに絞る。

## アーキテクチャ

### パッケージレイアウト

```
pkg/quote/
  api/              # Source インタフェース族・Spec・Capabilities (postproc/api と対称)
  meta/             # bean-price 互換 price-meta パーサ
  registry.go       # Register / Lookup / Names (init-time global, 重複 panic)
  fetch.go          # オーケストレータ Fetch(ctx, registry, spec, opts...)
  scheduler.go      # level-by-level fallback スケジューラ
  sourceutil/       # source 著者向けアダプタ・デコレータ群
  pricedb/          # Dedup + FormatStream (stdout 整形)
  std/
    ecb/            # ECB FX rates リファレンス実装 (7a)

cmd/beanprice/
  main.go           # cmd/beancheck と同じ骨格
  main_test.go
  BUILD.bazel
```

### 公開 API (`pkg/quote/api`)

```go
// Pair は (commodity, quote currency) を表す。bean-price meta における
// "USD:yahoo/AAPL" の AAPL (commodity) と USD (quote) を持つ単位。
type Pair struct {
    Commodity     string // ast.Price.Commodity / 通常 ast.Commodity.Currency と一致
    QuoteCurrency string // ast.Price.Amount.Currency
}

// SourceRef は 1 source × 1 source 上のシンボルを束ねる。
type SourceRef struct {
    Source string // registry に登録された source 名 (例: "yahoo")
    Symbol string // source 上のティッカ (例: "GOOG", "NASDAQ:GOOG")
    // Negate bool   // bean-price の '^' プレフィクス (1/X) — 7a では未対応として保留
}

// PriceRequest は「このペアの価格を、優先順の source 群で得る」単位。
// 同じ Pair に対する fallback チェーンは 1 リクエスト内に Sources で並ぶ。
// 同じ commodity の異なる QuoteCurrency は別 PriceRequest で表現される。
type PriceRequest struct {
    Pair    Pair
    Sources []SourceRef // index 0 が primary、以降が fallback
}

type Mode uint8
const ( ModeLatest Mode = iota; ModeAt; ModeRange )

type Spec struct {
    Requests   []PriceRequest // 1 個以上
    Mode       Mode
    At         time.Time      // ModeAt 時のみ (TZ なしカレンダー日付として 0:00 UTC)
    Start, End time.Time      // ModeRange 時のみ。半開区間 [Start, End)
}

type Capabilities struct {
    SupportsLatest bool
    SupportsAt     bool
    SupportsRange  bool
    BatchPairs     bool // 1 呼び出しで複数 (Pair, Symbol) を混載可能か
    RangePerCall   int  // 1 Range 呼び出しあたりの最大日数 (0 = unlimited)
}

type Source interface {
    Name() string
    Capabilities() Capabilities
}

// SourceQuery は scheduler が 1 source を 1 回叩くときに渡す束。
// (Pair, Symbol) のペアを並べる — quoter は Symbol を見て source 内 API を叩く。
type SourceQuery struct {
    Pair   Pair
    Symbol string
}

type LatestSource interface {
    Source
    QuoteLatest(ctx context.Context, q []SourceQuery) ([]ast.Price, []ast.Diagnostic, error)
}
type AtSource interface {
    Source
    QuoteAt(ctx context.Context, q []SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error)
}
type RangeSource interface {
    Source
    QuoteRange(ctx context.Context, q []SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error)
}
```

**サブインタフェース選定理由**: 単一メソッドで全形状をカバーする方式は
不可能形状を `error` で返す醜さが残り、3 メソッド必須にすると軸違いの
source に空実装を強いる。`Source` 基底 + `Capabilities` 構造体 + 任意
サブインタフェース、というハイブリッドが「natively できることだけ
宣言する」という設計意図を最も素直に表現できる。

**Pair vs SourceQuery を分けた理由**: 同じ commodity でも source ごとに
ティッカが違う (`GOOG` vs `NASDAQ:GOOG`)。Pair が請求の論理単位、
SourceQuery が source 呼び出し時の物理単位。`ast.Price` を組み立てる
ときは Pair の側を権威ソースとして使う。

### オーケストレータ `Fetch` の動作

```go
func Fetch(ctx context.Context, reg Registry, spec api.Spec, opts ...Option) ([]ast.Price, []ast.Diagnostic, error)

// Registry は名前 → Source の解決器。pkg/quote のグローバルレジストリも
// この interface を満たすので、テスト時に差し替え可能。
type Registry interface { Lookup(name string) (api.Source, bool) }

type Option func(*runConfig)
func WithConcurrency(n int) Option           // 既定 32 (per-Fetch 全体セマフォ)
func WithClock(now func() time.Time) Option  // テスト・Latest↔At 換算
func WithObserver(fn func(Event)) Option     // フェッチ計測 hook (cross-cutting)
```

#### 取得単位 (unit)

スケジューラは Spec を「unit」の集合に展開する:

- `ModeLatest`/`ModeAt`: unit = `(PriceRequest)` — 1 価格点
- `ModeRange`: unit = `(PriceRequest, dateBucket)` — RangePerCall を超えない
  日付バケット単位

各 unit は state を持つ:
- `depth` (現在試している `Sources[depth]`)
- `done` (Price が得られた / すべての fallback を試し尽くした)

#### Level-by-level スケジューリング (deadlock 回避の核)

```
未確定 unit が残っている間:
  1. すべての未確定 unit について、現在の depth が指す source を集める。
     source 名 → 集合(units) のマップを作る。
  2. 各 source について、対応する units を SourceQuery のリストに変換し、
     その source の Capabilities と spec.Mode から最適なメソッドを選択して
     ディスパッチする (BatchPairs/RangePerCall に従い 1 source あたり
     複数呼び出しになりうる)。
  3. すべての source の呼び出しを並列に走らせ、レベル全体が終わるのを待つ
     (全体は WithConcurrency セマフォで束縛)。
  4. 結果を unit に取り込む:
       - Price が得られた → done
       - Price なし or error → depth++; depth >= len(Sources) → done(failed)
  5. レベル番号をインクリメントし、未確定 unit が残っていればループ継続。
```

**この方式が deadlock を回避する論拠**: あるレベル内では各 source は
一度しか呼ばれず、レベル間にだけグローバルバリアがある。同じ batch source
を異なる優先度で共有する commodity (例: A は yahoo→google、B は
google→yahoo) があっても、レベル 0 では yahoo:[A], google:[B] が独立に
走る。レベル 1 で初めて B の yahoo フォールバックや A の google
フォールバックが必要かが判明し、必要なものだけが集まって 1 回ずつ走る。
循環待ちが構造的に発生しない。

**理想 (primary 失敗を待ってから fallback) との関係**: バリアが一段階
入る代わりに、「primary が成功した unit については fallback が叩かれない」
という意図は厳密に守られる。primary がまだ動いている間に fallback を
投機的に走らせる方式は、batch 共有時に予測不能なフォールバック爆発を
起こすため採らない。

**Capabilities ↔ Mode の降格**: 各 source 呼び出しの内側でこれまで通りの
段階表が適用される (Latest→At、At→Range±1日、Range→At grid 等)。
`ModeRange` + `LatestSource` のみは `now()` ∈ range の 1 unit のみ満たし、
他には `quote-mode-unsupported` Warning を残す。

**エラー方針**:
- 各 source 呼び出しの `error`/panic は `quote-fetch-error` Diagnostic に
  変換し、その unit は次の depth へ進む。
- `Fetch` が `error` を返すのは ① `ctx.Err() != nil` ② 結果 Price が
  0 件 のときのみ。1 件でも得られれば nil error + diagnostic 蓄積。

#### `FallbackSource` を作らない

各 PriceRequest が `Sources[]` を直接持ち、scheduler がチェーンを処理する。
"compose して 1 名前で公開する fallback source" は今フェーズの API では
不要 — meta / CLI が一級で表現できる。

### Source 著者向けヘルパー (`pkg/quote/sourceutil`)

| ヘルパー | 用途 |
|---|---|
| `WrapSingleCell(name, fn) Source` | (1 ペア × 1 日付) 関数を `AtSource` にラップ |
| `DateRangeIter(s AtSource, cal Calendar) RangeSource` | 営業日カレンダーで日付ループし `Range` を提供 |
| `BatchPairs(s AtSource, n int) AtSource` | 単セル At を n 件まとめて並列呼びし `BatchPairs=true` を宣言 |
| `Concurrency(s Source, n int) Source` | セマフォベースの並列度束縛デコレータ |
| `RateLimit(s Source, rps float64, burst int) Source` | 1 source あたりのトークンバケット (例: 1 req/s)。3 メソッド全てに適用 |
| `RetryOnError(s Source, policy) Source` | 4xx/429/5xx 指数バックオフ。429 は `RateLimit` が無くても効く後段防壁 |
| `Cache(s Source, opts CacheOptions) Source` | Latest/At/Range すべてに適用可能な on-memory キャッシュ |

```go
type CacheOptions struct {
    TTL        time.Duration // 0 = 無期限 (Fetch ごとに作って捨てる用途を想定)
    MaxEntries int           // 0 = 無制限
}
```

**`Cache` の責務と典型的な利用シナリオ**:

- ECB のように「1 回叩くと要求外の通貨も含む batch をまるごと返す」種類の
  source では、最初の呼び出しで返った `ast.Price` を
  `(Date.UTC, Commodity, QuoteCurrency)` キーで全件 indexing しておけば、
  後続レベルで同 source が違う pair について叩かれた時にキャッシュヒット
  して呼び出しを省略できる。
- scheduler の level-by-level 進行下で、レベル k で source S が batch
  呼びされ、レベル k+1 で別 unit がまた S を必要とした場合 (異なる
  優先度で S を共有する commodity 群がある時にこれが起きる) に効く。
- 実装: 各メソッドの戻り `[]ast.Price` をキャッシュに書き込み、次回
  同メソッド呼び出し時はリクエスト側を「キャッシュにあるもの / 無いもの」
  に分割し、無いものだけを下流に流す。すべて hit なら下流呼び出しゼロ。
  下流が partial fan-out を許さない場合 (`BatchPairs=false`) はリクエスト
  単位で分割する。
- 既定では `Fetch` 1 回ごとにフレッシュなインスタンスを作る使い方を
  想定。プロセスを跨ぐ永続キャッシュは責任分離して別レイヤに譲る。

ドライバの動的選択 (`Fetch` 内) と意図的に重複している関数群がある: 著者が
公開時に静的に固定したい場面 (例: `DateRangeIter` で `SupportsRange=true`
を宣言、`RateLimit` で source 固有の API 制限を保証する) と、ドライバが
実行時に補う場面の役割分担。

### bean-price 互換 meta パーサ (`pkg/quote/meta`)

```go
// ParsePriceMeta は Commodity ディレクティブの "price" メタ値を解釈し、
// 0 個以上の PriceRequest を返す。1 メタ値内に複数の psource (空白区切り)
// があれば QuoteCurrency ごとに別 PriceRequest になる。
//
// 文法 (bean-price 互換のうち 7a でサポートする部分集合):
//   value   := psource (WS+ psource)*
//   psource := CCY ":" chain
//   chain   := entry ("," entry)*
//   entry   := SOURCE "/" SYMBOL          // SYMBOL は ':' を含み得る
//   CCY     := beancount currency
//   SOURCE  := registry の source 名
//
// 7a 未対応: '^' 反転接頭辞、CCY 省略形 (常に明示要求)。これらは
// 将来エラーで弾く一方、検出時は将来拡張点として明示 diag コードを返す。
func ParsePriceMeta(commodity, raw string) ([]api.PriceRequest, []ast.Diagnostic)

// ExtractFromCommodity は ast.Commodity から price meta を読み、
// 上の関数を経由して PriceRequest を返す。meta が無ければ nil。
func ExtractFromCommodity(c *ast.Commodity, metaKey string) ([]api.PriceRequest, []ast.Diagnostic)
```

bean-price 互換のため、デフォルトの meta キーは `"price"` とし、CLI で
`--meta-key` により上書き可能にする。

### レジストリ (`pkg/quote`)

```go
func Register(name string, s api.Source) // 重複 panic
func Lookup(name string) (api.Source, bool)
func Names() []string
```

`pkg/ext/postproc/registry.go` を完全踏襲。goplug プラグインは自分の
`InitPlugin()` 内で `quote.Register("yahoo", &YahooSource{})` を呼ぶ。

## `pkg/quote/pricedb`

```go
func Dedup(prices []ast.Price, keepFirst bool) (kept []ast.Price, diags []ast.Diagnostic)
func FormatStream(w io.Writer, prices []ast.Price) error
```

- dedup キー: `(Date.UTC, Commodity, Amount.Currency)` — 同 quote 通貨の
  重複を判定。
- `FormatStream` は `pkg/printer` を経由し Date 昇順・Commodity 昇順で
  安定ソートしてから書く。

`MergeInto` 系は今フェーズでは作らない。

## CLI 設計 — `cmd/beanprice`

`cmd/beancheck` の構造をコピー (main.go + main_test.go + BUILD.bazel)。

```
beanprice [flags]
  --ledger PATH                  # 繰り返し可。*ast.Commodity 走査
  --commodity CODE                # 繰り返し可。--ledger 必須。指定 commodity に絞る
  --source 'CCY:source/SYM[,source/SYM]*'  # 繰り返し可。bean-price 互換
  --meta-key NAME                # 既定 "price"。bean-price と同じ
  --date YYYY-MM-DD              # ModeAt
  --range START..END             # ModeRange (半開)
  --latest                       # ModeLatest (date/range が無ければ既定 ON)
  --concurrency N                # 既定 32 (per-Fetch 全体)
  --plugin PATH                  # 繰り返し可。goplug 経由 quoter 読込
  --strict                       # Warning も exit 1 に
```

繰り返し指定はすべて flag 反復 (`--commodity AAPL --commodity GOOG`)。
`,` は **bean-price meta 値内の fallback チェーン専用** であり、CLI の
複数値の区切りには使わない。

### Commodity ディレクティブのメタキー (bean-price 互換)

- `price: "USD:yahoo/AAPL"` — 単一 quote 通貨、単一 source
- `price: "USD:yahoo/AAPL,google/AAPL"` — 単一通貨に対する優先度付き
  fallback チェーン
- `price: "USD:yahoo/X JPY:yahoo/XJPY"` — 同 commodity の異なる quote 通貨を
  別 PriceRequest として展開
- `price: "USD:yahoo/X,google/X JPY:google/XJPY"` — 上記の組合せ

メタが無いか、`--meta-key` で指す key が無い commodity は対象外
(自前の disable フラグは設けない)。

CLI #1 のロジック: `*ast.Commodity` を走査 → meta から `price` を読む →
`pkg/quote/meta.ExtractFromCommodity` で `[]PriceRequest` 化 → 集約。

### `--source` の文法

bean-price meta の単一 psource とまったく同じ文字列を受理する:

```
--source 'USD:yahoo/AAPL'
--source 'USD:yahoo/AAPL,google/AAPL'  # fallback チェーン
--source 'USD:yahoo/AAPL' --source 'JPY:yahoo/AAPLJPY'  # 複数ペア
```

quote 通貨の省略・上書きフラグは無い (互換性の帰結)。

### `--commodity` の解決

`--commodity` は `--ledger` 指定下で **commodity 名のフィルタ** として
動作する。`AAPL` 単独で `--source` 無しの指定は不可 (どこから取るか不明
なため)。`--ledger` 内で当該 commodity に `price` meta が無ければ
`quote-no-meta` Warning を出してスキップ。

### 出力先・終了コード

- **stdout**: 整形済み `price` ディレクティブのみ
- **stderr**: `ast.Diagnostic` を `cmd/beancheck` と同形式で
- exit 0=全成功 / 1=フェッチエラーまたは `--strict` 下の Warning / 2=
  CLI 自身の失敗 (フラグ誤り、ファイル不在、プラグインロード失敗)

## クォータ間共通メタデータの監査

framework 側で扱うべき関心事は cross-cutting な観測 hook
(`WithObserver`) 1 つに集約する。

| 関心事 | 配置 |
|---|---|
| Source 帰属 / 取得時刻 / 通貨ピボット | quoter の `Price.Meta` 任せ |
| リトライ・レート制限・キャッシュ | `sourceutil` ヘルパー (任意) |
| フェッチ遅延・リプレイログ | `WithObserver` hook 1 本 |
| Fallback (per-pair 優先順) | `PriceRequest.Sources` で表現、scheduler が処理 |
| 冪等性 | quoter 任せ (HTTP GET 主体で問題なし) |

`Price.Meta` への書き込み内容を framework が規約化することはしない。

## サブフェーズ計画

| サブ | 範囲 | 規模 |
|---|---|---|
| **7a** | `pkg/quote/{api,meta,registry,fetch,scheduler,sourceutil,pricedb}`、`pkg/quote/std/ecb`、`cmd/beanprice` | 6 新パッケージ + 1 リファレンス source + 1 CLI |
| **7b** | goplug プラグイン経路の動作確認 (testdata 雛形 + `--plugin` 結線 + ドキュメント)。コア API は固定済 | 新パッケージ 0 |
| **~~7c~~** | extproc Source。**Phase 6c 完成まで保留**。Phase 7 範囲外 | — |

7a で API を fix することがプラグイン ABI 安定化のチェックポイント。
ECB を選ぶことで CI が外部認証・レート制限ナシで通る。

## 受け入れ基準・検証方法

1. `bazel build //...` と `bazel test //...` が pass。
2. `pkg/quote/meta` のテーブルテストで bean-price 互換文法を網羅 (単一/
   複数 quote 通貨、fallback チェーン、空白区切り、未対応 `^` の拒絶、
   不正文字列の Diagnostic)。
3. `pkg/quote/std/ecb` の単体テストが録画 HTTP fixture (testdata/) で pass。
   ライブネットワーク呼びは別タグで分離。
4. `cmd/beanprice` の e2e 試験:
   - `bazel run //cmd/beanprice -- --source 'EUR:ecb/USD' --latest` が
     stdout に `2026-04-26 price USD ... EUR` 形式の 1 行を出す。
   - `--ledger testdata/with-meta.beancount --range 2026-01-01..2026-02-01`
     で複数 commodity・複数 quote 通貨の時系列が出る。
   - `--commodity AAPL --commodity GOOG --ledger ...` のフラグ反復が機能。
   - `--source 'USD:nonexistent/X'` が `quote-source-unknown` で exit 2。
5. `pkg/quote/scheduler` の単体試験 (モック source):
   - 各 Capabilities 組合せ (Latest only, At only, Range only,
     BatchPairs true/false) のドライバ降格パスを網羅。
   - 「primary 失敗 → fallback 成功」パスで primary 成功 unit には
     fallback が呼ばれないこと。
   - **deadlock 回帰テスト**: A=[yahoo,google], B=[google,yahoo] の
     共有構成で、両 source が `BatchPairs=true` の場合に各レベルでちょうど
     1 回ずつしか叩かれず、両方の Price が確定すること (記録された
     呼び出し順を assertion)。
   - レベル間の barrier が機能し、`ctx.Done()` で全 unit に伝搬すること。
6. `bazel run //:gazelle` で BUILD ファイルが生成済の状態を維持。
7. コミットメッセージは CLAUDE.md の方針に従い、各ファイルではなく
   「導入する behavior と設計上の判断」を述べる。

## 主要参考ファイル

- `pkg/ast/directives.go:118-129` (`ast.Commodity`)、`pkg/ast/directives.go:220-232` (`ast.Price`)
- `pkg/ast/ast.go:24-56` (`ast.Diagnostic`、Severity)、`pkg/ast/ast.go:173-177` (`Metadata`)
- `pkg/ext/goplug/goplug.go` (`Load`, `Manifest`, `APIVersion`)
- `pkg/ext/postproc/api/plugin.go` (Plugin/Input/Result の型配置パターン)
- `pkg/ext/postproc/registry.go` (init-time global registry の実装)
- `pkg/ext/postproc/apply.go` (ランナーの error / diagnostic 取扱い)
- `pkg/ext/postproc/std/checkclosing/plugin.go:23-26` (init での dual-name registration)
- `pkg/loader/loader.go` (CLI 経由のロード起点)
- `cmd/beancheck/main.go`、`cmd/beancheck/BUILD.bazel` (CLI 骨格テンプレ)
- `pkg/printer/` (`ast.Price` の整形)
- `CLAUDE.md` (commit message 方針、Bazel + Gazelle ワークフロー)
