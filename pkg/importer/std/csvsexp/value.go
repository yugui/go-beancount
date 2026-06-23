package csvsexp

import (
	"fmt"
	"regexp"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// valKind is the compile-time type tag of a [value]. Key-typed kinds box a
// csvbase.Key[T] whose T the tag identifies; the remaining kinds box
// configuration literals consumed while building the pipeline.
type valKind int

const (
	kindStrKey valKind = iota
	kindDateKey
	kindAmtKey
	kindRowKey
	kindCostKey
	kindBoolKey
	kindAmountValueKey
	kindPostingKey
	kindPostingListKey
	kindStrListKey
	kindMetaKey
	kindTxnKey
	kindStrLit
	kindIntLit
	kindBoolLit
	kindRegex
	kindDict
	kindNumberFormat
	kindMapMode
	kindRowBindings
	kindFunction
)

func (k valKind) String() string {
	switch k {
	case kindStrKey:
		return "string-key"
	case kindDateKey:
		return "date-key"
	case kindAmtKey:
		return "amount-key"
	case kindRowKey:
		return "row-key"
	case kindCostKey:
		return "cost-key"
	case kindBoolKey:
		return "bool-key"
	case kindAmountValueKey:
		return "amount-value-key"
	case kindPostingKey:
		return "posting-key"
	case kindPostingListKey:
		return "posting-list-key"
	case kindStrListKey:
		return "string-list-key"
	case kindMetaKey:
		return "metadata-key"
	case kindTxnKey:
		return "transaction-key"
	case kindStrLit:
		return "string"
	case kindIntLit:
		return "integer"
	case kindBoolLit:
		return "boolean"
	case kindRegex:
		return "regex"
	case kindDict:
		return "dict"
	case kindNumberFormat:
		return "number-format"
	case kindMapMode:
		return "map-mode"
	case kindRowBindings:
		return "row-bindings"
	case kindFunction:
		return "function"
	default:
		return "unknown"
	}
}

// value is a typed, boxed result of evaluating one S-expression form. kind
// identifies how to unbox v.
type value struct {
	kind valKind
	v    any
}

// env is a lexical scope mapping names to values. Child scopes (created by let*
// and by function application) shadow parents.
type env struct {
	parent *env
	vars   map[string]value
}

func newEnv(parent *env) *env {
	return &env{parent: parent, vars: map[string]value{}}
}

// lookup returns the value bound to name in the nearest enclosing scope, or
// (value{}, false) when no scope binds it.
func (e *env) lookup(name string) (value, bool) {
	for s := e; s != nil; s = s.parent {
		if v, ok := s.vars[name]; ok {
			return v, true
		}
	}
	return value{}, false
}

func (e *env) bind(name string, v value) { e.vars[name] = v }

// wantKind returns nil when v has kind want, otherwise a type-mismatch error.
func wantKind(v value, want valKind) error {
	if v.kind != want {
		return fmt.Errorf("expected %s, got %s", want, v.kind)
	}
	return nil
}

func asStrKey(v value) (csvbase.Key[string], error) {
	if err := wantKind(v, kindStrKey); err != nil {
		return csvbase.Key[string]{}, err
	}
	return v.v.(csvbase.Key[string]), nil
}

func asDateKey(v value) (csvbase.Key[time.Time], error) {
	if err := wantKind(v, kindDateKey); err != nil {
		return csvbase.Key[time.Time]{}, err
	}
	return v.v.(csvbase.Key[time.Time]), nil
}

func asAmtKey(v value) (csvbase.Key[*csvkit.Amount], error) {
	if err := wantKind(v, kindAmtKey); err != nil {
		return csvbase.Key[*csvkit.Amount]{}, err
	}
	return v.v.(csvbase.Key[*csvkit.Amount]), nil
}

func asRowKey(v value) (csvbase.Key[map[string]string], error) {
	if err := wantKind(v, kindRowKey); err != nil {
		return csvbase.Key[map[string]string]{}, err
	}
	return v.v.(csvbase.Key[map[string]string]), nil
}

func asCostKey(v value) (csvbase.Key[*ast.CostSpec], error) {
	if err := wantKind(v, kindCostKey); err != nil {
		return csvbase.Key[*ast.CostSpec]{}, err
	}
	return v.v.(csvbase.Key[*ast.CostSpec]), nil
}

func asBoolKey(v value) (csvbase.Key[bool], error) {
	if err := wantKind(v, kindBoolKey); err != nil {
		return csvbase.Key[bool]{}, err
	}
	return v.v.(csvbase.Key[bool]), nil
}

func asAmountValueKey(v value) (csvbase.Key[*ast.Amount], error) {
	if err := wantKind(v, kindAmountValueKey); err != nil {
		return csvbase.Key[*ast.Amount]{}, err
	}
	return v.v.(csvbase.Key[*ast.Amount]), nil
}

func asPostingKey(v value) (csvbase.Key[ast.Posting], error) {
	if err := wantKind(v, kindPostingKey); err != nil {
		return csvbase.Key[ast.Posting]{}, err
	}
	return v.v.(csvbase.Key[ast.Posting]), nil
}

func asPostingListKey(v value) (csvbase.Key[[]ast.Posting], error) {
	if err := wantKind(v, kindPostingListKey); err != nil {
		return csvbase.Key[[]ast.Posting]{}, err
	}
	return v.v.(csvbase.Key[[]ast.Posting]), nil
}

func asStrListKey(v value) (csvbase.Key[[]string], error) {
	if err := wantKind(v, kindStrListKey); err != nil {
		return csvbase.Key[[]string]{}, err
	}
	return v.v.(csvbase.Key[[]string]), nil
}

func asMetaKey(v value) (csvbase.Key[ast.Metadata], error) {
	if err := wantKind(v, kindMetaKey); err != nil {
		return csvbase.Key[ast.Metadata]{}, err
	}
	return v.v.(csvbase.Key[ast.Metadata]), nil
}

func asTxnKey(v value) (csvbase.Key[*ast.Transaction], error) {
	if err := wantKind(v, kindTxnKey); err != nil {
		return csvbase.Key[*ast.Transaction]{}, err
	}
	return v.v.(csvbase.Key[*ast.Transaction]), nil
}

func asString(v value) (string, error) {
	if err := wantKind(v, kindStrLit); err != nil {
		return "", err
	}
	return v.v.(string), nil
}

func asRegex(v value) (*regexp.Regexp, error) {
	if err := wantKind(v, kindRegex); err != nil {
		return nil, err
	}
	return v.v.(*regexp.Regexp), nil
}

func asDict(v value) (map[string]string, error) {
	if err := wantKind(v, kindDict); err != nil {
		return nil, err
	}
	return v.v.(map[string]string), nil
}

func asMapMode(v value) (csvkit.MapMode, error) {
	if err := wantKind(v, kindMapMode); err != nil {
		return csvkit.Verbatim, err
	}
	return v.v.(csvkit.MapMode), nil
}

func asNumberFormat(v value) (csvkit.NumberFormat, error) {
	if err := wantKind(v, kindNumberFormat); err != nil {
		return csvkit.NumberFormat{}, err
	}
	return v.v.(csvkit.NumberFormat), nil
}

func asRowBindings(v value) (map[string]csvbase.Key[string], error) {
	if err := wantKind(v, kindRowBindings); err != nil {
		return nil, err
	}
	return v.v.(map[string]csvbase.Key[string]), nil
}

// funcValue is a compile-time, macro-style function: each application
// re-evaluates body in a child scope of closure, emitting a fresh set of
// pipeline steps. Recursion is not supported.
type funcValue struct {
	params  []string
	body    node
	closure *env
}
