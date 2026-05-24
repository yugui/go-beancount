package pricecompletion

import (
	"container/heap"
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

const (
	codeInvalidConfig = "price-completion-invalid-config"

	defaultTemporalBase  = 1.0
	defaultTemporalScale = 0.1
)

// arithCtx matches std/implicitprices: 34 digits ≈ IEEE-754 decimal128.
// The package-wide [apd.BaseContext] has Precision=0 (exact ops only);
// reciprocals such as 1/3 need a positive precision to terminate.
var arithCtx = apd.BaseContext.WithPrecision(34)

func init() {
	postproc.Register("beansprout.plugins.price_completion", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/sprout/pricecompletion", api.PluginFunc(apply))
}

// apply synthesizes missing Price directives derived from the existing
// ones via temporal shortest-path search. See the package godoc for the
// full behavior, upstream attribution, and deviations.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	cfg, cfgDiags := parseConfig(in.Config, in.Directive)

	var all []ast.Directive
	for _, d := range in.Directives {
		all = append(all, d)
	}
	if len(all) == 0 {
		if len(cfgDiags) != 0 {
			return api.Result{Diagnostics: cfgDiags}, nil
		}
		return api.Result{}, nil
	}

	operating := operatingCurrencies(in.Options)

	g := newPriceGraph(cfg.temporalBase, cfg.temporalScale)
	for _, d := range all {
		if p, ok := d.(*ast.Price); ok {
			g.add(p)
		}
	}
	g.sortHistory()

	out := make([]ast.Directive, 0, len(all))
	out = append(out, all...)

	if len(operating) == 0 || len(g.commodities) == 0 {
		return finalize(out, cfgDiags), nil
	}

	dates := g.dates()
	for _, date := range dates {
		if err := ctx.Err(); err != nil {
			return api.Result{}, err
		}
		g.buildForDate(date)
		for _, opc := range operating {
			if err := ctx.Err(); err != nil {
				return api.Result{}, err
			}
			if _, ok := g.commodities[opc]; !ok {
				continue
			}
			paths := g.shortestPaths(ctx, opc)
			for target, hop := range paths {
				if g.hasDirectPrice(date, target, opc) {
					continue
				}
				calc := g.derivePrice(hop.path, date)
				if calc == nil || !calc.hasCurrentDateEdge {
					continue
				}
				inv, err := reciprocal(calc.price)
				if err != nil {
					continue
				}
				out = append(out, &ast.Price{
					Span:      synthSpan(in.Directive),
					Date:      date,
					Commodity: target,
					Amount:    ast.Amount{Number: *inv, Currency: opc},
					Meta:      ast.CloneMeta(calc.closestMeta),
				})
			}
		}
	}

	return finalize(out, cfgDiags), nil
}

func finalize(out []ast.Directive, diags []ast.Diagnostic) api.Result {
	res := api.Result{Directives: out}
	if len(diags) != 0 {
		res.Diagnostics = diags
	}
	return res
}

// config holds the parsed temporal weighting parameters.
type config struct {
	temporalBase  float64
	temporalScale float64
}

func parseConfig(raw string, plug *ast.Plugin) (config, []ast.Diagnostic) {
	cfg := config{temporalBase: defaultTemporalBase, temporalScale: defaultTemporalScale}
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	var diags []ast.Diagnostic
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			diags = append(diags, configDiag(plug, fmt.Sprintf("malformed parameter %q: missing '='", part)))
			continue
		}
		key := strings.TrimSpace(part[:eq])
		val := strings.TrimSpace(part[eq+1:])
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			diags = append(diags, configDiag(plug, fmt.Sprintf("invalid numeric value for %q: %v", key, err)))
			continue
		}
		switch key {
		case "temporal_base":
			cfg.temporalBase = f
		case "temporal_scale":
			cfg.temporalScale = f
		default:
			diags = append(diags, configDiag(plug, fmt.Sprintf("unknown parameter %q", key)))
		}
	}
	return cfg, diags
}

func configDiag(plug *ast.Plugin, msg string) ast.Diagnostic {
	d := ast.Diagnostic{Code: codeInvalidConfig, Message: msg, Severity: ast.Error}
	if plug != nil {
		d.Span = plug.Span
	}
	return d
}

func operatingCurrencies(opts *ast.OptionValues) []string {
	if opts == nil {
		return nil
	}
	return opts.StringList("operating_currency")
}

// priceGraph indexes recorded *ast.Price observations so the plugin
// can derive missing prices via shortest-path search through the
// commodity network reachable from operating currencies.
//
// Lifecycle: the temporal index (history, commodities, direct) is
// populated once via add() and frozen by sortHistory(). The per-date
// adjacency view (currentAdjacency) is rebuilt by buildForDate before
// every Dijkstra pass — callers must invoke buildForDate(date) before
// reading shortestPaths/derivePrice for that date.
type priceGraph struct {
	// edge-weight tuning constants; weight =
	// temporalBase + temporalScale * ln(days_ago) for non-current edges.
	temporalBase  float64
	temporalScale float64

	commodities map[string]struct{}

	// per-pair price observations, sorted ascending by date after
	// sortHistory.
	history map[pairKey][]historicalEntry

	// per-date adjacency view. Rebuilt by buildForDate; reading
	// without a prior buildForDate yields stale state.
	currentAdjacency map[string][]edge

	// existing (date, base, quote) source prices, used by the
	// synthesis pass to avoid emitting duplicates.
	direct map[directKey]struct{}
}

// pairKey is a directed commodity pair — base→quote direction of a price.
type pairKey struct {
	base  string
	quote string
}

type historicalEntry struct {
	date  time.Time
	value apd.Decimal
	meta  ast.Metadata
}

// directKey identifies a source price so the synthesis pass skips
// re-emitting one that already exists.
type directKey struct {
	date  time.Time
	base  string
	quote string
}

// edge is one directed weighted graph edge in priceGraph.currentAdjacency.
type edge struct {
	target string
	weight float64
	value  apd.Decimal
	date   time.Time
	meta   ast.Metadata
	// true iff date equals the build's target date; the synthesis
	// pass requires at least one such edge on the chosen path.
	currentDateEdge bool
}

func newPriceGraph(base, scale float64) *priceGraph {
	return &priceGraph{
		temporalBase:  base,
		temporalScale: scale,
		commodities:   make(map[string]struct{}),
		history:       make(map[pairKey][]historicalEntry),
		direct:        make(map[directKey]struct{}),
	}
}

func (g *priceGraph) add(p *ast.Price) {
	base := p.Commodity
	quote := p.Amount.Currency
	g.commodities[base] = struct{}{}
	g.commodities[quote] = struct{}{}
	key := pairKey{base: base, quote: quote}
	g.history[key] = append(g.history[key], historicalEntry{
		date:  p.Date,
		value: p.Amount.Number,
		meta:  p.Meta,
	})
	g.direct[directKey{date: p.Date, base: base, quote: quote}] = struct{}{}
}

// sortHistory orders each per-pair history ascending by date so
// buildForDate can scan to find the most recent observation on or
// before the target.
func (g *priceGraph) sortHistory() {
	for k, h := range g.history {
		sort.SliceStable(h, func(i, j int) bool { return h[i].date.Before(h[j].date) })
		g.history[k] = h
	}
}

func (g *priceGraph) dates() []time.Time {
	seen := make(map[time.Time]struct{})
	for _, h := range g.history {
		for _, e := range h {
			seen[e.date] = struct{}{}
		}
	}
	out := make([]time.Time, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// buildForDate populates g.currentAdjacency with the best price for
// each pair on or before target.
func (g *priceGraph) buildForDate(target time.Time) {
	g.currentAdjacency = make(map[string][]edge, len(g.commodities))
	for key, hist := range g.history {
		if key.base == key.quote {
			continue
		}
		best, ok := bestPriceForDate(hist, target)
		if !ok {
			continue
		}
		weight := edgeWeight(target, best.date, g.temporalBase, g.temporalScale)
		current := best.date.Equal(target)

		g.currentAdjacency[key.base] = append(g.currentAdjacency[key.base], edge{
			target:          key.quote,
			weight:          weight,
			value:           best.value,
			date:            best.date,
			meta:            best.meta,
			currentDateEdge: current,
		})

		if inv, err := reciprocal(best.value); err == nil {
			g.currentAdjacency[key.quote] = append(g.currentAdjacency[key.quote], edge{
				target:          key.base,
				weight:          weight,
				value:           *inv,
				date:            best.date,
				meta:            best.meta,
				currentDateEdge: current,
			})
		}
	}
}

// bestPriceForDate returns the entry whose date is the largest value
// not later than target. hist must be sorted ascending by date.
func bestPriceForDate(hist []historicalEntry, target time.Time) (historicalEntry, bool) {
	// last entry with date <= target
	var best historicalEntry
	ok := false
	for _, e := range hist {
		if e.date.After(target) {
			break
		}
		best = e
		ok = true
	}
	return best, ok
}

func edgeWeight(target, observed time.Time, base, scale float64) float64 {
	if observed.Equal(target) {
		return 1.0
	}
	days := int(target.Sub(observed).Hours()/24 + 0.5)
	if days < 1 {
		days = 1
	}
	return base + scale*math.Log(float64(days))
}

// shortestHop carries the Dijkstra result for one target commodity.
type shortestHop struct {
	dist float64
	path []string
}

// shortestPaths runs Dijkstra from source and returns the
// minimum-weight path to every reachable commodity (excluding source).
func (g *priceGraph) shortestPaths(ctx context.Context, source string) map[string]shortestHop {
	dist := map[string]float64{source: 0}
	prev := make(map[string]string)
	visited := make(map[string]struct{})

	pq := &nodeHeap{}
	heap.Init(pq)
	heap.Push(pq, pqNode{dist: 0, name: source})

	// Cancellation poll counter: amortize ctx.Err() across pops.
	const cancelCheckInterval = 1024
	pops := 0

	for pq.Len() > 0 {
		pops++
		if pops%cancelCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return nil
			}
		}

		cur := heap.Pop(pq).(pqNode)
		if _, seen := visited[cur.name]; seen {
			continue
		}
		visited[cur.name] = struct{}{}

		for _, e := range g.currentAdjacency[cur.name] {
			if _, seen := visited[e.target]; seen {
				continue
			}
			nd := cur.dist + e.weight
			if best, ok := dist[e.target]; ok && nd >= best {
				continue
			}
			dist[e.target] = nd
			prev[e.target] = cur.name
			heap.Push(pq, pqNode{dist: nd, name: e.target})
		}
	}

	out := make(map[string]shortestHop, len(dist))
	for c, d := range dist {
		if c == source {
			continue
		}
		out[c] = shortestHop{dist: d, path: reconstructPath(prev, source, c)}
	}
	return out
}

func reconstructPath(prev map[string]string, source, target string) []string {
	// reverse-build the chain from target back to source
	var reversed []string
	for cur := target; cur != source; {
		reversed = append(reversed, cur)
		p, ok := prev[cur]
		if !ok {
			return nil
		}
		cur = p
	}
	reversed = append(reversed, source)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed
}

// pqNode is the priority-queue node for Dijkstra.
type pqNode struct {
	dist float64
	name string
}

// nodeHeap implements container/heap as a min-heap on dist.
type nodeHeap []pqNode

func (h nodeHeap) Len() int           { return len(h) }
func (h nodeHeap) Less(i, j int) bool { return h[i].dist < h[j].dist }
func (h nodeHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *nodeHeap) Push(x any) { *h = append(*h, x.(pqNode)) }
func (h *nodeHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// priceCalc carries the multiplied price along a path together with the
// metadata of the edge incident on the path's terminal commodity.
type priceCalc struct {
	price              apd.Decimal
	closestMeta        ast.Metadata
	hasCurrentDateEdge bool
}

func (g *priceGraph) derivePrice(path []string, target time.Time) *priceCalc {
	if len(path) < 2 {
		return nil
	}
	product := apd.New(1, 0)
	out := &priceCalc{}
	terminal := path[len(path)-1]
	for i := 0; i+1 < len(path); i++ {
		base, quote := path[i], path[i+1]
		e, ok := findEdge(g.currentAdjacency[base], quote)
		if !ok {
			return nil
		}
		mult := new(apd.Decimal)
		if _, err := arithCtx.Mul(mult, product, &e.value); err != nil {
			return nil
		}
		product = mult
		if e.currentDateEdge {
			out.hasCurrentDateEdge = true
		}
		if quote == terminal {
			out.closestMeta = e.meta
		}
	}
	out.price = *product
	return out
}

func findEdge(edges []edge, target string) (edge, bool) {
	for _, e := range edges {
		if e.target == target {
			return e, true
		}
	}
	return edge{}, false
}

func (g *priceGraph) hasDirectPrice(date time.Time, base, quote string) bool {
	_, ok := g.direct[directKey{date: date, base: base, quote: quote}]
	return ok
}

// reciprocal returns 1 / v. Returns an error when v is zero (or a
// non-finite apd form).
func reciprocal(v apd.Decimal) (*apd.Decimal, error) {
	if v.IsZero() {
		return nil, fmt.Errorf("division by zero")
	}
	out := new(apd.Decimal)
	if _, err := arithCtx.Quo(out, apd.New(1, 0), &v); err != nil {
		return nil, err
	}
	return out, nil
}

// synthSpan anchors a synthesized Price at the triggering plugin
// directive's span — synthesized directives have no original source
// posting to attribute. Returns the zero span when no plugin directive
// is available.
func synthSpan(plug *ast.Plugin) ast.Span {
	if plug == nil {
		return ast.Span{}
	}
	return plug.Span
}
