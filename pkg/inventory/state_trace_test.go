package inventory

import (
	"strings"
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

// ---- group checkpoint / rollback tests ----

// TestStateTrace_Group_SeedStateAndRollback is a table-driven test covering
// three related scenarios that share the structure:
//
//	seed state → enter/commit one or more cycles → optionally rollback → assert final state.
func TestStateTrace_Group_SeedStateAndRollback(t *testing.T) {
	type cycle struct {
		currency string
		add      string // amount to add to "Assets:A" in this cycle
	}
	tests := []struct {
		name     string
		seed     string // initial amount in "Assets:A" ("" = empty map)
		cycles   []cycle
		rollback bool   // whether to call rollbackGroup("USD") at the end
		want     string // expected amount in "Assets:A" after all operations
	}{
		{
			name:     "RollbackRestoresState",
			seed:     "100",
			cycles:   []cycle{{"USD", "50"}},
			rollback: true,
			want:     "100",
		},
		{
			name:     "CommitWithoutRollback",
			seed:     "",
			cycles:   []cycle{{"USD", "42"}},
			rollback: false,
			want:     "42",
		},
		{
			name:     "FirstTouchWins",
			seed:     "100",
			cycles:   []cycle{{"USD", "10"}, {"USD", "5"}},
			rollback: true,
			want:     "100",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := map[ast.Account]*Inventory{}
			if tc.seed != "" {
				state["Assets:A"] = seedInventory(t, tc.seed)
			}
			trace := newStateTrace(state)

			for i, c := range tc.cycles {
				tok := trace.enterGroup()
				inv := trace.prepareForEdit("Assets:A")
				if err := inv.Add(Position{Units: mkAmount(t, c.add, c.currency)}); err != nil {
					t.Fatalf("cycle %d Add(%s %s): %v", i+1, c.add, c.currency, err)
				}
				trace.commitGroup(tok, "USD")
			}

			if tc.rollback {
				trace.rollbackGroup("USD")
			}

			got := state["Assets:A"]
			want := seedInventory(t, tc.want)
			if !got.Equal(want) {
				t.Errorf("state[acct] = %v, want %v", got, want)
			}
		})
	}
}

// TestStateTrace_Group_IndependentGroupsDoNotInterfere verifies that
// rolling back group A does not affect accounts touched only by group B.
func TestStateTrace_Group_IndependentGroupsDoNotInterfere(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)

	// Group A touches Assets:X.
	tokA := trace.enterGroup()
	invX := trace.prepareForEdit("Assets:X")
	if err := invX.Add(Position{Units: mkAmount(t, "10", "USD")}); err != nil {
		t.Fatalf("mutate Assets:X: %v", err)
	}
	trace.commitGroup(tokA, "USD")

	// Group B touches Assets:Y.
	tokB := trace.enterGroup()
	invY := trace.prepareForEdit("Assets:Y")
	if err := invY.Add(Position{Units: mkAmount(t, "20", "EUR")}); err != nil {
		t.Fatalf("mutate Assets:Y: %v", err)
	}
	trace.commitGroup(tokB, "EUR")

	// Roll back only group A.
	trace.rollbackGroup("USD")

	// Assets:X should be restored (empty new inventory).
	if !state["Assets:X"].IsEmpty() {
		t.Errorf("after rollbackGroup(USD): Assets:X should be empty, got %v", state["Assets:X"])
	}
	// Assets:Y must still carry group B's mutation.
	wantY := seedInventoryOf(t, "20", "EUR")
	if !state["Assets:Y"].Equal(wantY) {
		t.Errorf("after rollbackGroup(USD): Assets:Y = %v, want %v", state["Assets:Y"], wantY)
	}
}

// TestStateTrace_Group_RollbackUnknownKeyIsNoop verifies that calling
// rollbackGroup with a key that was never committed does nothing.
func TestStateTrace_Group_RollbackUnknownKeyIsNoop(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)

	// Should not panic and state should remain empty.
	trace.rollbackGroup("USD")
	if len(state) != 0 {
		t.Errorf("rollbackGroup(unknown key): state unexpectedly modified")
	}
}

// TestStateTrace_Group_RollbackIdempotent verifies that a second
// rollbackGroup call for the same key is a no-op (does not panic).
func TestStateTrace_Group_RollbackIdempotent(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)

	tok := trace.enterGroup()
	inv := trace.prepareForEdit("Assets:A")
	if err := inv.Add(Position{Units: mkAmount(t, "1", "USD")}); err != nil {
		t.Fatalf("Add(1 USD): %v", err)
	}
	trace.commitGroup(tok, "USD")

	trace.rollbackGroup("USD") // first call — undoes mutation
	trace.rollbackGroup("USD") // second call — must be no-op, not panic
}

// TestStateTrace_Group_PrepareForEditOutsideScopeUnchanged verifies
// that prepareForEdit behaves identically when no group scope is open,
// preserving the existing before-map contract. This is a regression
// guard complementing TestStateTrace_PrepareForEdit_ExistingAccount:
// that test exercises the non-group code path in isolation; this test
// confirms the group-aware rewrite did not alter that path.
func TestStateTrace_Group_PrepareForEditOutsideScopeUnchanged(t *testing.T) {
	existing := seedInventory(t, "77")
	state := map[ast.Account]*Inventory{"Assets:A": existing}
	trace := newStateTrace(state)

	// No enterGroup called — this exercises the non-group path.
	inv := trace.prepareForEdit("Assets:A")

	if inv != existing {
		t.Errorf("prepareForEdit (no scope) returned wrong pointer")
	}
	snap := trace.before["Assets:A"]
	if snap == nil {
		t.Fatalf("before[acct] = nil, want a clone")
	}
	if snap == existing {
		t.Errorf("before[acct] is the same pointer as state[acct]; want a clone")
	}
	want := seedInventory(t, "77")
	if !snap.Equal(want) {
		t.Errorf("before snapshot = %v, want %v", snap, want)
	}
}

// TestStateTrace_Group_EnterGroupPanicsWhenAlreadyOpen verifies that
// calling enterGroup without first committing the previous scope panics
// with a message identifying the violation.
func TestStateTrace_Group_EnterGroupPanicsWhenAlreadyOpen(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)
	trace.enterGroup() // open first scope (not committed)

	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("enterGroup on already-open scope: got no panic, want panic")
			return
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "previous group scope was not committed") {
			t.Errorf("got panic %v, want message containing %q", r, "previous group scope was not committed")
		}
	}()
	trace.enterGroup() // should panic
}

// TestStateTrace_Group_BeforeMapUnaffectedByRollback verifies that
// rollbackGroup does not modify st.before, preserving the visitor
// contract that before[acct] is the pre-transaction snapshot.
func TestStateTrace_Group_BeforeMapUnaffectedByRollback(t *testing.T) {
	initial := seedInventory(t, "50")
	state := map[ast.Account]*Inventory{"Assets:A": initial}
	trace := newStateTrace(state)

	tok := trace.enterGroup()
	trace.prepareForEdit("Assets:A")
	trace.commitGroup(tok, "USD")
	trace.rollbackGroup("USD")

	// before["Assets:A"] must still equal the original 50 USD snapshot.
	snap := trace.before["Assets:A"]
	if snap == nil {
		t.Fatalf("before[acct] = nil after rollback; want pre-transaction snapshot")
	}
	want := seedInventory(t, "50")
	if !snap.Equal(want) {
		t.Errorf("before[acct] = %v after rollback, want %v", snap, want)
	}
}

// TestStateTrace_Group_CommitTokenMismatchPanics verifies that commitGroup
// panics when given a token that does not match the currently open scope.
func TestStateTrace_Group_CommitTokenMismatchPanics(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)

	_ = trace.enterGroup()
	staleToken := groupToken{id: 9999} // deliberately wrong ID

	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("commitGroup with mismatched token: got no panic, want panic")
			return
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "token does not match") {
			t.Errorf("got panic %v, want message containing %q", r, "token does not match")
		}
	}()
	trace.commitGroup(staleToken, "USD") // should panic
}

// TestStateTrace_Group_CommitWithoutEnterPanics verifies that commitGroup
// panics when no group scope is currently open.
func TestStateTrace_Group_CommitWithoutEnterPanics(t *testing.T) {
	state := map[ast.Account]*Inventory{}
	trace := newStateTrace(state)

	tok := groupToken{id: 1}
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("commitGroup with no open scope: got no panic, want panic")
			return
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "no group scope is open") {
			t.Errorf("got panic %v, want message containing %q", r, "no group scope is open")
		}
	}()
	trace.commitGroup(tok, "USD") // should panic
}

// TestStateTrace_Group_PrepareForEditSecondTouchInScope verifies the
// firstTouchInTrace==false path in prepareForEdit: an account touched
// outside any scope, then touched again inside a scope, gets a live-state
// clone as its restore snapshot (not the before-map alias).
func TestStateTrace_Group_PrepareForEditSecondTouchInScope(t *testing.T) {
	initial := seedInventory(t, "100")
	state := map[ast.Account]*Inventory{"Assets:A": initial}
	trace := newStateTrace(state)

	// First touch: outside any scope. This records before["Assets:A"] = clone(100 USD)
	// and sets live state to the same *Inventory pointer.
	inv := trace.prepareForEdit("Assets:A")
	if err := inv.Add(Position{Units: mkAmount(t, "20", "USD")}); err != nil {
		t.Fatalf("pre-scope Add(20 USD): %v", err)
	}
	// Live state is now 120 USD; before is still 100 USD.

	// Open a group scope and touch the same account again. This is the
	// firstTouchInTrace==false branch: the snapshot should be a clone of
	// the current live state (120 USD), not before (100 USD).
	tok := trace.enterGroup()
	inv2 := trace.prepareForEdit("Assets:A")
	if err := inv2.Add(Position{Units: mkAmount(t, "5", "USD")}); err != nil {
		t.Fatalf("in-scope Add(5 USD): %v", err)
	}
	trace.commitGroup(tok, "USD")
	// Live state is now 125 USD.

	trace.rollbackGroup("USD")

	// After rollback the group's scope should be undone, restoring to 120 USD
	// (the state at the time the scope opened), not 100 USD (before-map value).
	got := state["Assets:A"]
	want := seedInventory(t, "120")
	if !got.Equal(want) {
		t.Errorf("after rollbackGroup: state[acct] = %v, want 120 USD (pre-scope live state)", got)
	}

	// before["Assets:A"] must remain unchanged (100 USD) — rollback does not touch it.
	beforeSnap := trace.before["Assets:A"]
	wantBefore := seedInventory(t, "100")
	if !beforeSnap.Equal(wantBefore) {
		t.Errorf("before[acct] = %v after rollback, want 100 USD (original pre-transaction value)", beforeSnap)
	}
}

// TestStateTrace_Group_RollbackBeforeIndependence is a regression test for
// the aliasing invariant introduced with M1: after rollbackGroup, mutations
// to live state must not affect st.before (they must be independent copies).
// This locks the invariant that before[acct] is always a frozen pre-txn
// snapshot even after a rollback writes a new *Inventory into st.state.
func TestStateTrace_Group_RollbackBeforeIndependence(t *testing.T) {
	initial := seedInventory(t, "50")
	state := map[ast.Account]*Inventory{"Assets:A": initial}
	trace := newStateTrace(state)

	tok := trace.enterGroup()
	inv := trace.prepareForEdit("Assets:A")
	if err := inv.Add(Position{Units: mkAmount(t, "30", "USD")}); err != nil {
		t.Fatalf("in-scope Add(30 USD): %v", err)
	}
	trace.commitGroup(tok, "USD")
	// Live state: 80 USD.  before: 50 USD.

	trace.rollbackGroup("USD")
	// Live state: 50 USD (restored via Clone in rollbackGroup).

	// Mutate live state after rollback.
	if err := state["Assets:A"].Add(Position{Units: mkAmount(t, "99", "USD")}); err != nil {
		t.Fatalf("post-rollback Add(99 USD): %v", err)
	}
	// Live state: 149 USD.

	// before must still reflect the original 50 USD — independent of the clone
	// that rollbackGroup wrote back.
	beforeSnap := trace.before["Assets:A"]
	want := seedInventory(t, "50")
	if !beforeSnap.Equal(want) {
		t.Errorf("before[acct] = %v after post-rollback mutation, want 50 USD (must be independent)", beforeSnap)
	}

	// Also verify diff() sees the correct before/after pair.
	before, after := trace.diff()
	wantBefore := seedInventory(t, "50")
	if !before["Assets:A"].Equal(wantBefore) {
		t.Errorf("diff() before[acct] = %v, want 50 USD", before["Assets:A"])
	}
	wantAfter := seedInventory(t, "149")
	if !after["Assets:A"].Equal(wantAfter) {
		t.Errorf("diff() after[acct] = %v, want 149 USD", after["Assets:A"])
	}
}
