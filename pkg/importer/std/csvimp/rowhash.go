package csvimp

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	recordSep = "\x1e"
	unitSep   = "\x1f"
)

// rowHash returns the 16-character lowercase-hex prefix of SHA-256 over
//
//	shapeName || RS || trim(fields[0]) || US || trim(fields[1]) || ...
//
// Trimming is leading/trailing Unicode whitespace; no other
// normalisation is applied. The shape name is included so the same raw
// row interpreted under two different shapes hashes differently.
func rowHash(shapeName string, fields []string) string {
	h := sha256.New()
	h.Write([]byte(shapeName))
	h.Write([]byte(recordSep))
	for i, f := range fields {
		if i > 0 {
			h.Write([]byte(unitSep))
		}
		h.Write([]byte(strings.TrimSpace(f)))
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}
