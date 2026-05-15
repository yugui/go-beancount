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
	return seedInventoryOf(t, num, "USD")
}

// seedInventoryOf returns a fresh Inventory pre-populated with a single
// position of the given amount and currency.
func seedInventoryOf(t *testing.T, num, currency string) *Inventory {
	t.Helper()
	inv := NewInventory()
	if err := inv.Add(Position{Units: mkAmount(t, num, currency)}); err != nil {
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

// ---- prepareForRollback tests ----

// TestStateTrace_PrepareForRollback_RecordsAndReturnsLive verifies that
// prepareForRollback records the account in the rolledBack set and returns
// the live inventory pointer for that account.
func TestStateTrace_PrepareForRollback_RecordsAndReturnsLive(t *testing.T) {
	seed := seedInventory(t, "100")
	state := map[ast.Account]*Inventory{"Assets:A": seed}
	trace := newStateTrace(state)

	// Touch the account first so it has a live inventory.
	live := trace.prepareForEdit("Assets:A")

	inv := trace.prepareForRollback("Assets:A")

	if inv != live {
		t.Errorf("prepareForRollback returned %p, want live pointer %p", inv, live)
	}
	if trace.rolledBack == nil {
		t.Fatalf("rolledBack map is nil after prepareForRollback")
	}
	if _, ok := trace.rolledBack["Assets:A"]; !ok {
		t.Errorf("rolledBack does not contain Assets:A after prepareForRollback")
	}
}

// TestStateTrace_PrepareForRollback_LazyInit verifies that the rolledBack
// map is nil before the first prepareForRollback call and non-nil after.
func TestStateTrace_PrepareForRollback_LazyInit(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)

	if trace.rolledBack != nil {
		t.Errorf("prepareForRollback: rolledBack should be nil before any call, got non-nil")
	}

	trace.prepareForRollback("Assets:A")

	if trace.rolledBack == nil {
		t.Errorf("prepareForRollback: rolledBack should be non-nil after first call, got nil")
	}
}

// TestStateTrace_PrepareForRollback_NewAccount verifies that prepareForRollback
// on an account not yet touched by prepareForEdit still initializes the
// before-snapshot (nil) and creates a live inventory, mirroring prepareForEdit.
func TestStateTrace_PrepareForRollback_NewAccount(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)

	inv := trace.prepareForRollback("Assets:A")

	if inv == nil {
		t.Fatalf("prepareForRollback returned nil for new account")
	}
	if state["Assets:A"] != inv {
		t.Errorf("prepareForRollback: state[acct] = %p, want same pointer as returned inv %p", state["Assets:A"], inv)
	}
	snap, present := trace.before["Assets:A"]
	if !present {
		t.Fatalf("before map lacks entry after prepareForRollback on new account")
	}
	if snap != nil {
		t.Errorf("prepareForRollback: before[acct] = %v, want nil for new account", snap)
	}
	if _, ok := trace.rolledBack["Assets:A"]; !ok {
		t.Errorf("prepareForRollback: rolledBack does not contain Assets:A")
	}
}

// ---- diff() exclusion rule tests ----

// TestStateTrace_Diff_RolledBack_EqualNonNil verifies that an account in
// rolledBack whose live state equals the before-snapshot is excluded from
// both before and after.
func TestStateTrace_Diff_RolledBack_EqualNonNil(t *testing.T) {
	seed := seedInventory(t, "100")
	state := map[ast.Account]*Inventory{"Assets:A": seed}
	trace := newStateTrace(state)

	inv := trace.prepareForEdit("Assets:A")
	// Mutate, then undo by subtracting the same amount.
	if err := inv.Add(Position{Units: mkAmount(t, "50", "USD")}); err != nil {
		t.Fatalf("Add(50): %v", err)
	}
	if err := inv.Add(Position{Units: mkAmount(t, "-50", "USD")}); err != nil {
		t.Fatalf("Add(-50): %v", err)
	}
	// Live state is now equal to before (100 USD).
	trace.rolledBack = map[ast.Account]struct{}{"Assets:A": {}}

	before, after := trace.diff()

	if _, ok := before["Assets:A"]; ok {
		t.Errorf("diff(): before contains rolledBack+equal account Assets:A; want excluded")
	}
	if _, ok := after["Assets:A"]; ok {
		t.Errorf("diff(): after contains rolledBack+equal account Assets:A; want excluded")
	}
}

// TestStateTrace_Diff_RolledBack_EqualNilBeforeEmptyState verifies that an
// account in rolledBack with before==nil and an empty live state is excluded.
func TestStateTrace_Diff_RolledBack_EqualNilBeforeEmptyState(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)

	// prepareForRollback on a new account records before==nil and creates empty inv.
	trace.prepareForRollback("Assets:A")
	// Live state is empty (no mutations applied).

	before, after := trace.diff()

	if _, ok := before["Assets:A"]; ok {
		t.Errorf("diff(): before contains rolledBack+empty account; want excluded")
	}
	if _, ok := after["Assets:A"]; ok {
		t.Errorf("diff(): after contains rolledBack+empty account; want excluded")
	}
}

// TestStateTrace_Diff_RolledBack_NotEqual verifies that an account in
// rolledBack that still differs from its before-snapshot is included in
// both before and after (partial mutation residue must remain visible).
func TestStateTrace_Diff_RolledBack_NotEqual(t *testing.T) {
	seed := seedInventory(t, "100")
	state := map[ast.Account]*Inventory{"Assets:A": seed}
	trace := newStateTrace(state)

	inv := trace.prepareForEdit("Assets:A")
	// Mutate but do not undo — live state diverges from before.
	if err := inv.Add(Position{Units: mkAmount(t, "50", "USD")}); err != nil {
		t.Fatalf("Add(50): %v", err)
	}
	trace.rolledBack = map[ast.Account]struct{}{"Assets:A": {}}

	before, after := trace.diff()

	if _, ok := before["Assets:A"]; !ok {
		t.Errorf("diff(): before missing rolledBack+unequal account Assets:A; want included")
	}
	if _, ok := after["Assets:A"]; !ok {
		t.Errorf("diff(): after missing rolledBack+unequal account Assets:A; want included")
	}
}

// TestStateTrace_Diff_NotRolledBack_NetZero verifies that an account NOT in
// rolledBack is included even if its live state happens to equal its
// before-snapshot (net-zero change). rolledBack is the only signal for
// "intentionally suppressed"; net-zero without rollback is still surfaced.
func TestStateTrace_Diff_NotRolledBack_NetZero(t *testing.T) {
	seed := seedInventory(t, "100")
	state := map[ast.Account]*Inventory{"Assets:A": seed}
	trace := newStateTrace(state)

	inv := trace.prepareForEdit("Assets:A")
	// Mutate then undo — net-zero, but account is not in rolledBack.
	if err := inv.Add(Position{Units: mkAmount(t, "50", "USD")}); err != nil {
		t.Fatalf("Add(50): %v", err)
	}
	if err := inv.Add(Position{Units: mkAmount(t, "-50", "USD")}); err != nil {
		t.Fatalf("Add(-50): %v", err)
	}
	// rolledBack is nil — not marked.

	before, after := trace.diff()

	if _, ok := before["Assets:A"]; !ok {
		t.Errorf("diff(): before missing net-zero (not rolledBack) account; want included")
	}
	if _, ok := after["Assets:A"]; !ok {
		t.Errorf("diff(): after missing net-zero (not rolledBack) account; want included")
	}
}
