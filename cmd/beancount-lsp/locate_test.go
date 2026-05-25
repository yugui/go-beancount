package main

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/syntax"
)

// locateSrc has two spaces between "open" and "Assets:Bank" so that there is
// a true whitespace-only byte that falls outside any token's [Pos, End()] range
// under the inclusive-end convention (End() = Pos + len(Raw)).
const locateSrc = "2024-01-01 open  Assets:Bank USD\n"

// locateOffset returns the byte offset of the first occurrence of substr in locateSrc.
func locateOffset(t *testing.T, substr string) int {
	t.Helper()
	for i := 0; i+len(substr) <= len(locateSrc); i++ {
		if locateSrc[i:i+len(substr)] == substr {
			return i
		}
	}
	t.Fatalf("LocateAt: %q not found in test source", substr)
	return -1
}

func TestLocateAt_FindsToken(t *testing.T) {
	file := syntax.Parse(locateSrc)
	offset := locateOffset(t, "Assets:Bank") + 3 // mid-token
	loc := LocateAt(file, offset)
	if loc.Token == nil {
		t.Fatal("LocateAt: expected non-nil Token for account in open directive")
	}
	if loc.Token.Kind != syntax.ACCOUNT {
		t.Errorf("LocateAt: Token.Kind = %v, want ACCOUNT", loc.Token.Kind)
	}
	if loc.Token.Raw != "Assets:Bank" {
		t.Errorf("LocateAt: Token.Raw = %q, want %q", loc.Token.Raw, "Assets:Bank")
	}
}

func TestLocateAt_Boundaries(t *testing.T) {
	file := syntax.Parse(locateSrc)
	start := locateOffset(t, "Assets:Bank")
	end := start + len("Assets:Bank")

	for _, off := range []int{start, start + 1, end - 1, end} {
		loc := LocateAt(file, off)
		if loc.Token == nil {
			t.Errorf("LocateAt: offset %d (boundary): Token is nil, want non-nil", off)
			continue
		}
		if loc.Token.Raw != "Assets:Bank" {
			t.Errorf("LocateAt: offset %d: Token.Raw = %q, want %q", off, loc.Token.Raw, "Assets:Bank")
		}
	}
}

func TestLocateAt_BetweenTokens(t *testing.T) {
	// locateSrc has "open  Assets:Bank" (two spaces). "open" ends at offset 15
	// (inclusive-end = End() = 15). "Assets:Bank" starts at 17. Offset 16
	// (the second space) falls outside any token's [Pos, End()] range and
	// must return a nil Token.
	file := syntax.Parse(locateSrc)
	// Locate the second space: one past End() of "open" = 16.
	openEnd := locateOffset(t, "open") + len("open") // = 15
	off := openEnd + 1                               // = 16, second space
	loc := LocateAt(file, off)
	if loc.Token != nil {
		t.Errorf("LocateAt: whitespace offset %d: Token = %q, want nil", off, loc.Token.Raw)
	}
}

func TestLocateAt_PastEOF(t *testing.T) {
	file := syntax.Parse(locateSrc)
	loc := LocateAt(file, len(locateSrc)+100)
	if loc.Token != nil {
		t.Errorf("LocateAt: past-EOF: Token = %v, want nil", loc.Token)
	}
	if loc.Directive != nil {
		t.Errorf("LocateAt: past-EOF: Directive = %v, want nil", loc.Directive)
	}
}

func TestLocateAt_TopLevelDirective(t *testing.T) {
	file := syntax.Parse(locateSrc)
	off := locateOffset(t, "Assets:Bank")
	loc := LocateAt(file, off)
	if loc.Directive == nil {
		t.Fatal("LocateAt: Directive is nil, want top-level OpenDirective node")
	}
	if loc.Directive.Kind != syntax.OpenDirective {
		t.Errorf("LocateAt: Directive.Kind = %v, want OpenDirective", loc.Directive.Kind)
	}
}

func TestLocateAt_NilFile(t *testing.T) {
	loc := LocateAt(nil, 0)
	if loc.Token != nil || loc.Directive != nil {
		t.Errorf("LocateAt: nil file: got %+v, want zero Located", loc)
	}
}
