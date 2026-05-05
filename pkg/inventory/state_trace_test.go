package inventory

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

// seedInventory returns a fresh Inventory pre-populated with a single
// USD position. Used by tests that need a recognisably non-empty
// inventory to verify clone independence.
func seedInventory(t *testing.T, num string) *Inventory {
	t.Helper()
	inv := NewInventory()
	if err := inv.Add(Position{Units: mkAmount(t, num, "USD")}); err != nil {
		t.Fatalf("seed inventory: %v", err)
	}
	return inv
}

func TestStateTrace_PrepareForEdit_NewAccount(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)

	inv := trace.prepareForEdit("Assets:A")

	if inv == nil {
		t.Fatalf("prepareForEdit returned nil for new account")
	}
	if state["Assets:A"] != inv {
		t.Errorf("state[acct] = %p, want same pointer as returned %p", state["Assets:A"], inv)
	}
	snapshot, present := trace.before["Assets:A"]
	if !present {
		t.Fatalf("before map lacks entry for newly-touched account")
	}
	if snapshot != nil {
		t.Errorf("before[acct] = %v, want nil for previously-untouched account", snapshot)
	}
}

func TestStateTrace_PrepareForEdit_ExistingAccount(t *testing.T) {
	existing := seedInventory(t, "100")
	state := map[ast.Account]*Inventory{"Assets:A": existing}
	trace := newStateTrace(state)

	inv := trace.prepareForEdit("Assets:A")

	if inv != existing {
		t.Errorf("prepareForEdit returned %p, want pre-existing pointer %p", inv, existing)
	}
	snapshot := trace.before["Assets:A"]
	if snapshot == nil {
		t.Fatalf("before[acct] = nil, want a clone of the existing inventory")
	}
	if snapshot == existing {
		t.Errorf("before[acct] is the same pointer as state[acct]; want a clone")
	}
	if !snapshot.Equal(existing) {
		t.Errorf("prepareForEdit(): before[acct].Equal(existing) = false; clone differs from source (got len=%d, want len=%d)", snapshot.Len(), existing.Len())
	}
}

func TestStateTrace_PrepareForEdit_Idempotent(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)

	first := trace.prepareForEdit("Assets:A")
	// Mutate the live inventory between calls; the second call must
	// still return the same pointer and must not refresh the
	// before-snapshot from the now-mutated state.
	if err := first.Add(Position{Units: mkAmount(t, "50", "USD")}); err != nil {
		t.Fatalf("mutate inventory: %v", err)
	}
	second := trace.prepareForEdit("Assets:A")

	if second != first {
		t.Errorf("second prepareForEdit returned %p, want same pointer as first %p", second, first)
	}
	if trace.before["Assets:A"] != nil {
		t.Errorf("before[acct] = %v after second prepareForEdit, want preserved nil from first call", trace.before["Assets:A"])
	}
}

func TestStateTrace_PrepareForEdit_BeforeIsIndependent(t *testing.T) {
	seed := seedInventory(t, "100")
	state := map[ast.Account]*Inventory{"Assets:A": seed}
	trace := newStateTrace(state)

	inv := trace.prepareForEdit("Assets:A")
	snapshot := trace.before["Assets:A"]
	if snapshot == nil {
		t.Fatalf("before snapshot missing for existing account")
	}

	// Mutate the live inventory; the snapshot must not drift.
	if err := inv.Add(Position{Units: mkAmount(t, "10", "USD")}); err != nil {
		t.Fatalf("mutate live inventory: %v", err)
	}

	expected := seedInventory(t, "100")
	if !snapshot.Equal(expected) {
		t.Errorf("prepareForEdit(): snapshot drifted after live mutation: got len=%d, want %d", snapshot.Len(), expected.Len())
	}
}

func TestStateTrace_Diff_OwnershipAndCloning(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)
	trace.prepareForEdit("Assets:A")

	before, after := trace.diff()

	// Map-ownership: a write to trace.before after diff must be
	// visible through the returned before map. That's the proof
	// that diff transfers the same underlying map (rather than
	// returning a copy).
	trace.before["Assets:B"] = nil
	if _, ok := before["Assets:B"]; !ok {
		t.Errorf("diff(): returned before does not see trace.before mutations; ownership was not transferred")
	}

	// after-cloning: mutating an after-inventory must not affect
	// the corresponding state-inventory.
	afterInv, ok := after["Assets:A"]
	if !ok || afterInv == nil {
		t.Fatalf("after lacks entry for touched account")
	}
	if err := afterInv.Add(Position{Units: mkAmount(t, "999", "JPY")}); err != nil {
		t.Fatalf("mutate after-inventory: %v", err)
	}
	stateInv := state["Assets:A"]
	if stateInv == nil {
		t.Fatalf("state lost the prepared inventory")
	}
	if !stateInv.IsEmpty() {
		t.Errorf("diff(): state[acct] was modified via after-clone; want clone independence (state has %d positions)", stateInv.Len())
	}
}

func TestStateTrace_Diff_OnlyTouchedAccounts(t *testing.T) {
	untouched := seedInventory(t, "5")
	state := map[ast.Account]*Inventory{
		"Assets:Untouched": untouched,
	}
	trace := newStateTrace(state)
	trace.prepareForEdit("Assets:Touched")

	before, after := trace.diff()

	if _, ok := before["Assets:Untouched"]; ok {
		t.Errorf("diff(): before contains untouched account Assets:Untouched")
	}
	if _, ok := after["Assets:Untouched"]; ok {
		t.Errorf("diff(): after contains untouched account Assets:Untouched")
	}
	if _, ok := before["Assets:Touched"]; !ok {
		t.Errorf("diff(): before missing touched account Assets:Touched")
	}
	if _, ok := after["Assets:Touched"]; !ok {
		t.Errorf("diff(): after missing touched account Assets:Touched")
	}
}

func TestStateTrace_Diff_Empty(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)

	before, after := trace.diff()

	// The visitor contract requires non-nil maps even when empty
	// (callers iterate via `range` either way, but they may also
	// distinguish "no touched accounts" from "uninitialised").
	if before == nil {
		t.Errorf("diff(): before is nil; want empty non-nil map")
	}
	if after == nil {
		t.Errorf("diff(): after is nil; want empty non-nil map")
	}
	if len(before) != 0 {
		t.Errorf("diff(): before has %d entries, want 0", len(before))
	}
	if len(after) != 0 {
		t.Errorf("diff(): after has %d entries, want 0", len(after))
	}
}
