package std

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/query/types"
)

// The lean tables never expose a NULL dict column (the meta column is always
// a present, possibly empty Dict), so getitem's NULL-dict branch is
// unreachable through query.Query. This direct test covers it; it is the
// CLAUDE.md "no exported path" exception.
func TestGetitemNullDict(t *testing.T) {
	v, err := getitem([]types.Value{types.Null(types.DictType), types.NewString("k")})
	if err != nil {
		t.Fatalf("getitem: %v", err)
	}
	if !v.IsNull() {
		t.Errorf("getitem(NULL dict) = %v, want NULL", v)
	}

	// With a String default, a NULL dict still returns NULL, not the default:
	// the default applies only to a present dict with an absent key.
	v, err = getitem([]types.Value{types.Null(types.DictType), types.NewString("k"), types.NewString("d")})
	if err != nil {
		t.Fatalf("getitem: %v", err)
	}
	if !v.IsNull() {
		t.Errorf("getitem(NULL dict, default) = %v, want NULL (default ignored)", v)
	}
}
