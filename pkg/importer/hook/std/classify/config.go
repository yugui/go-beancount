package classify

import (
	"fmt"
	"regexp"

	"github.com/yugui/go-beancount/pkg/importer/hook"
)

type config struct {
	Rules []ruleConfig `toml:"rule"`
}

// ruleConfig is the TOML-decoded representation of a single rule. An empty
// string in PayeeRegex or NarrationRegex means that selector is absent.
type ruleConfig struct {
	PayeeRegex     string `toml:"payee_regex"`
	NarrationRegex string `toml:"narration_regex"`
	Currency       string `toml:"currency"`
	Account        string `toml:"account"`
}

type rule struct {
	payeeRegex     *regexp.Regexp // nil when PayeeRegex was unset
	narrationRegex *regexp.Regexp // nil when NarrationRegex was unset
	currency       string         // "" means infer
	account        string
}

// newHook is the factory function registered under kind "classify". On failure
// it returns (nil, err) with the error prefixed "classify: configure: ".
func newHook(name string, decode func(dest any) error) (hook.Hook, error) {
	if decode == nil {
		return nil, fmt.Errorf("classify: configure: nil decoder")
	}
	var cfg config
	if err := decode(&cfg); err != nil {
		return nil, fmt.Errorf("classify: configure: %w", err)
	}
	rules, err := buildRules(cfg)
	if err != nil {
		return nil, fmt.Errorf("classify: configure: %w", err)
	}
	return &Hook{name: name, rules: rules}, nil
}

func buildRules(cfg config) ([]rule, error) {
	out := make([]rule, 0, len(cfg.Rules))
	for i, rc := range cfg.Rules {
		r, err := validateRule(i, rc)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func validateRule(i int, rc ruleConfig) (rule, error) {
	if rc.PayeeRegex == "" && rc.NarrationRegex == "" {
		return rule{}, fmt.Errorf("rule[%d]: at least one of payee_regex/narration_regex required", i)
	}
	if rc.Account == "" {
		return rule{}, fmt.Errorf("rule[%d]: account is required", i)
	}
	r := rule{
		currency: rc.Currency,
		account:  rc.Account,
	}
	if rc.PayeeRegex != "" {
		re, err := regexp.Compile(rc.PayeeRegex)
		if err != nil {
			return rule{}, fmt.Errorf("rule[%d]: payee_regex: %w", i, err)
		}
		r.payeeRegex = re
	}
	if rc.NarrationRegex != "" {
		re, err := regexp.Compile(rc.NarrationRegex)
		if err != nil {
			return rule{}, fmt.Errorf("rule[%d]: narration_regex: %w", i, err)
		}
		r.narrationRegex = re
	}
	return r, nil
}
