package ast

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cockroachdb/apd/v3"
)

const invalidOptionCode = "invalid-option"

// OptionKind classifies how an option's raw value is parsed, stored,
// and serialized. Callers (in particular beancompat) switch on
// OptionKind to dispatch formatting.
type OptionKind int

const (
	KindString OptionKind = iota
	KindBool
	KindDecimal
	KindStringList
	KindInt
	KindDecimalMap
	KindIntMap
)

// spec describes one option's type, parser, and default.
type spec struct {
	key          string
	kind         OptionKind
	parse        func(raw string) (any, error)
	defaultValue any
	// aliasOf: writes redirect to the canonical key; reads return this
	// spec's own registered default.
	aliasOf string
}

// registry holds all known option specs keyed by option name.
type registry struct {
	specs map[string]spec
}

func newRegistry() *registry {
	return &registry{specs: make(map[string]spec)}
}

func (r *registry) register(s spec) error {
	if _, ok := r.specs[s.key]; ok {
		return fmt.Errorf("options: key %q is already registered", s.key)
	}
	if s.aliasOf != "" {
		canonical, ok := r.specs[s.aliasOf]
		if !ok {
			return fmt.Errorf("spec %q aliases %q which is not registered", s.key, s.aliasOf)
		}
		if canonical.aliasOf != "" {
			return fmt.Errorf("spec %q aliases %q which is itself an alias (chains are forbidden)", s.key, s.aliasOf)
		}
	}
	r.specs[s.key] = s
	return nil
}

// OptionValues holds parsed option values for a single ledger.
//
// Construct via [NewOptionValues] or [ParseOptions]; the zero value is not
// usable. Accessor methods share the same contract: they return the registered
// default when a key has not been set, are nil-safe (a nil receiver consults
// the package's default registry), and panic if the key is not registered.
// Non-scalar accessors ([Decimal], [StringList], [DecimalMap], [IntMap])
// return fresh copies; callers may mutate them without affecting stored state.
type OptionValues struct {
	reg    *registry
	values map[string]any
}

// NewOptionValues returns an *OptionValues bound to the default registry
// with no options set. Accessor methods return registry defaults.
func NewOptionValues() *OptionValues {
	return newOptionValues(defaultRegistry)
}

func newOptionValues(reg *registry) *OptionValues {
	return &OptionValues{reg: reg, values: make(map[string]any)}
}

// mapEntry carries one parsed "KEY:value" pair from a map-kind option directive.
type mapEntry[V any] struct {
	subKey string
	value  V
}

// set silently ignores unknown keys; unknown is not an error.
// KindStringList items accumulate; KindDecimalMap and KindIntMap merge per
// sub-key (last-write-wins); scalar values use last-write-wins.
// Writes to a deprecated alias key are redirected to the canonical key's
// storage slot; the deprecated slot is never written to. Mirrors
// upstream beancount's grammar.py:393-396 write redirect.
func (v *OptionValues) set(key, raw string) error {
	s, ok := v.reg.specs[key]
	if !ok {
		return nil
	}
	target := key
	if s.aliasOf != "" {
		target = s.aliasOf
	}
	parsed, err := s.parse(raw)
	if err != nil {
		return err
	}
	switch s.kind {
	case KindStringList:
		str, ok := parsed.(string)
		if !ok {
			return fmt.Errorf("string list parser returned %T, want string", parsed)
		}
		existing, _ := v.values[target].([]string)
		v.values[target] = append(existing, str)
	case KindDecimalMap:
		entry, ok := parsed.(mapEntry[*apd.Decimal])
		if !ok {
			return fmt.Errorf("decimal-map parser returned %T, want mapEntry[*apd.Decimal]", parsed)
		}
		m, _ := v.values[target].(map[string]*apd.Decimal)
		if m == nil {
			m = make(map[string]*apd.Decimal)
		}
		m[entry.subKey] = entry.value
		v.values[target] = m
	case KindIntMap:
		entry, ok := parsed.(mapEntry[int])
		if !ok {
			return fmt.Errorf("int-map parser returned %T, want mapEntry[int]", parsed)
		}
		m, _ := v.values[target].(map[string]int)
		if m == nil {
			m = make(map[string]int)
		}
		m[entry.subKey] = entry.value
		v.values[target] = m
	default:
		v.values[target] = parsed
	}
	return nil
}

func (v *OptionValues) lookupSpec(key string) spec {
	var reg *registry
	if v != nil {
		reg = v.reg
	} else {
		reg = defaultRegistry
	}
	s, ok := reg.specs[key]
	if !ok {
		panic(fmt.Sprintf("options: key %q is not registered", key))
	}
	return s
}

// String returns the string value for key.
func (v *OptionValues) String(key string) string {
	s := v.lookupSpec(key)
	if v != nil {
		if val, ok := v.values[key]; ok {
			return val.(string)
		}
	}
	if s.defaultValue == nil {
		return ""
	}
	return s.defaultValue.(string)
}

// Bool returns the bool value for key.
func (v *OptionValues) Bool(key string) bool {
	s := v.lookupSpec(key)
	if v != nil {
		if val, ok := v.values[key]; ok {
			return val.(bool)
		}
	}
	if s.defaultValue == nil {
		return false
	}
	return s.defaultValue.(bool)
}

// Decimal returns a fresh clone of the decimal value for key.
func (v *OptionValues) Decimal(key string) *apd.Decimal {
	s := v.lookupSpec(key)
	var src *apd.Decimal
	if v != nil {
		if val, ok := v.values[key]; ok {
			src = val.(*apd.Decimal)
		}
	}
	if src == nil && s.defaultValue != nil {
		src = s.defaultValue.(*apd.Decimal)
	}
	if src == nil {
		return nil
	}
	out := new(apd.Decimal)
	out.Set(src)
	return out
}

// StringList returns a fresh clone of the accumulated string list for key.
func (v *OptionValues) StringList(key string) []string {
	s := v.lookupSpec(key)
	var src []string
	if v != nil {
		if val, ok := v.values[key]; ok {
			src = val.([]string)
		}
	}
	if src == nil && s.defaultValue != nil {
		src = s.defaultValue.([]string)
	}
	if src == nil {
		return nil
	}
	return append([]string(nil), src...)
}

// Int returns the integer value for key.
func (v *OptionValues) Int(key string) int {
	s := v.lookupSpec(key)
	if v != nil {
		if val, ok := v.values[key]; ok {
			return val.(int)
		}
	}
	if s.defaultValue == nil {
		return 0
	}
	return s.defaultValue.(int)
}

// DecimalMap returns a fresh map keyed by the option's sub-key. The
// returned map and every *apd.Decimal value are fresh copies; callers
// may mutate them without affecting stored state. Returns an empty
// (non-nil) map when nothing has been set and the registered default
// is empty.
func (v *OptionValues) DecimalMap(key string) map[string]*apd.Decimal {
	s := v.lookupSpec(key)
	var src map[string]*apd.Decimal
	if v != nil {
		if val, ok := v.values[key]; ok {
			src = val.(map[string]*apd.Decimal)
		}
	}
	if src == nil && s.defaultValue != nil {
		src = s.defaultValue.(map[string]*apd.Decimal)
	}
	out := make(map[string]*apd.Decimal, len(src))
	for k, d := range src {
		clone := new(apd.Decimal)
		clone.Set(d)
		out[k] = clone
	}
	return out
}

// IntMap returns a fresh map keyed by the option's sub-key. The
// returned map is a fresh copy; callers may mutate it without
// affecting stored state. Returns an empty (non-nil) map when nothing
// has been set and the registered default is empty.
func (v *OptionValues) IntMap(key string) map[string]int {
	s := v.lookupSpec(key)
	var src map[string]int
	if v != nil {
		if val, ok := v.values[key]; ok {
			src = val.(map[string]int)
		}
	}
	if src == nil && s.defaultValue != nil {
		src = s.defaultValue.(map[string]int)
	}
	out := make(map[string]int, len(src))
	for k, val := range src {
		out[k] = val
	}
	return out
}

// OptionEntry is one option's snapshot at the time Snapshot was called.
// The Kind field tells the caller which typed accessor method returns
// a meaningful value; all other accessors return the zero value for
// their type.
//
// Map and slice accessors return fresh copies; mutating them does not
// affect the OptionValues the entry came from.
type OptionEntry struct {
	Key   string
	Kind  OptionKind
	value any
}

// String returns the stored string-kind value, or "" when Kind != KindString.
//
// Note: this method does not follow the fmt.Stringer convention of returning
// a human display form; it returns the raw stored value. OptionEntry should
// not be passed to fmt-family formatters directly.
func (e OptionEntry) String() string {
	if e.Kind != KindString {
		return ""
	}
	if e.value == nil {
		return ""
	}
	return e.value.(string)
}

// Bool returns the stored bool value, or false when Kind != KindBool.
func (e OptionEntry) Bool() bool {
	if e.Kind != KindBool {
		return false
	}
	if e.value == nil {
		return false
	}
	return e.value.(bool)
}

// Decimal returns a fresh clone of the stored decimal value, or nil when
// Kind != KindDecimal or no value has been set and the registered default is nil.
func (e OptionEntry) Decimal() *apd.Decimal {
	if e.Kind != KindDecimal {
		return nil
	}
	if e.value == nil {
		return nil
	}
	src := e.value.(*apd.Decimal)
	if src == nil {
		return nil
	}
	out := new(apd.Decimal)
	out.Set(src)
	return out
}

// StringList returns a fresh copy of the stored string list, or nil when
// Kind != KindStringList.
func (e OptionEntry) StringList() []string {
	if e.Kind != KindStringList {
		return nil
	}
	if e.value == nil {
		return nil
	}
	src := e.value.([]string)
	return append([]string(nil), src...)
}

// Int returns the stored integer value, or 0 when Kind != KindInt.
func (e OptionEntry) Int() int {
	if e.Kind != KindInt {
		return 0
	}
	if e.value == nil {
		return 0
	}
	return e.value.(int)
}

// DecimalMap returns a fresh copy of the stored decimal map, or an empty
// non-nil map when Kind != KindDecimalMap or when nothing has been set.
func (e OptionEntry) DecimalMap() map[string]*apd.Decimal {
	if e.Kind != KindDecimalMap {
		return map[string]*apd.Decimal{}
	}
	var src map[string]*apd.Decimal
	if e.value != nil {
		src = e.value.(map[string]*apd.Decimal)
	}
	out := make(map[string]*apd.Decimal, len(src))
	for k, d := range src {
		clone := new(apd.Decimal)
		clone.Set(d)
		out[k] = clone
	}
	return out
}

// IntMap returns a fresh copy of the stored int map, or an empty non-nil map
// when Kind != KindIntMap or when nothing has been set.
func (e OptionEntry) IntMap() map[string]int {
	if e.Kind != KindIntMap {
		return map[string]int{}
	}
	var src map[string]int
	if e.value != nil {
		src = e.value.(map[string]int)
	}
	out := make(map[string]int, len(src))
	for k, val := range src {
		out[k] = val
	}
	return out
}

// Snapshot returns one OptionEntry per registered key, in ascending key order.
// Keys that were never set are included with their registered default. Map and
// slice values inside each entry are fresh copies. Snapshot on a nil
// *OptionValues returns the defaults for every key in the default registry.
func (v *OptionValues) Snapshot() []OptionEntry {
	var reg *registry
	if v != nil {
		reg = v.reg
	} else {
		reg = defaultRegistry
	}

	keys := make([]string, 0, len(reg.specs))
	for k := range reg.specs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	entries := make([]OptionEntry, 0, len(keys))
	for _, key := range keys {
		s := reg.specs[key]
		e := OptionEntry{Key: key, Kind: s.kind}
		switch s.kind {
		case KindString:
			e.value = v.String(key)
		case KindBool:
			e.value = v.Bool(key)
		case KindDecimal:
			e.value = v.Decimal(key)
		case KindStringList:
			e.value = v.StringList(key)
		case KindInt:
			e.value = v.Int(key)
		case KindDecimalMap:
			e.value = v.DecimalMap(key)
		case KindIntMap:
			e.value = v.IntMap(key)
		}
		entries = append(entries, e)
	}
	return entries
}

// ParseOptions walks ledger's directives and applies the default registry,
// returning typed option values and diagnostics for any malformed entries.
// The diagnostic format matches what [Ledger.Diagnostics] records. Option
// keys not in the registry are silently ignored. A nil ledger returns
// default values and no diagnostics.
func ParseOptions(ledger *Ledger) (*OptionValues, []Diagnostic) {
	v := newOptionValues(defaultRegistry)
	if ledger == nil {
		return v, nil
	}
	var diags []Diagnostic
	for _, d := range ledger.All() {
		opt, ok := d.(*Option)
		if !ok {
			continue
		}
		if err := v.set(opt.Key, opt.Value); err != nil {
			diags = append(diags, Diagnostic{
				Code:     invalidOptionCode,
				Span:     opt.Span,
				Severity: Error,
				Message:  fmt.Sprintf("invalid option %q: %v", opt.Key, err),
			})
		}
	}
	return v, diags
}

var defaultRegistry = newDefaultRegistry()

func newDefaultRegistry() *registry {
	r := newRegistry()
	mustRegisterDefault(r.register(spec{
		key:          "operating_currency",
		kind:         KindStringList,
		parse:        parseCurrencyListItem,
		defaultValue: []string(nil),
	}))
	// inferred_tolerance_multiplier is the upstream-deprecated alias.
	mustRegisterDefault(r.register(spec{
		key:          "tolerance_multiplier",
		kind:         KindDecimal,
		parse:        parseDecimalOption,
		defaultValue: apd.New(5, -1),
	}))
	mustRegisterDefault(r.register(spec{
		key:          "inferred_tolerance_multiplier",
		kind:         KindDecimal,
		parse:        parseDecimalOption,
		defaultValue: apd.New(5, -1),
		aliasOf:      "tolerance_multiplier",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "infer_tolerance_from_cost",
		kind:         KindBool,
		parse:        parseBoolOption,
		defaultValue: false,
	}))
	mustRegisterDefault(r.register(spec{
		key:          "plugin_processing_mode",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "title",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "",
	}))
	// booking_method: default for Open directives that omit the booking keyword.
	// Consumer: ast.ResolveBookingMethod; validated at resolution time, not parse time.
	mustRegisterDefault(r.register(spec{
		key:          "booking_method",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "STRICT",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "long_string_maxlines",
		kind:         KindInt,
		parse:        parseIntOption,
		defaultValue: 64,
	}))
	mustRegisterDefault(r.register(spec{
		key:          "inferred_tolerance_default",
		kind:         KindDecimalMap,
		parse:        parseDecimalMapEntry,
		defaultValue: map[string]*apd.Decimal(nil),
	}))
	// display_precision: per-currency display precision example (e.g. "USD:0.01").
	// Consumer: DisplayPrecisionContext in pkg/ast.
	mustRegisterDefault(r.register(spec{
		key:          "display_precision",
		kind:         KindIntMap,
		parse:        parseDisplayPrecisionEntry,
		defaultValue: map[string]int(nil),
	}))

	// Account-type names — options.py get_account_types(); consumer deferred
	// (account-type classification subsystem not yet present).
	mustRegisterDefault(r.register(spec{
		key:          "name_assets",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "Assets",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "name_liabilities",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "Liabilities",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "name_equity",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "Equity",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "name_income",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "Income",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "name_expenses",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "Expenses",
	}))

	// Derived-account references — options.py get_previous_accounts(),
	// get_current_accounts(), get_unrealized_account(); consumer deferred
	// (no derived-account computation subsystem yet).
	mustRegisterDefault(r.register(spec{
		key:          "account_previous_balances",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "Opening-Balances",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "account_previous_earnings",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "Earnings:Previous",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "account_previous_conversions",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "Conversions:Previous",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "account_current_earnings",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "Earnings:Current",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "account_current_conversions",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "Conversions:Current",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "account_unrealized_gains",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "Earnings:Unrealized",
	}))
	// account_rounding — upstream default is Python None; Go uses "".
	// Consumer deferred (rounding-error plugin not present).
	mustRegisterDefault(r.register(spec{
		key:          "account_rounding",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "",
	}))

	// Python-only / Fava-expected keys — no Go consumer; stored for
	// cross-tool compat.
	mustRegisterDefault(r.register(spec{
		key:          "pythonpath",
		kind:         KindStringList,
		parse:        parseStringOption,
		defaultValue: []string(nil),
	}))
	mustRegisterDefault(r.register(spec{
		key:          "insert_pythonpath",
		kind:         KindBool,
		parse:        parseBoolOption,
		defaultValue: false,
	}))
	// render_commas: enables thousands-separator commas in number output.
	// Read via ledger.Options.Bool("render_commas") at call sites; wired via
	// format.WithCommaGrouping.
	mustRegisterDefault(r.register(spec{
		key:          "render_commas",
		kind:         KindBool,
		parse:        parseBoolOption,
		defaultValue: false,
	}))

	// Deprecated parser flags — options.py; Go parser does not implement
	// these features; stored as inert booleans.
	mustRegisterDefault(r.register(spec{
		key:          "allow_pipe_separator",
		kind:         KindBool,
		parse:        parseBoolOption,
		defaultValue: false,
	}))
	mustRegisterDefault(r.register(spec{
		key:          "allow_deprecated_none_for_tags_and_links",
		kind:         KindBool,
		parse:        parseBoolOption,
		defaultValue: false,
	}))

	// Other deferred options — storage-only; consumers tracked as TODOs.
	// conversion_currency: options.py zero-rate conversion logic; no Go analog yet.
	mustRegisterDefault(r.register(spec{
		key:          "conversion_currency",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "NOTHING",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "commodities",
		kind:         KindStringList,
		parse:        parseStringOption,
		defaultValue: []string(nil),
	}))
	// plugin: v2 option form (deprecated); v3 uses the plugin directive.
	mustRegisterDefault(r.register(spec{
		key:          "plugin",
		kind:         KindStringList,
		parse:        parseStringOption,
		defaultValue: []string(nil),
	}))
	mustRegisterDefault(r.register(spec{
		key:          "documents",
		kind:         KindStringList,
		parse:        parseStringOption,
		defaultValue: []string(nil),
	}))

	return r
}

func mustRegisterDefault(err error) {
	if err != nil {
		panic(fmt.Sprintf("options: default registry initialization failed: %v", err))
	}
}

func parseStringOption(raw string) (any, error) {
	return raw, nil
}

func parseBoolOption(raw string) (any, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "TRUE":
		return true, nil
	case "FALSE":
		return false, nil
	}
	return nil, fmt.Errorf("expected TRUE or FALSE, got %q", raw)
}

func parseDecimalOption(raw string) (any, error) {
	d, _, err := apd.BaseContext.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return d, nil
}

func parseCurrencyListItem(raw string) (any, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("currency must not be empty")
	}
	return s, nil
}

// parseIntOption parses raw as a base-10 signed integer with surrounding
// whitespace trimmed.
func parseIntOption(raw string) (any, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("expected integer, got %q: %w", raw, err)
	}
	return n, nil
}

// splitMapEntry splits raw on the first ':' separator, returning the
// trimmed key and value strings. Returns an error when ':' is absent or
// the key is empty.
func splitMapEntry(raw string) (key, value string, err error) {
	idx := strings.IndexByte(raw, ':')
	if idx < 0 {
		return "", "", fmt.Errorf("missing ':' separator in %q", raw)
	}
	key = strings.TrimSpace(raw[:idx])
	if key == "" {
		return "", "", fmt.Errorf("empty sub-key in %q", raw)
	}
	value = strings.TrimSpace(raw[idx+1:])
	return key, value, nil
}

// parseDecimalMapEntry parses raw as "KEY:value" where value is an apd.Decimal.
// Errors when the separator is missing, KEY is empty, or value fails decimal parsing.
func parseDecimalMapEntry(raw string) (any, error) {
	key, val, err := splitMapEntry(raw)
	if err != nil {
		return nil, err
	}
	d, _, err := apd.BaseContext.NewFromString(val)
	if err != nil {
		return nil, fmt.Errorf("invalid decimal %q for key %q: %w", val, key, err)
	}
	return mapEntry[*apd.Decimal]{subKey: key, value: d}, nil
}

// parseIntMapEntry parses raw as "KEY:value" where value is a base-10 integer.
// Errors on the same conditions as parseDecimalMapEntry plus integer parse failure.
func parseIntMapEntry(raw string) (any, error) {
	key, val, err := splitMapEntry(raw)
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return nil, fmt.Errorf("invalid integer %q for key %q: %w", val, key, err)
	}
	return mapEntry[int]{subKey: key, value: n}, nil
}

// parseDisplayPrecisionEntry parses raw as "CURRENCY:DECIMAL" where DECIMAL is
// a positive example value (e.g. "USD:0.01"). The stored integer is the
// fractional-digit count derived from the example's exponent: max(0, -exponent).
// Rejects NaN, Infinity, negative, and zero values.
func parseDisplayPrecisionEntry(raw string) (any, error) {
	key, val, err := splitMapEntry(raw)
	if err != nil {
		return nil, err
	}
	d, _, err := apd.BaseContext.NewFromString(val)
	if err != nil {
		return nil, fmt.Errorf("invalid decimal %q for currency %q: %w", val, key, err)
	}
	digits, err := digitsFromDecimal(key, d)
	if err != nil {
		return nil, err
	}
	return mapEntry[int]{subKey: key, value: digits}, nil
}

// digitsFromDecimal validates d as a positive non-special decimal and returns
// the fractional-digit count max(0, -d.Exponent).
func digitsFromDecimal(currency string, d *apd.Decimal) (int, error) {
	switch d.Form {
	case apd.NaN, apd.NaNSignaling:
		return 0, fmt.Errorf("NaN is not a valid precision example for %q", currency)
	case apd.Infinite:
		return 0, fmt.Errorf("infinity is not a valid precision example for %q", currency)
	}
	if d.Sign() < 0 {
		return 0, fmt.Errorf("negative value %s is not a valid precision example for %q", d, currency)
	}
	if d.IsZero() {
		return 0, fmt.Errorf("zero is not a valid precision example for %q; use %q for zero fractional digits", currency, currency+":1")
	}
	digits := 0
	if d.Exponent < 0 {
		digits = int(-d.Exponent)
	}
	return digits, nil
}
