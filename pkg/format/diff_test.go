package format_test

import (
	"fmt"
	"strings"
)

// diffStrings returns a human-readable description of the first difference
// between two strings a and b, labeled with aLabel and bLabel.
func diffStrings(a, b, aLabel, bLabel string) string {
	aLines := strings.Split(a, "\n")
	bLines := strings.Split(b, "\n")

	n := len(aLines)
	if len(bLines) < n {
		n = len(bLines)
	}

	for i := 0; i < n; i++ {
		if aLines[i] != bLines[i] {
			return fmt.Sprintf("first difference at line %d:\n  %s:  %q\n  %s:  %q", i+1, aLabel, aLines[i], bLabel, bLines[i])
		}
	}

	if len(aLines) != len(bLines) {
		return fmt.Sprintf("%s has %d lines, %s has %d lines", aLabel, len(aLines), bLabel, len(bLines))
	}

	// Lines match but strings differ — find byte offset.
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return fmt.Sprintf("first byte difference at offset %d: %s=%q %s=%q", i, aLabel, a[i], bLabel, b[i])
		}
	}

	if len(a) != len(b) {
		return fmt.Sprintf("strings differ in length: %s=%d bytes, %s=%d bytes", aLabel, len(a), bLabel, len(b))
	}

	return ""
}
