package inventory

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestPositionClone(t *testing.T) {
	orig := Position{
		Units: ast.Amount{Number: decimalVal(t, "10"), Currency: "ACME"},
		Cost: &Cost{
			Number:   decimalVal(t, "100"),
			Currency: "USD",
			Date:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Label:    "lot-a",
		},
	}

	clone := orig.Clone()

	// Cost pointer must differ so mutations are isolated.
	if clone.Cost == orig.Cost {
		t.Fatal("clone shares Cost pointer with original")
	}
	if !clone.Cost.Equal(*orig.Cost) {
		t.Errorf("clone.Cost %+v not equal to orig.Cost %+v", clone.Cost, orig.Cost)
	}

	// Mutate the clone's Units number.
	newUnits := decimalVal(t, "42")
	clone.Units.Number.Set(&newUnits)
	clone.Cost.Label = "lot-z"

	if got := orig.Units.Number.String(); got != "10" {
		t.Errorf("orig.Units.Number = %q, want %q", got, "10")
	}
	if orig.Cost.Label != "lot-a" {
		t.Errorf("orig.Cost.Label = %q, want %q", orig.Cost.Label, "lot-a")
	}
}

func TestPositionCloneNilCost(t *testing.T) {
	orig := Position{
		Units: ast.Amount{Number: decimalVal(t, "10"), Currency: "USD"},
		Cost:  nil,
	}
	clone := orig.Clone()
	if clone.Cost != nil {
		t.Errorf("clone.Cost = %+v, want nil", clone.Cost)
	}

	// Mutating the clone's Units must not affect the original.
	newUnits := decimalVal(t, "99")
	clone.Units.Number.Set(&newUnits)
	if got := orig.Units.Number.String(); got != "10" {
		t.Errorf("orig.Units.Number = %q, want %q", got, "10")
	}
}

func TestPositionCommodity(t *testing.T) {
	p := Position{
		Units: ast.Amount{Number: decimalVal(t, "5"), Currency: "ACME"},
	}
	if got := p.Commodity(); got != "ACME" {
		t.Errorf("Commodity() = %q, want %q", got, "ACME")
	}
}
