package env

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// ErrNoOverload reports that no registered overload of a name accepts the
// given argument types. ErrAmbiguous reports that two or more overloads
// match equally well under widening. Both are returned (wrapped) by
// [Resolve]; test for them with [errors.Is].
var (
	ErrNoOverload = errors.New("query/env: no matching overload")
	ErrAmbiguous  = errors.New("query/env: ambiguous overload")
)

// widenings is the complete, explicit set of implicit type conversions the
// resolver applies, keyed by source type. Int widens to Decimal; nothing
// else widens. Each widening costs one toward the fewest-widenings tally.
var widenings = map[types.Type][]types.Type{
	types.Int: {types.Decimal},
}

// Resolve binds a call site to a single registered overload of name given
// the static argument types.
//
// Matching is case-insensitive on the name and exact on arity (an overload
// is a candidate only when len(In) == len(argTypes)). Among candidates:
//
//   - An exact match — every argTypes[i] equals In[i] — wins outright.
//   - Otherwise widening applies: an argument widens to its parameter via
//     the documented set (Int → Decimal, and no other). A candidate is
//     viable when every argument is either an exact match or widens to its
//     parameter. The unique viable candidate with the fewest widenings is
//     chosen.
//
// Resolve never panics on a resolution failure. It returns an error
// wrapping [ErrAmbiguous] when two viable candidates tie on widening count,
// and an error wrapping [ErrNoOverload] when the name is unknown or no
// candidate matches the arity and types.
func Resolve(name string, argTypes []types.Type) (api.Function, error) {
	candidates := overloads(name)

	var (
		best      api.Function
		bestCost  int
		bestFound bool
		ambiguous bool
	)
	for _, cand := range candidates {
		if len(cand.In) != len(argTypes) {
			continue
		}
		cost, ok := wideningCost(cand.In, argTypes)
		if !ok {
			continue
		}
		if cost == 0 {
			return cand, nil
		}
		switch {
		case !bestFound || cost < bestCost:
			best, bestCost, bestFound, ambiguous = cand, cost, true, false
		case cost == bestCost:
			ambiguous = true
		}
	}

	switch {
	case ambiguous:
		return api.Function{}, fmt.Errorf("%w for %s", ErrAmbiguous, formatCall(name, argTypes))
	case bestFound:
		return best, nil
	default:
		return api.Function{}, fmt.Errorf("%w for %s", ErrNoOverload, formatCall(name, argTypes))
	}
}

// anyMatchPenalty is the cost charged for binding an argument to a
// [types.Any] parameter slot. It dominates any realistic number of
// widenings, so a candidate that matches via widening always outranks one
// that matches only because a slot is Any. Among Any candidates, fewer Any
// slots still win.
const anyMatchPenalty = 1 << 20

// wideningCost reports the cost for args to satisfy params, and whether each
// argument matches its parameter. A parameter equal to the argument costs
// nothing; an Int->Decimal widening costs one; a [types.Any] parameter
// accepts any argument (including an untyped NULL) for [anyMatchPenalty].
// params and args have equal length.
func wideningCost(params, args []types.Type) (cost int, ok bool) {
	for i, want := range params {
		switch {
		case want == types.Any:
			cost += anyMatchPenalty
		case args[i] == want:
		case canWiden(args[i], want):
			cost++
		default:
			return 0, false
		}
	}
	return cost, true
}

func canWiden(from, to types.Type) bool {
	for _, t := range widenings[from] {
		if t == to {
			return true
		}
	}
	return false
}

func sameSignature(a, b []types.Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func formatSignature(in []types.Type) string {
	parts := make([]string, len(in))
	for i, t := range in {
		parts[i] = t.String()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func formatCall(name string, argTypes []types.Type) string {
	return name + formatSignature(argTypes)
}
