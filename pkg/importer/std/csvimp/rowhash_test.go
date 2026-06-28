package csvimp

import (
	"strings"
	"testing"
)

func shapeConfigFromTOML(t *testing.T, src string) shapeConfig {
	t.Helper()
	var sc shapeConfig
	if err := permissiveDecoder(src)(&sc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return sc
}

// Two instances must not share a rowhash key: a shared id key with
// conflicting values trips pkg/distribute/dedup's veto rule.
func TestRowhashKey_DefaultInstanceScoped(t *testing.T) {
	sc := shapeConfigFromTOML(t, simpleTOML)
	bank, err := validateShape("bank", sc)
	if err != nil {
		t.Fatalf("validateShape(bank): %v", err)
	}
	card, err := validateShape("card", sc)
	if err != nil {
		t.Fatalf("validateShape(card): %v", err)
	}
	if got, want := bank.rowhashKey, "csvimp-rowhash-bank"; got != want {
		t.Errorf("bank rowhashKey = %q, want %q", got, want)
	}
	if bank.rowhashKey == card.rowhashKey {
		t.Errorf("distinct instances share rowhash key %q; the dedup veto would misfire", bank.rowhashKey)
	}
}

func TestRowhashKey_ExplicitOverrideVerbatim(t *testing.T) {
	sc := shapeConfigFromTOML(t, simpleTOML)
	sc.RowhashKey = "mybank-id"
	s, err := validateShape("bank", sc)
	if err != nil {
		t.Fatalf("validateShape: %v", err)
	}
	if got, want := s.rowhashKey, "mybank-id"; got != want {
		t.Errorf("rowhashKey = %q, want %q (explicit key used verbatim)", got, want)
	}
}

func TestRowhashKey_EmptyFallsBackToDerived(t *testing.T) {
	sc := shapeConfigFromTOML(t, simpleTOML)
	sc.RowhashKey = ""
	s, err := validateShape("bank", sc)
	if err != nil {
		t.Fatalf("validateShape: %v", err)
	}
	if got, want := s.rowhashKey, "csvimp-rowhash-bank"; got != want {
		t.Errorf("rowhashKey = %q, want derived %q", got, want)
	}
}

func TestRowhashKey_InvalidInstanceNameRejected(t *testing.T) {
	sc := shapeConfigFromTOML(t, simpleTOML)
	_, err := validateShape("My Bank", sc) // space: not a valid metadata key fragment
	if err == nil {
		t.Fatal("expected error for an instance name that cannot form a valid metadata key")
	}
	if !strings.Contains(err.Error(), "rowhash_key") {
		t.Errorf("error %q should point the user at rowhash_key", err)
	}
}

func TestRowhashKey_InvalidExplicitKeyRejected(t *testing.T) {
	sc := shapeConfigFromTOML(t, simpleTOML)
	sc.RowhashKey = "Bad Key"
	if _, err := validateShape("bank", sc); err == nil {
		t.Fatal("expected error for an invalid explicit rowhash_key")
	}
}
