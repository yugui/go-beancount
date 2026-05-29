package std_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/query/types"
)

func TestMetaPresentKeyTypedValue(t *testing.T) {
	l := scalarLedger(t)

	// A present String-valued key returns the stored String.
	res := mustQuery(t, l,
		"SELECT meta('category') AS c FROM postings WHERE account = 'Assets:Brokerage:AAPL'")
	checkStr(t, res.Rows[0][0], "tech")

	// A present Number-valued key returns the stored value with its own
	// runtime type (Decimal), confirming getitem's dynamic typing.
	res = mustQuery(t, l,
		"SELECT meta('qty') AS q FROM postings WHERE account = 'Assets:Brokerage:AAPL'")
	v := res.Rows[0][0]
	if v.Type() != types.Decimal {
		t.Fatalf("meta('qty') type = %s, want decimal", v.Type())
	}
	if v.Format() != "42" {
		t.Errorf("meta('qty') = %s, want 42", v.Format())
	}
}

func TestMetaMissingKey(t *testing.T) {
	l := scalarLedger(t)

	// 2-arg form: a missing key yields NULL.
	res := mustQuery(t, l,
		"SELECT meta('nope') AS m FROM postings WHERE account = 'Assets:Brokerage:AAPL'")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("meta('nope') = %v, want NULL", res.Rows[0][0])
	}

	// 3-arg form: a missing key yields the String fallback.
	res = mustQuery(t, l,
		"SELECT meta('nope', 'fallback') AS m FROM postings WHERE account = 'Assets:Brokerage:AAPL'")
	checkStr(t, res.Rows[0][0], "fallback")

	// A present key with a 3-arg form still returns the stored value.
	res = mustQuery(t, l,
		"SELECT meta('category', 'fallback') AS m FROM postings WHERE account = 'Assets:Brokerage:AAPL'")
	checkStr(t, res.Rows[0][0], "tech")
}
