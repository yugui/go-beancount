package predict

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/hook"
	"github.com/yugui/go-beancount/pkg/loader"
)

const (
	defaultMinConfidence = 0.30
	defaultMinMargin     = 0.10
)

// config is the TOML-decoded representation of one predict instance. Pointer
// fields distinguish an unset key (use the default) from an explicit zero.
type config struct {
	Ledger           string       `toml:"ledger"`
	MinConfidence    *float64     `toml:"min_confidence"`
	MinMargin        *float64     `toml:"min_margin"`
	K                int          `toml:"k"`
	ExactAmountBonus *float64     `toml:"exact_amount_bonus"`
	MinSupport       int          `toml:"min_support"`
	Fields           fieldsConfig `toml:"fields"`
}

type fieldsConfig struct {
	Payee     *float64 `toml:"payee"`
	Narration *float64 `toml:"narration"`
	Metadata  *float64 `toml:"metadata"`
	Account   *float64 `toml:"account"`
	Sign      *float64 `toml:"sign"`
}

// newHook is the factory registered under kind "predict". It loads the
// configured ledger, harvests training examples, drops examples whose label is
// not currently open, and indexes the rest into a k-NN predictor. On failure it
// returns (nil, err) prefixed "predict: configure: ".
func newHook(name string, decode func(dest any) error) (hook.Hook, error) {
	if decode == nil {
		return nil, fmt.Errorf("predict: configure: nil decoder")
	}
	var cfg config
	if err := decode(&cfg); err != nil {
		return nil, fmt.Errorf("predict: configure: %w", err)
	}
	if cfg.Ledger == "" {
		return nil, fmt.Errorf("predict: configure: ledger is required")
	}
	led, err := loader.LoadFile(context.Background(), cfg.Ledger)
	if err != nil {
		return nil, fmt.Errorf("predict: configure: load ledger %q: %w", cfg.Ledger, err)
	}

	fw := mergeFieldWeights(cfg.Fields)
	tok := NewDefaultTokenizer()
	examples := openLabeled(ExtractExamples(led, tok, fw), OpenAccounts(led))

	var opts []KNNOption
	if cfg.K != 0 {
		opts = append(opts, WithK(cfg.K))
	}
	if cfg.MinSupport != 0 {
		opts = append(opts, WithMinSupport(cfg.MinSupport))
	}
	if cfg.ExactAmountBonus != nil {
		opts = append(opts, WithExactAmountBonus(*cfg.ExactAmountBonus))
	}

	return &Hook{
		name:          name,
		tok:           tok,
		fw:            fw,
		pred:          NewKNNPredictor(examples, opts...),
		minConfidence: floatOr(cfg.MinConfidence, defaultMinConfidence),
		minMargin:     floatOr(cfg.MinMargin, defaultMinMargin),
	}, nil
}

func mergeFieldWeights(fc fieldsConfig) FieldWeights {
	fw := DefaultFieldWeights()
	for _, o := range []struct {
		p   *float64
		dst *float64
	}{
		{fc.Payee, &fw.Payee},
		{fc.Narration, &fw.Narration},
		{fc.Metadata, &fw.Metadata},
		{fc.Account, &fw.Account},
		{fc.Sign, &fw.Sign},
	} {
		if o.p != nil {
			*o.dst = *o.p
		}
	}
	return fw
}

func floatOr(p *float64, def float64) float64 {
	if p != nil {
		return *p
	}
	return def
}

// openLabeled returns the subset of examples whose Label is currently open.
func openLabeled(examples []Example, open map[ast.Account]bool) []Example {
	var out []Example
	for _, ex := range examples {
		if open[ex.Label] {
			out = append(out, ex)
		}
	}
	return out
}
