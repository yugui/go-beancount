package csvsexp

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/ianaindex"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// compiler accumulates pipeline build steps while evaluating one program. numFmt
// is the program-wide default number format applied to parse-amount and cost
// unless a form overrides it.
type compiler struct {
	b      *csvbase.Builder
	numFmt csvkit.NumberFormat
}

// compileProgram parses src and assembles a *csvbase.Driver bound to name. The
// program is a single (csv-import ...) form: leading keyword arguments configure
// the reader, gate, filters, default number format, and idempotency stamping,
// and the trailing positional form is the pipeline body (a let* or
// emit-transaction). Errors are prefixed "csvsexp:" and carry a source line.
func compileProgram(name, src string) (*csvbase.Driver, error) {
	top, err := parseProgram(src)
	if err != nil {
		return nil, err
	}
	if top.kind != nodeList || len(top.items) == 0 ||
		top.items[0].kind != nodeSymbol || top.items[0].text != "csv-import" {
		return nil, fmt.Errorf("csvsexp: top-level form must be (csv-import ...)")
	}

	kw, body, err := splitCsvImport(top.items[1:])
	if err != nil {
		return nil, err
	}

	c := &compiler{b: csvbase.NewBuilder()}

	if nfNode, ok := kw["number"]; ok {
		nf, err := c.evalNumberFormat(nfNode, newEnv(nil))
		if err != nil {
			return nil, err
		}
		c.numFmt = nf
	}

	emit, err := c.compileBody(body, newEnv(nil))
	if err != nil {
		return nil, err
	}
	pipeline := c.b.Emit(emit)

	reader, err := buildReader(kw)
	if err != nil {
		return nil, err
	}
	gate, err := buildGate(kw)
	if err != nil {
		return nil, err
	}
	filters, err := buildFilters(kw)
	if err != nil {
		return nil, err
	}
	var rowhash *csvbase.RowHash
	if rh, ok := kw["rowhash"]; ok {
		key, err := nodeText(rh)
		if err != nil {
			return nil, err
		}
		rowhash = &csvbase.RowHash{Key: key}
	}

	return csvbase.New(name, csvbase.Config{
		Reader:  reader,
		Gate:    gate,
		Mapper:  pipeline,
		Filters: filters,
		RowHash: rowhash,
	})
}

// splitCsvImport separates the csv-import arguments into its keyword options and
// the single trailing body form.
func splitCsvImport(items []node) (map[string]node, node, error) {
	kw := map[string]node{}
	var body node
	hasBody := false
	for i := 0; i < len(items); {
		if items[i].kind == nodeKeyword {
			if i+1 >= len(items) {
				return nil, node{}, fmt.Errorf("csvsexp: line %d: keyword :%s has no value", items[i].line, items[i].text)
			}
			kw[items[i].text] = items[i+1]
			i += 2
			continue
		}
		if hasBody {
			return nil, node{}, fmt.Errorf("csvsexp: line %d: csv-import accepts a single body form", items[i].line)
		}
		body = items[i]
		hasBody = true
		i++
	}
	if !hasBody {
		return nil, node{}, fmt.Errorf("csvsexp: csv-import has no body form")
	}
	return kw, body, nil
}

func (c *compiler) compileBody(n node, e *env) (csvbase.EmitFunc, error) {
	if n.kind != nodeList || len(n.items) == 0 || n.items[0].kind != nodeSymbol {
		return nil, fmt.Errorf("csvsexp: line %d: body must be a (let* ...) or (emit-transaction ...) form", n.line)
	}
	switch n.items[0].text {
	case "let*":
		return c.compileLet(n, e)
	case "emit-transaction":
		return c.compileEmit(n, e)
	default:
		return nil, fmt.Errorf("csvsexp: line %d: body must be let* or emit-transaction, got %q", n.line, n.items[0].text)
	}
}

func (c *compiler) compileLet(n node, parent *env) (csvbase.EmitFunc, error) {
	if len(n.items) != 3 {
		return nil, fmt.Errorf("csvsexp: line %d: let* expects (let* (bindings) body)", n.line)
	}
	bindings := n.items[1]
	if bindings.kind != nodeList {
		return nil, fmt.Errorf("csvsexp: line %d: let* bindings must be a list", bindings.line)
	}
	e := newEnv(parent)
	for _, b := range bindings.items {
		if b.kind != nodeList || len(b.items) != 2 || b.items[0].kind != nodeSymbol {
			return nil, fmt.Errorf("csvsexp: line %d: each binding must be (name expr)", b.line)
		}
		v, err := c.evalExpr(b.items[1], e)
		if err != nil {
			return nil, err
		}
		e.bind(b.items[0].text, v)
	}
	return c.compileBody(n.items[2], e)
}

// evalExpr evaluates one expression node in scope e.
func (c *compiler) evalExpr(n node, e *env) (value, error) {
	switch n.kind {
	case nodeString:
		return value{kind: kindStrLit, v: n.text}, nil
	case nodeInt:
		return value{kind: kindIntLit, v: n.num}, nil
	case nodeBool:
		return value{kind: kindBoolLit, v: n.b}, nil
	case nodeKeyword:
		switch n.text {
		case "strict":
			return value{kind: kindMapMode, v: csvkit.Strict}, nil
		case "verbatim":
			return value{kind: kindMapMode, v: csvkit.Verbatim}, nil
		}
		return value{}, fmt.Errorf("csvsexp: line %d: unexpected keyword :%s", n.line, n.text)
	case nodeSymbol:
		if v, ok := e.lookup(n.text); ok {
			return v, nil
		}
		return value{}, fmt.Errorf("csvsexp: line %d: unbound symbol %q", n.line, n.text)
	case nodeList:
		return c.evalList(n, e)
	default:
		return value{}, fmt.Errorf("csvsexp: line %d: cannot evaluate", n.line)
	}
}

func (c *compiler) evalList(n node, e *env) (value, error) {
	if len(n.items) == 0 {
		return value{}, fmt.Errorf("csvsexp: line %d: empty form ()", n.line)
	}
	head := n.items[0]
	if head.kind != nodeSymbol {
		return value{}, fmt.Errorf("csvsexp: line %d: form head must be a symbol", n.line)
	}
	args := n.items[1:]

	if v, ok := e.lookup(head.text); ok {
		if v.kind != kindFunction {
			return value{}, fmt.Errorf("csvsexp: line %d: %q is not callable (%s)", head.line, head.text, v.kind)
		}
		return c.applyFunction(v.v.(funcValue), head, args, e)
	}

	switch head.text {
	case "column":
		if err := arity(head, args, 1, 1); err != nil {
			return value{}, err
		}
		name, err := c.evalString(args[0], e)
		if err != nil {
			return value{}, err
		}
		return strKey(csvbase.Column(c.b, name)), nil

	case "row":
		if err := arity(head, args, 0, 0); err != nil {
			return value{}, err
		}
		return value{kind: kindRowKey, v: csvbase.Row(c.b)}, nil

	case "const":
		if err := arity(head, args, 1, 1); err != nil {
			return value{}, err
		}
		s, err := c.evalString(args[0], e)
		if err != nil {
			return value{}, err
		}
		return strKey(csvbase.Const(c.b, s)), nil

	case "hint":
		if err := arity(head, args, 1, 1); err != nil {
			return value{}, err
		}
		name, err := c.evalString(args[0], e)
		if err != nil {
			return value{}, err
		}
		return strKey(csvbase.Hint(c.b, name)), nil

	case "trim":
		if err := arity(head, args, 1, 1); err != nil {
			return value{}, err
		}
		k, err := c.evalStrKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		return strKey(csvbase.Trim(c.b, k)), nil

	case "required":
		if err := arity(head, args, 1, 2); err != nil {
			return value{}, err
		}
		k, err := c.evalStrKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		code, err := c.optString(args, 1, e)
		if err != nil {
			return value{}, err
		}
		return strKey(csvbase.Require(c.b, k, code)), nil

	case "coalesce":
		if err := arity(head, args, 1, -1); err != nil {
			return value{}, err
		}
		keys, err := c.evalStrKeys(args, e)
		if err != nil {
			return value{}, err
		}
		return strKey(csvbase.Coalesce(c.b, keys...)), nil

	case "or-else":
		if err := arity(head, args, 2, 2); err != nil {
			return value{}, err
		}
		primary, err := c.evalStrKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		fallback, err := c.evalStrKey(args[1], e)
		if err != nil {
			return value{}, err
		}
		return strKey(csvbase.Else(c.b, primary, fallback)), nil

	case "join-keys":
		if err := arity(head, args, 2, -1); err != nil {
			return value{}, err
		}
		sep, err := c.evalString(args[0], e)
		if err != nil {
			return value{}, err
		}
		keys, err := c.evalStrKeys(args[1:], e)
		if err != nil {
			return value{}, err
		}
		return strKey(csvbase.JoinKeys(c.b, sep, keys...)), nil

	case "map-value":
		if err := arity(head, args, 3, 4); err != nil {
			return value{}, err
		}
		k, err := c.evalStrKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		dict, err := c.evalDict(args[1], e)
		if err != nil {
			return value{}, err
		}
		mode, err := c.evalMapMode(args[2], e)
		if err != nil {
			return value{}, err
		}
		code, err := c.optString(args, 3, e)
		if err != nil {
			return value{}, err
		}
		return strKey(csvbase.MapValue(c.b, k, dict, mode, code)), nil

	case "diag-as-warning":
		if err := arity(head, args, 2, 2); err != nil {
			return value{}, err
		}
		k, err := c.evalStrKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		code, err := c.evalString(args[1], e)
		if err != nil {
			return value{}, err
		}
		return strKey(csvbase.DiagAsWarning(c.b, k, code)), nil

	case "parse-date":
		if err := arity(head, args, 2, 3); err != nil {
			return value{}, err
		}
		k, err := c.evalStrKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		layout, err := c.evalString(args[1], e)
		if err != nil {
			return value{}, err
		}
		code, err := c.optString(args, 2, e)
		if err != nil {
			return value{}, err
		}
		return value{kind: kindDateKey, v: csvbase.ParseDate(c.b, k, layout, code)}, nil

	case "parse-amount":
		if err := arity(head, args, 1, -1); err != nil {
			return value{}, err
		}
		src, err := c.evalStrKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		kw, err := trailingKwargs(args[1:])
		if err != nil {
			return value{}, err
		}
		cfg := csvbase.ParseAmountConfig{Format: c.numFmt}
		if fn, ok := kw["format"]; ok {
			nf, err := c.evalNumberFormat(fn, e)
			if err != nil {
				return value{}, err
			}
			cfg.Format = nf
		}
		if sn, ok := kw["split-currency"]; ok {
			b, err := c.evalBool(sn, e)
			if err != nil {
				return value{}, err
			}
			cfg.SplitCurrency = b
		}
		if cn, ok := kw["code"]; ok {
			code, err := c.evalString(cn, e)
			if err != nil {
				return value{}, err
			}
			cfg.Code = code
		}
		return value{kind: kindAmtKey, v: csvbase.ParseAmount(c.b, src, cfg)}, nil

	case "negate-amount":
		if err := arity(head, args, 1, 1); err != nil {
			return value{}, err
		}
		a, err := c.evalAmtKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		return value{kind: kindAmtKey, v: csvbase.NegateAmount(c.b, a)}, nil

	case "add-amounts":
		if err := arity(head, args, 2, 3); err != nil {
			return value{}, err
		}
		lhs, err := c.evalAmtKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		rhs, err := c.evalAmtKey(args[1], e)
		if err != nil {
			return value{}, err
		}
		code, err := c.optString(args, 2, e)
		if err != nil {
			return value{}, err
		}
		return value{kind: kindAmtKey, v: csvbase.AddAmounts(c.b, lhs, rhs, code)}, nil

	case "currency-hint":
		if err := arity(head, args, 1, 1); err != nil {
			return value{}, err
		}
		a, err := c.evalAmtKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		return strKey(csvbase.CurrencyHint(c.b, a)), nil

	case "split":
		if err := arity(head, args, 2, 2); err != nil {
			return value{}, err
		}
		k, err := c.evalStrKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		re, err := c.evalRegex(args[1], e)
		if err != nil {
			return value{}, err
		}
		return value{kind: kindRowKey, v: csvbase.Split(c.b, k, re)}, nil

	case "group":
		if err := arity(head, args, 2, 2); err != nil {
			return value{}, err
		}
		split, err := c.evalRowKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		name, err := c.evalString(args[1], e)
		if err != nil {
			return value{}, err
		}
		return strKey(csvbase.Group(c.b, split, name)), nil

	case "merge":
		if err := arity(head, args, 2, 2); err != nil {
			return value{}, err
		}
		base, err := c.evalRowKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		over, err := c.evalRowBindings(args[1], e)
		if err != nil {
			return value{}, err
		}
		return value{kind: kindRowKey, v: csvbase.Merge(c.b, base, over)}, nil

	case "bindings":
		m := map[string]csvbase.Key[string]{}
		for _, pair := range args {
			if pair.kind != nodeList || len(pair.items) != 2 {
				return value{}, fmt.Errorf("csvsexp: line %d: each binding must be (\"name\" key)", pair.line)
			}
			name, err := c.evalString(pair.items[0], e)
			if err != nil {
				return value{}, err
			}
			k, err := c.evalStrKey(pair.items[1], e)
			if err != nil {
				return value{}, err
			}
			m[name] = k
		}
		return value{kind: kindRowBindings, v: m}, nil

	case "template":
		if err := arity(head, args, 2, 2); err != nil {
			return value{}, err
		}
		src, err := c.evalString(args[0], e)
		if err != nil {
			return value{}, err
		}
		data, err := c.evalRowKey(args[1], e)
		if err != nil {
			return value{}, err
		}
		tmpl, err := csvkit.CompileTemplate(src)
		if err != nil {
			return value{}, errLine(args[0], err)
		}
		return strKey(csvbase.Template(c.b, tmpl, data)), nil

	case "regex":
		if err := arity(head, args, 1, 1); err != nil {
			return value{}, err
		}
		pat, err := c.evalString(args[0], e)
		if err != nil {
			return value{}, err
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return value{}, errLine(args[0], err)
		}
		return value{kind: kindRegex, v: re}, nil

	case "dict":
		m := map[string]string{}
		for _, pair := range args {
			if pair.kind != nodeList || len(pair.items) != 2 {
				return value{}, fmt.Errorf("csvsexp: line %d: each dict entry must be (\"key\" \"value\")", pair.line)
			}
			k, err := c.evalString(pair.items[0], e)
			if err != nil {
				return value{}, err
			}
			val, err := c.evalString(pair.items[1], e)
			if err != nil {
				return value{}, err
			}
			m[k] = val
		}
		return value{kind: kindDict, v: m}, nil

	case "number-format":
		nf, err := c.evalNumberFormatForm(args, e)
		if err != nil {
			return value{}, err
		}
		return value{kind: kindNumberFormat, v: nf}, nil

	case "cost":
		return c.compileCost(args, e)

	case "sub-amounts":
		if err := arity(head, args, 2, 3); err != nil {
			return value{}, err
		}
		lhs, err := c.evalAmtKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		rhs, err := c.evalAmtKey(args[1], e)
		if err != nil {
			return value{}, err
		}
		code, err := c.optString(args, 2, e)
		if err != nil {
			return value{}, err
		}
		return value{kind: kindAmtKey, v: csvbase.SubAmounts(c.b, lhs, rhs, code)}, nil

	case "abs-amount":
		if err := arity(head, args, 1, 1); err != nil {
			return value{}, err
		}
		a, err := c.evalAmtKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		return value{kind: kindAmtKey, v: csvbase.AbsAmount(c.b, a)}, nil

	case "empty?":
		if err := arity(head, args, 1, 1); err != nil {
			return value{}, err
		}
		k, err := c.evalStrKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		return boolKey(csvbase.IsBlank(c.b, k)), nil

	case "equal?":
		if err := arity(head, args, 2, 2); err != nil {
			return value{}, err
		}
		lhs, err := c.evalStrKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		rhs, err := c.evalStrKey(args[1], e)
		if err != nil {
			return value{}, err
		}
		return boolKey(csvbase.StrEqual(c.b, lhs, rhs)), nil

	case "matches?":
		if err := arity(head, args, 2, 2); err != nil {
			return value{}, err
		}
		k, err := c.evalStrKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		re, err := c.evalRegex(args[1], e)
		if err != nil {
			return value{}, err
		}
		return boolKey(csvbase.MatchRegexp(c.b, k, re)), nil

	case "and":
		if err := arity(head, args, 1, -1); err != nil {
			return value{}, err
		}
		ks, err := c.evalBoolKeys(args, e)
		if err != nil {
			return value{}, err
		}
		return boolKey(csvbase.And(c.b, ks...)), nil

	case "or":
		if err := arity(head, args, 1, -1); err != nil {
			return value{}, err
		}
		ks, err := c.evalBoolKeys(args, e)
		if err != nil {
			return value{}, err
		}
		return boolKey(csvbase.Or(c.b, ks...)), nil

	case "not":
		if err := arity(head, args, 1, 1); err != nil {
			return value{}, err
		}
		k, err := c.evalBoolKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		return boolKey(csvbase.Not(c.b, k)), nil

	case "negative?", "positive?", "zero?":
		if err := arity(head, args, 1, 1); err != nil {
			return value{}, err
		}
		a, err := c.evalAmtKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		switch head.text {
		case "negative?":
			return boolKey(csvbase.IsNegative(c.b, a)), nil
		case "positive?":
			return boolKey(csvbase.IsPositive(c.b, a)), nil
		case "zero?":
			return boolKey(csvbase.IsZero(c.b, a)), nil
		default:
			panic("unreachable")
		}

	case "amount<?", "amount>?", "amount=?":
		if err := arity(head, args, 2, 3); err != nil {
			return value{}, err
		}
		lhs, err := c.evalAmtKey(args[0], e)
		if err != nil {
			return value{}, err
		}
		rhs, err := c.evalAmtKey(args[1], e)
		if err != nil {
			return value{}, err
		}
		code, err := c.optString(args, 2, e)
		if err != nil {
			return value{}, err
		}
		switch head.text {
		case "amount<?":
			return boolKey(csvbase.AmountLess(c.b, lhs, rhs, code)), nil
		case "amount>?":
			return boolKey(csvbase.AmountGreater(c.b, lhs, rhs, code)), nil
		case "amount=?":
			return boolKey(csvbase.AmountEqual(c.b, lhs, rhs, code)), nil
		default:
			panic("unreachable")
		}

	case "if":
		return c.compileIf(head, args, e)

	case "lambda":
		if err := arity(head, args, 2, 2); err != nil {
			return value{}, err
		}
		plist := args[0]
		if plist.kind != nodeList {
			return value{}, fmt.Errorf("csvsexp: line %d: lambda parameters must be a list", plist.line)
		}
		params := make([]string, len(plist.items))
		for i, p := range plist.items {
			if p.kind != nodeSymbol {
				return value{}, fmt.Errorf("csvsexp: line %d: lambda parameter must be a symbol", p.line)
			}
			params[i] = p.text
		}
		return value{kind: kindFunction, v: funcValue{params: params, body: args[1], closure: e}}, nil

	case "let*", "emit-transaction":
		return value{}, fmt.Errorf("csvsexp: line %d: %s is only valid as the csv-import body", n.line, head.text)

	default:
		return value{}, fmt.Errorf("csvsexp: line %d: unknown form %q", n.line, head.text)
	}
}

func (c *compiler) compileEmit(n node, e *env) (csvbase.EmitFunc, error) {
	kw, err := trailingKwargs(n.items[1:])
	if err != nil {
		return nil, err
	}
	var cfg csvbase.TxConfig

	dn, ok := kw["date"]
	if !ok {
		return nil, fmt.Errorf("csvsexp: line %d: emit-transaction requires :date", n.line)
	}
	if cfg.Date, err = c.evalDateKey(dn, e); err != nil {
		return nil, err
	}
	an, ok := kw["amount"]
	if !ok {
		return nil, fmt.Errorf("csvsexp: line %d: emit-transaction requires :amount", n.line)
	}
	if cfg.Amount, err = c.evalAmtKey(an, e); err != nil {
		return nil, err
	}

	for _, f := range []struct {
		name string
		dst  *csvbase.Key[string]
	}{
		{"currency", &cfg.Currency},
		{"account", &cfg.Account},
		{"counter", &cfg.Counter},
		{"payee", &cfg.Payee},
		{"narration", &cfg.Narration},
	} {
		vn, ok := kw[f.name]
		if !ok {
			continue
		}
		k, err := c.evalStrKey(vn, e)
		if err != nil {
			return nil, err
		}
		*f.dst = k
	}

	if cn, ok := kw["cost"]; ok {
		if cfg.Cost, err = c.evalCostKey(cn, e); err != nil {
			return nil, err
		}
	}

	if fn, ok := kw["flag"]; ok {
		s, err := c.evalString(fn, e)
		if err != nil {
			return nil, err
		}
		if len(s) != 1 {
			return nil, fmt.Errorf("csvsexp: line %d: :flag must be a single ASCII character", fn.line)
		}
		cfg.Flag = s[0]
	}

	if tn, ok := kw["tags"]; ok {
		if cfg.Tags, err = c.evalStrKeyList(tn, e); err != nil {
			return nil, err
		}
	}
	if ln, ok := kw["links"]; ok {
		if cfg.Links, err = c.evalStrKeyList(ln, e); err != nil {
			return nil, err
		}
	}
	if mn, ok := kw["meta"]; ok {
		if cfg.Meta, err = c.evalMetaFields(mn, e); err != nil {
			return nil, err
		}
	}

	for name, dst := range map[string]*string{
		"missing-amount-code":   &cfg.MissingAmountCode,
		"missing-currency-code": &cfg.MissingCurrencyCode,
		"missing-account-code":  &cfg.MissingAccountCode,
	} {
		if vn, ok := kw[name]; ok {
			s, err := c.evalString(vn, e)
			if err != nil {
				return nil, err
			}
			*dst = s
		}
	}

	return csvbase.EmitTransaction(cfg), nil
}

func (c *compiler) evalStrKeyList(n node, e *env) ([]csvbase.Key[string], error) {
	if n.kind != nodeList {
		return nil, fmt.Errorf("csvsexp: line %d: expected a list of keys", n.line)
	}
	out := make([]csvbase.Key[string], 0, len(n.items))
	for _, it := range n.items {
		k, err := c.evalStrKey(it, e)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

func (c *compiler) evalMetaFields(n node, e *env) ([]csvbase.MetaField, error) {
	if n.kind != nodeList {
		return nil, fmt.Errorf("csvsexp: line %d: :meta expects a list of (\"name\" key) pairs", n.line)
	}
	out := make([]csvbase.MetaField, 0, len(n.items))
	for _, pair := range n.items {
		if pair.kind != nodeList || len(pair.items) != 2 {
			return nil, fmt.Errorf("csvsexp: line %d: each meta entry must be (\"name\" key)", pair.line)
		}
		name, err := c.evalString(pair.items[0], e)
		if err != nil {
			return nil, err
		}
		k, err := c.evalStrKey(pair.items[1], e)
		if err != nil {
			return nil, err
		}
		out = append(out, csvbase.MetaField{Name: name, Value: k})
	}
	return out, nil
}

// compileCost replicates csvimp's cost resolution: a blank cost-number cell
// yields no cost, while an unparseable number, an unresolved cost currency, or
// an unparseable date soft-fails with DiagBadCost.
func (c *compiler) compileCost(args []node, e *env) (value, error) {
	kw, err := trailingKwargs(args)
	if err != nil {
		return value{}, err
	}
	perUnit, hasPU := kw["per-unit"]
	total, hasTotal := kw["total"]
	if hasPU == hasTotal {
		return value{}, fmt.Errorf("csvsexp: cost requires exactly one of :per-unit or :total")
	}

	var numKey csvbase.Key[string]
	isTotal := false
	if hasPU {
		if numKey, err = c.evalStrKey(perUnit, e); err != nil {
			return value{}, err
		}
	} else {
		if numKey, err = c.evalStrKey(total, e); err != nil {
			return value{}, err
		}
		isTotal = true
	}

	numFmt := c.numFmt
	if nfNode, ok := kw["number"]; ok {
		if numFmt, err = c.evalNumberFormat(nfNode, e); err != nil {
			return value{}, err
		}
	}

	defaultCur := ""
	if dc, ok := kw["default-currency"]; ok {
		if defaultCur, err = c.evalString(dc, e); err != nil {
			return value{}, err
		}
	}
	var curKey csvbase.Key[string]
	hasCur := false
	if cn, ok := kw["currency"]; ok {
		if curKey, err = c.evalStrKey(cn, e); err != nil {
			return value{}, err
		}
		hasCur = true
	}
	if !hasCur && defaultCur == "" {
		return value{}, fmt.Errorf("csvsexp: cost requires :currency or :default-currency")
	}

	var dateKey csvbase.Key[string]
	hasDate := false
	dateFormat := ""
	if dn, ok := kw["date"]; ok {
		if dateKey, err = c.evalStrKey(dn, e); err != nil {
			return value{}, err
		}
		hasDate = true
	}
	if df, ok := kw["date-format"]; ok {
		if dateFormat, err = c.evalString(df, e); err != nil {
			return value{}, err
		}
	}
	if hasDate && dateFormat == "" {
		return value{}, fmt.Errorf("csvsexp: cost :date requires :date-format")
	}

	var labelKey csvbase.Key[string]
	hasLabel := false
	if ln, ok := kw["label"]; ok {
		if labelKey, err = c.evalStrKey(ln, e); err != nil {
			return value{}, err
		}
		hasLabel = true
	}

	costK := csvbase.AddStep(c.b, func(ms *csvbase.MappingState) (*ast.CostSpec, *ast.Diagnostic, error) {
		info := ms.Info()
		raw, d := csvbase.Value(ms, numKey)
		if d != nil {
			return nil, d, nil
		}
		num, blank, err := csvkit.ParseNumber(raw, numFmt)
		if blank {
			return nil, nil, nil
		}
		if err != nil {
			diag := csvbase.ErrorDiag(csvbase.DiagBadCost, info.Path, info.Line,
				fmt.Sprintf("cannot parse cost number %q", raw))
			return nil, &diag, nil
		}
		cur := defaultCur
		if hasCur {
			if v, _ := csvbase.Value(ms, curKey); strings.TrimSpace(v) != "" {
				cur = strings.TrimSpace(v)
			}
		}
		if cur == "" {
			diag := csvbase.ErrorDiag(csvbase.DiagBadCost, info.Path, info.Line,
				"cost has no currency")
			return nil, &diag, nil
		}
		label := ""
		if hasLabel {
			if v, _ := csvbase.Value(ms, labelKey); strings.TrimSpace(v) != "" {
				label = strings.TrimSpace(v)
			}
		}
		cs := &ast.CostSpec{Currency: cur, Label: label}
		n := num
		if isTotal {
			cs.Total = &n
		} else {
			cs.PerUnit = &n
		}
		if hasDate {
			if v, _ := csvbase.Value(ms, dateKey); strings.TrimSpace(v) != "" {
				dv := strings.TrimSpace(v)
				t, err := time.Parse(dateFormat, dv)
				if err != nil {
					diag := csvbase.ErrorDiag(csvbase.DiagBadCost, info.Path, info.Line,
						fmt.Sprintf("cannot parse cost date %q with format %q: %v", dv, dateFormat, err))
					return nil, &diag, nil
				}
				cs.Date = &t
			}
		}
		return cs, nil, nil
	})
	return value{kind: kindCostKey, v: costK}, nil
}

// evalNumberFormat evaluates n, which must be a (number-format ...) form.
func (c *compiler) evalNumberFormat(n node, e *env) (csvkit.NumberFormat, error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return csvkit.NumberFormat{}, err
	}
	nf, err := asNumberFormat(v)
	if err != nil {
		return csvkit.NumberFormat{}, errLine(n, err)
	}
	return nf, nil
}

func (c *compiler) evalNumberFormatForm(args []node, e *env) (csvkit.NumberFormat, error) {
	kw, err := trailingKwargs(args)
	if err != nil {
		return csvkit.NumberFormat{}, err
	}
	var nf csvkit.NumberFormat
	if tn, ok := kw["thousands-sep"]; ok {
		if nf.ThousandsSep, err = c.evalString(tn, e); err != nil {
			return csvkit.NumberFormat{}, err
		}
	}
	if dn, ok := kw["decimal-sep"]; ok {
		if nf.DecimalSep, err = c.evalString(dn, e); err != nil {
			return csvkit.NumberFormat{}, err
		}
	}
	if pn, ok := kw["placeholders"]; ok {
		if pn.kind != nodeList {
			return csvkit.NumberFormat{}, fmt.Errorf("csvsexp: line %d: :placeholders expects a list of strings", pn.line)
		}
		for _, it := range pn.items {
			s, err := c.evalString(it, e)
			if err != nil {
				return csvkit.NumberFormat{}, err
			}
			nf.Placeholders = append(nf.Placeholders, s)
		}
	}
	return nf, nil
}

// applyFunction evaluates a macro-style function: each argument is evaluated in
// the caller's scope e and bound to the corresponding parameter in a child of
// the function's captured closure, then body is evaluated there. Because body
// is re-evaluated per call, each application emits its own pipeline steps.
func (c *compiler) applyFunction(fn funcValue, head node, args []node, e *env) (value, error) {
	if len(args) != len(fn.params) {
		return value{}, fmt.Errorf("csvsexp: line %d: %s expects %d argument(s), got %d", head.line, head.text, len(fn.params), len(args))
	}
	callScope := newEnv(fn.closure)
	for i, p := range fn.params {
		v, err := c.evalExpr(args[i], e)
		if err != nil {
			return value{}, err
		}
		callScope.bind(p, v)
	}
	return c.evalExpr(fn.body, callScope)
}

// compileIf builds a per-row conditional. A literal condition folds at compile
// time; otherwise the branches must share one runtime key kind, which selects
// the typed csvbase.If instantiation.
func (c *compiler) compileIf(head node, args []node, e *env) (value, error) {
	if err := arity(head, args, 3, 3); err != nil {
		return value{}, err
	}
	condV, err := c.evalExpr(args[0], e)
	if err != nil {
		return value{}, err
	}
	if condV.kind == kindBoolLit {
		if condV.v.(bool) {
			return c.evalExpr(args[1], e)
		}
		return c.evalExpr(args[2], e)
	}
	cond, err := asBoolKey(condV)
	if err != nil {
		return value{}, errLine(args[0], err)
	}
	thenV, err := c.evalExpr(args[1], e)
	if err != nil {
		return value{}, err
	}
	elseV, err := c.evalExpr(args[2], e)
	if err != nil {
		return value{}, err
	}
	if thenV.kind != elseV.kind {
		return value{}, fmt.Errorf("csvsexp: line %d: if branches must have the same type, got %s and %s", head.line, thenV.kind, elseV.kind)
	}
	switch thenV.kind {
	case kindStrKey:
		return strKey(csvbase.If(c.b, cond, thenV.v.(csvbase.Key[string]), elseV.v.(csvbase.Key[string]))), nil
	case kindDateKey:
		return value{kind: kindDateKey, v: csvbase.If(c.b, cond, thenV.v.(csvbase.Key[time.Time]), elseV.v.(csvbase.Key[time.Time]))}, nil
	case kindAmtKey:
		return value{kind: kindAmtKey, v: csvbase.If(c.b, cond, thenV.v.(csvbase.Key[*csvkit.Amount]), elseV.v.(csvbase.Key[*csvkit.Amount]))}, nil
	case kindRowKey:
		return value{kind: kindRowKey, v: csvbase.If(c.b, cond, thenV.v.(csvbase.Key[map[string]string]), elseV.v.(csvbase.Key[map[string]string]))}, nil
	case kindCostKey:
		return value{kind: kindCostKey, v: csvbase.If(c.b, cond, thenV.v.(csvbase.Key[*ast.CostSpec]), elseV.v.(csvbase.Key[*ast.CostSpec]))}, nil
	case kindBoolKey:
		return boolKey(csvbase.If(c.b, cond, thenV.v.(csvbase.Key[bool]), elseV.v.(csvbase.Key[bool]))), nil
	default:
		return value{}, fmt.Errorf("csvsexp: line %d: if branches must be runtime keys, got %s", head.line, thenV.kind)
	}
}

func (c *compiler) evalStrKey(n node, e *env) (csvbase.Key[string], error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return csvbase.Key[string]{}, err
	}
	k, err := asStrKey(v)
	if err != nil {
		return csvbase.Key[string]{}, errLine(n, err)
	}
	return k, nil
}

func (c *compiler) evalStrKeys(ns []node, e *env) ([]csvbase.Key[string], error) {
	out := make([]csvbase.Key[string], 0, len(ns))
	for _, n := range ns {
		k, err := c.evalStrKey(n, e)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

func (c *compiler) evalBoolKey(n node, e *env) (csvbase.Key[bool], error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return csvbase.Key[bool]{}, err
	}
	k, err := asBoolKey(v)
	if err != nil {
		return csvbase.Key[bool]{}, errLine(n, err)
	}
	return k, nil
}

func (c *compiler) evalBoolKeys(ns []node, e *env) ([]csvbase.Key[bool], error) {
	out := make([]csvbase.Key[bool], 0, len(ns))
	for _, n := range ns {
		k, err := c.evalBoolKey(n, e)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

func (c *compiler) evalDateKey(n node, e *env) (csvbase.Key[time.Time], error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return csvbase.Key[time.Time]{}, err
	}
	k, err := asDateKey(v)
	if err != nil {
		return csvbase.Key[time.Time]{}, errLine(n, err)
	}
	return k, nil
}

func (c *compiler) evalAmtKey(n node, e *env) (csvbase.Key[*csvkit.Amount], error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return csvbase.Key[*csvkit.Amount]{}, err
	}
	k, err := asAmtKey(v)
	if err != nil {
		return csvbase.Key[*csvkit.Amount]{}, errLine(n, err)
	}
	return k, nil
}

func (c *compiler) evalRowKey(n node, e *env) (csvbase.Key[map[string]string], error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return csvbase.Key[map[string]string]{}, err
	}
	k, err := asRowKey(v)
	if err != nil {
		return csvbase.Key[map[string]string]{}, errLine(n, err)
	}
	return k, nil
}

func (c *compiler) evalCostKey(n node, e *env) (csvbase.Key[*ast.CostSpec], error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return csvbase.Key[*ast.CostSpec]{}, err
	}
	k, err := asCostKey(v)
	if err != nil {
		return csvbase.Key[*ast.CostSpec]{}, errLine(n, err)
	}
	return k, nil
}

func (c *compiler) evalString(n node, e *env) (string, error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return "", err
	}
	s, err := asString(v)
	if err != nil {
		return "", errLine(n, err)
	}
	return s, nil
}

func (c *compiler) evalBool(n node, e *env) (bool, error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return false, err
	}
	if v.kind != kindBoolLit {
		return false, errLine(n, fmt.Errorf("expected %s, got %s", kindBoolLit, v.kind))
	}
	return v.v.(bool), nil
}

func (c *compiler) evalRegex(n node, e *env) (*regexp.Regexp, error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return nil, err
	}
	re, err := asRegex(v)
	if err != nil {
		return nil, errLine(n, err)
	}
	return re, nil
}

func (c *compiler) evalDict(n node, e *env) (map[string]string, error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return nil, err
	}
	m, err := asDict(v)
	if err != nil {
		return nil, errLine(n, err)
	}
	return m, nil
}

func (c *compiler) evalMapMode(n node, e *env) (csvkit.MapMode, error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return csvkit.Verbatim, err
	}
	m, err := asMapMode(v)
	if err != nil {
		return csvkit.Verbatim, errLine(n, err)
	}
	return m, nil
}

func (c *compiler) evalRowBindings(n node, e *env) (map[string]csvbase.Key[string], error) {
	v, err := c.evalExpr(n, e)
	if err != nil {
		return nil, err
	}
	m, err := asRowBindings(v)
	if err != nil {
		return nil, errLine(n, err)
	}
	return m, nil
}

// optString evaluates args[i] as a string literal, returning "" when args has
// no element at i.
func (c *compiler) optString(args []node, i int, e *env) (string, error) {
	if i >= len(args) {
		return "", nil
	}
	return c.evalString(args[i], e)
}

func strKey(k csvbase.Key[string]) value { return value{kind: kindStrKey, v: k} }

func boolKey(k csvbase.Key[bool]) value { return value{kind: kindBoolKey, v: k} }

func errLine(n node, err error) error {
	return fmt.Errorf("csvsexp: line %d: %w", n.line, err)
}

func arity(head node, args []node, min, max int) error {
	if len(args) < min || (max >= 0 && len(args) > max) {
		want := fmt.Sprintf("at least %d", min)
		if max == min {
			want = fmt.Sprintf("exactly %d", min)
		} else if max >= 0 {
			want = fmt.Sprintf("%d to %d", min, max)
		}
		noun := "arguments"
		if min == 1 && max == 1 {
			noun = "argument"
		}
		return fmt.Errorf("csvsexp: line %d: %s expects %s %s, got %d", head.line, head.text, want, noun, len(args))
	}
	return nil
}

// trailingKwargs parses items as alternating keyword/value pairs.
func trailingKwargs(items []node) (map[string]node, error) {
	kw := map[string]node{}
	for i := 0; i < len(items); {
		if items[i].kind != nodeKeyword {
			return nil, fmt.Errorf("csvsexp: line %d: expected a keyword argument", items[i].line)
		}
		if i+1 >= len(items) {
			return nil, fmt.Errorf("csvsexp: line %d: keyword :%s has no value", items[i].line, items[i].text)
		}
		kw[items[i].text] = items[i+1]
		i += 2
	}
	return kw, nil
}

func nodeText(n node) (string, error) {
	if n.kind != nodeString {
		return "", fmt.Errorf("csvsexp: line %d: expected a string", n.line)
	}
	return n.text, nil
}

// buildReader constructs a csvkit.Reader from the csv-import keyword arguments,
// erroring on malformed :delimiter, :encoding, :skip-lines, or :columns values.
func buildReader(kw map[string]node) (csvkit.Reader, error) {
	r := csvkit.Reader{Delimiter: ',', LazyQuotes: true}
	if dn, ok := kw["delimiter"]; ok {
		s, err := nodeText(dn)
		if err != nil {
			return r, err
		}
		ru, size := utf8.DecodeRuneInString(s)
		if ru == utf8.RuneError || size != len(s) {
			return r, fmt.Errorf("csvsexp: line %d: :delimiter %q must be exactly one rune", dn.line, s)
		}
		r.Delimiter = ru
	}
	if en, ok := kw["encoding"]; ok {
		s, err := nodeText(en)
		if err != nil {
			return r, err
		}
		enc, err := ianaindex.IANA.Encoding(s)
		if err != nil || enc == nil {
			return r, fmt.Errorf("csvsexp: line %d: :encoding %q is not a recognised IANA charset name", en.line, s)
		}
		r.Encoding = enc
	}
	if sn, ok := kw["skip-lines"]; ok {
		if sn.kind != nodeInt {
			return r, fmt.Errorf("csvsexp: line %d: :skip-lines must be an integer", sn.line)
		}
		r.SkipLines = int(sn.num)
	}
	if hn, ok := kw["header-match"]; ok {
		names, err := stringListNode(hn)
		if err != nil {
			return r, err
		}
		r.HeaderMatch = headerMatcher(names)
	}
	if cn, ok := kw["columns"]; ok {
		if cn.kind != nodeList {
			return r, fmt.Errorf("csvsexp: line %d: :columns expects a list of (\"name\" index) pairs", cn.line)
		}
		cols := map[string]int{}
		for _, pair := range cn.items {
			if pair.kind != nodeList || len(pair.items) != 2 ||
				pair.items[0].kind != nodeString || pair.items[1].kind != nodeInt {
				return r, fmt.Errorf("csvsexp: line %d: each column must be (\"name\" index)", pair.line)
			}
			idx := int(pair.items[1].num)
			if idx < 0 {
				return r, fmt.Errorf("csvsexp: line %d: column index must be non-negative", pair.line)
			}
			cols[pair.items[0].text] = idx
		}
		r.Columns = cols
	}
	return r, nil
}

// buildGate returns the path gate for the importer: the default gate alone, or
// combined with a :match path regexp when one is given.
func buildGate(kw map[string]node) (csvbase.Gate, error) {
	if mn, ok := kw["match"]; ok {
		s, err := nodeText(mn)
		if err != nil {
			return nil, err
		}
		re, err := regexp.Compile(s)
		if err != nil {
			return nil, errLine(mn, err)
		}
		return csvbase.AllGates(csvbase.DefaultGate, csvbase.PathMatch(re)), nil
	}
	return csvbase.DefaultGate, nil
}

// buildFilters compiles the :exclude rules into row filters, erroring on any
// rule that is not a well-formed (exclude :match ... [:col ...]) form.
func buildFilters(kw map[string]node) ([]csvkit.RowFilter, error) {
	en, ok := kw["exclude"]
	if !ok {
		return nil, nil
	}
	if en.kind != nodeList {
		return nil, fmt.Errorf("csvsexp: line %d: :exclude expects a list of (exclude ...) forms", en.line)
	}
	var filters []csvkit.RowFilter
	for _, ex := range en.items {
		if ex.kind != nodeList || len(ex.items) == 0 ||
			ex.items[0].kind != nodeSymbol || ex.items[0].text != "exclude" {
			return nil, fmt.Errorf("csvsexp: line %d: each exclude rule must be (exclude :match ... [:col ...])", ex.line)
		}
		kwx, err := trailingKwargs(ex.items[1:])
		if err != nil {
			return nil, err
		}
		mn, ok := kwx["match"]
		if !ok {
			return nil, fmt.Errorf("csvsexp: line %d: exclude requires :match", ex.line)
		}
		pat, err := nodeText(mn)
		if err != nil {
			return nil, err
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, errLine(mn, err)
		}
		col := ""
		if cn, ok := kwx["col"]; ok {
			if col, err = nodeText(cn); err != nil {
				return nil, err
			}
		}
		if col != "" {
			filters = append(filters, csvkit.ExcludeMatching(col, re))
		} else {
			filters = append(filters, csvkit.ExcludeAnyField(re))
		}
	}
	return filters, nil
}

func stringListNode(n node) ([]string, error) {
	if n.kind != nodeList {
		return nil, fmt.Errorf("csvsexp: line %d: expected a list of strings", n.line)
	}
	out := make([]string, 0, len(n.items))
	for _, it := range n.items {
		if it.kind != nodeString {
			return nil, fmt.Errorf("csvsexp: line %d: expected a string", it.line)
		}
		out = append(out, it.text)
	}
	return out, nil
}

// headerMatcher accepts any row that contains every name in required, compared
// after trimming.
func headerMatcher(required []string) func([]string) bool {
	return func(row []string) bool {
		present := make(map[string]bool, len(row))
		for _, c := range row {
			present[strings.TrimSpace(c)] = true
		}
		for _, r := range required {
			if !present[r] {
				return false
			}
		}
		return true
	}
}
