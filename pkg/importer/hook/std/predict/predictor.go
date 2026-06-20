package predict

import (
	"math"
	"sort"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// Predictor infers the counter account for a query Features built from an
// import's single known posting. It is the extension point for alternative
// learners; the default ([NewKNNPredictor]) is k-NN over TF-IDF cosine.
type Predictor interface {
	// Predict returns the best counter-account candidate and ok=true, or
	// ok=false when there is no basis (empty corpus, or the query shares no
	// in-vocabulary term with any eligible example). The caller applies the
	// confidence and margin thresholds; Predict itself never abstains on
	// threshold grounds.
	Predict(q Features) (Prediction, bool)
}

// Prediction is the predictor's verdict for one query. Confidence and Margin
// are both in [0,1] and orthogonal: Confidence is the cosine similarity of the
// single closest supporting example (how good the best match is); Margin is the
// normalized weighted-vote gap between the top-1 and top-2 accounts (how clearly
// the winner leads), and is 1 when only one account is in contention.
type Prediction struct {
	Account    ast.Account
	Confidence float64
	Margin     float64
	Evidence   Evidence
}

// Evidence describes the closest supporting example, for diagnostics.
type Evidence struct {
	// Score is the cosine similarity of the closest supporting example.
	Score float64
	// Date is that example's transaction date.
	Date time.Time
}

// KNNOption configures [NewKNNPredictor].
type KNNOption func(*knnConfig)

// WithK sets the number of nearest neighbors aggregated per query (default 10).
func WithK(k int) KNNOption { return func(c *knnConfig) { c.k = k } }

// WithExactAmountBonus sets the similarity bonus added to a neighbor whose
// absolute amount and currency exactly match the query (default 0.25). The
// bonus affects ranking and voting only, not the reported Confidence.
func WithExactAmountBonus(b float64) KNNOption { return func(c *knnConfig) { c.exactBonus = b } }

// WithMinSupport sets the minimum number of training examples an account must
// have to be a candidate (default 1).
func WithMinSupport(n int) KNNOption { return func(c *knnConfig) { c.minSupport = n } }

// WithRecencyHalfLife sets the half-life (in days) of the recency weight
// applied to each neighbor's contribution: a neighbor whose example date is
// halfLife days older than the query date contributes 50% of its similarity
// to both ranking and voting. A non-positive value disables recency weighting
// (all neighbors weighted equally). The query's reported Confidence is the
// undecayed raw cosine and is unaffected. Default 365 (one year).
func WithRecencyHalfLife(days float64) KNNOption {
	return func(c *knnConfig) { c.recencyHalfLife = days }
}

// NewKNNPredictor builds a k-NN / TF-IDF predictor over examples. Construction
// indexes the corpus (computes IDF and L2-normalized term vectors); there is no
// iterative training. The returned Predictor is immutable and safe for
// concurrent use.
func NewKNNPredictor(examples []Example, opts ...KNNOption) Predictor {
	cfg := knnConfig{k: 10, exactBonus: 0.25, minSupport: 1, recencyHalfLife: 365}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.k < 1 {
		cfg.k = 1
	}
	if cfg.minSupport < 1 {
		cfg.minSupport = 1
	}

	n := len(examples)
	df := map[string]float64{}
	for i := range examples {
		seen := map[string]struct{}{}
		for _, t := range examples[i].Features.Terms {
			if _, ok := seen[t.Token]; ok {
				continue
			}
			seen[t.Token] = struct{}{}
			df[t.Token]++
		}
	}
	idf := make(map[string]float64, len(df))
	for tok, d := range df {
		idf[tok] = math.Log((1+float64(n))/(1+d)) + 1
	}

	vecs := make([]exampleVec, 0, n)
	support := map[ast.Account]int{}
	for i := range examples {
		ex := &examples[i]
		support[ex.Label]++
		vecs = append(vecs, exampleVec{
			vec:       normalizedVector(ex.Features.Terms, idf, false),
			label:     ex.Label,
			date:      ex.Date,
			amountAbs: ex.Features.AmountAbs,
			currency:  ex.Features.Currency,
		})
	}
	return &knnPredictor{cfg: cfg, idf: idf, vecs: vecs, support: support}
}

type knnConfig struct {
	k               int
	exactBonus      float64
	minSupport      int
	recencyHalfLife float64 // days; <=0 disables decay
}

type exampleVec struct {
	vec       map[string]float64
	label     ast.Account
	date      time.Time
	amountAbs *apd.Decimal
	currency  string
}

type knnPredictor struct {
	cfg     knnConfig
	idf     map[string]float64
	vecs    []exampleVec
	support map[ast.Account]int
}

// normalizedVector builds the L2-normalized TF-IDF vector of terms. When
// query is true, tokens absent from idf (out of vocabulary) are dropped; for
// training vectors every token is in idf by construction. Returns nil for an
// empty or zero-norm vector.
func normalizedVector(terms []Term, idf map[string]float64, query bool) map[string]float64 {
	vec := make(map[string]float64, len(terms))
	for _, t := range terms {
		w, ok := idf[t.Token]
		if query && !ok {
			continue
		}
		vec[t.Token] += t.Weight * w
	}
	if len(vec) == 0 {
		return nil
	}
	var sum float64
	for _, w := range vec {
		sum += w * w
	}
	if sum == 0 { // degenerate; drop
		return nil
	}
	norm := math.Sqrt(sum)
	for k := range vec {
		vec[k] /= norm
	}
	return vec
}

func (p *knnPredictor) Predict(q Features) (Prediction, bool) {
	qvec := normalizedVector(q.Terms, p.idf, true)
	if qvec == nil {
		return Prediction{}, false
	}

	neighbors := p.nearest(q, qvec)
	if len(neighbors) == 0 {
		return Prediction{}, false
	}

	aggs, order := aggregate(neighbors)
	sort.Slice(order, func(i, j int) bool {
		ai, aj := aggs[order[i]], aggs[order[j]]
		if ai.vote != aj.vote {
			return ai.vote > aj.vote
		}
		if !ai.latest.Equal(aj.latest) {
			return ai.latest.After(aj.latest)
		}
		return order[i] < order[j]
	})

	win := aggs[order[0]]
	margin := 1.0
	if len(order) > 1 && win.vote > 0 {
		margin = (win.vote - aggs[order[1]].vote) / win.vote
	}
	return Prediction{
		Account:    order[0],
		Confidence: win.maxCos,
		Margin:     margin,
		Evidence:   Evidence{Score: win.maxCos, Date: win.evDate},
	}, true
}

type neighbor struct {
	adj   float64
	raw   float64
	label ast.Account
	date  time.Time
}

// nearest returns the top-k eligible neighbors of the query, sorted by adjusted
// similarity (descending), breaking ties by more-recent date then account name.
// Each neighbor's adj carries the exact-amount bonus and the recency-decay
// weight; raw is the undecayed cosine and remains the basis of Confidence.
func (p *knnPredictor) nearest(q Features, qvec map[string]float64) []neighbor {
	var ns []neighbor
	for i := range p.vecs {
		ev := &p.vecs[i]
		if ev.vec == nil || p.support[ev.label] < p.cfg.minSupport {
			continue
		}
		raw := dot(qvec, ev.vec)
		if raw <= 0 {
			continue
		}
		adj := raw
		if exactAmountMatch(q, ev) {
			adj += p.cfg.exactBonus
		}
		adj *= recencyWeight(q.Date, ev.date, p.cfg.recencyHalfLife)
		ns = append(ns, neighbor{adj: adj, raw: raw, label: ev.label, date: ev.date})
	}
	sort.Slice(ns, func(i, j int) bool {
		if ns[i].adj != ns[j].adj {
			return ns[i].adj > ns[j].adj
		}
		if !ns[i].date.Equal(ns[j].date) {
			return ns[i].date.After(ns[j].date)
		}
		return ns[i].label < ns[j].label
	})
	if len(ns) > p.cfg.k {
		ns = ns[:p.cfg.k]
	}
	return ns
}

type accAgg struct {
	vote   float64
	maxCos float64
	evDate time.Time
	latest time.Time
}

// aggregate sums per-account votes over neighbors and returns the aggregates
// plus the accounts in first-seen order (which is deterministic because
// neighbors is already sorted).
func aggregate(neighbors []neighbor) (map[ast.Account]*accAgg, []ast.Account) {
	aggs := map[ast.Account]*accAgg{}
	var order []ast.Account
	for _, nb := range neighbors {
		a := aggs[nb.label]
		if a == nil {
			a = &accAgg{}
			aggs[nb.label] = a
			order = append(order, nb.label)
		}
		a.vote += nb.adj
		if nb.raw > a.maxCos {
			a.maxCos = nb.raw
			a.evDate = nb.date
		}
		if nb.date.After(a.latest) {
			a.latest = nb.date
		}
	}
	return aggs, order
}

// dot is the inner product of two sparse vectors. With both vectors
// L2-normalized it is the cosine similarity, in [0,1] since all weights are
// non-negative.
func dot(a, b map[string]float64) float64 {
	if len(a) > len(b) {
		a, b = b, a
	}
	var s float64
	for k, av := range a {
		s += av * b[k]
	}
	return s
}

func exactAmountMatch(q Features, ev *exampleVec) bool {
	if q.AmountAbs == nil || ev.amountAbs == nil || q.Currency != ev.currency {
		return false
	}
	return q.AmountAbs.Cmp(ev.amountAbs) == 0
}

// recencyWeight returns the multiplier applied to a neighbor's contribution
// given a query and example date pair. It returns 1.0 (no decay) when the
// half-life is non-positive, when either date is zero, or when the example
// is at-or-after the query date.
func recencyWeight(q, ex time.Time, halfLife float64) float64 {
	if halfLife <= 0 || q.IsZero() || ex.IsZero() {
		return 1.0
	}
	dt := q.Sub(ex).Hours() / 24.0
	if dt <= 0 {
		return 1.0
	}
	return math.Exp2(-dt / halfLife)
}
