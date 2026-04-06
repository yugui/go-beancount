package syntax

import "testing"

func TestNodeKindString_AllNonEmpty(t *testing.T) {
	for k := NodeKind(0); k < nodeKindCount; k++ {
		name := k.String()
		if name == "" || name == "UNKNOWN" {
			t.Errorf("NodeKind(%d).String() = %q, want a non-empty name", k, name)
		}
	}
}

func TestNodeKindString_SpecificValues(t *testing.T) {
	tests := []struct {
		kind NodeKind
		want string
	}{
		{FileNode, "FileNode"},
		{OptionDirective, "OptionDirective"},
		{TransactionDirective, "TransactionDirective"},
		{PostingNode, "PostingNode"},
		{AmountNode, "AmountNode"},
		{ErrorNode, "ErrorNode"},
		{UnrecognizedLineNode, "UnrecognizedLineNode"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("NodeKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestNodeKindString_OutOfRange(t *testing.T) {
	k := NodeKind(9999)
	if got := k.String(); got != "UNKNOWN" {
		t.Errorf("NodeKind(9999).String() = %q, want %q", got, "UNKNOWN")
	}
}
