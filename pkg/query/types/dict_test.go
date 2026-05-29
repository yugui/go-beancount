package types_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/query/types"
)

func TestDictGetPresentAbsent(t *testing.T) {
	d := types.NewDict(map[string]types.Value{
		"a": types.NewInt(1),
		"b": types.Null(types.String),
	})
	if v, ok := d.Get("a"); !ok {
		t.Error("Get(a): ok=false")
	} else if n, _ := types.AsInt(v); n != 1 {
		t.Errorf("Get(a) = %v", v)
	}
	if v, ok := d.Get("b"); !ok || !v.IsNull() {
		t.Errorf("Get(b) = (%v,%v); want present NULL", v, ok)
	}
	if _, ok := d.Get("missing"); ok {
		t.Error("Get(missing): ok=true")
	}
}

func TestDictNilValueDropped(t *testing.T) {
	d := types.NewDict(map[string]types.Value{"a": types.NewInt(1), "b": nil})
	if d.Len() != 1 {
		t.Errorf("Len() = %d, want 1 (nil entry dropped)", d.Len())
	}
	if _, ok := d.Get("b"); ok {
		t.Error("nil-valued key should be absent")
	}
}

func TestDictCompareDeterministic(t *testing.T) {
	a := types.NewDict(map[string]types.Value{"k": types.NewInt(1)})
	b := types.NewDict(map[string]types.Value{"k": types.NewInt(2)})
	c := types.NewDict(map[string]types.Value{"k": types.NewInt(1), "z": types.NewInt(9)})

	if a.Compare(b) != -1 {
		t.Errorf("{k:1} vs {k:2} = %d, want -1", a.Compare(b))
	}
	if a.Compare(c) != -1 {
		t.Errorf("{k:1} (prefix) vs {k:1,z:9} = %d, want -1", a.Compare(c))
	}
	if a.Compare(a) != 0 {
		t.Error("reflexive compare != 0")
	}
	// determinism: insertion order of the input map must not matter.
	d1 := types.NewDict(map[string]types.Value{"x": types.NewInt(1), "y": types.NewInt(2)})
	d2 := types.NewDict(map[string]types.Value{"y": types.NewInt(2), "x": types.NewInt(1)})
	if d1.Compare(d2) != 0 {
		t.Error("dicts with same entries compare != 0")
	}
}

func TestDictEmpty(t *testing.T) {
	d := types.NewDict(nil)
	if d.Len() != 0 {
		t.Errorf("Len() = %d, want 0", d.Len())
	}
	if d.Format() != "{}" {
		t.Errorf("Format() = %q, want {}", d.Format())
	}
}

func TestDictInputCopyIsolation(t *testing.T) {
	m := map[string]types.Value{"a": types.NewInt(1)}
	d := types.NewDict(m)
	m["b"] = types.NewInt(2)
	if d.Len() != 1 {
		t.Error("Dict retained an alias to the input map")
	}
}
