package csvbase

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// DefaultRowHashKey is the metadata key used when RowHash.Key is empty.
const DefaultRowHashKey = "csvbase-rowhash"

// RowHash configures idempotency stamping. When set on a Config, the Driver
// stamps every directive a row produces with a stable content hash so a
// re-import can be deduplicated. The hash is computed over the raw record
// fields before the mapper sees them, so mapper transformations do not affect
// the key.
type RowHash struct {
	// Key is the metadata key to stamp. Empty selects DefaultRowHashKey.
	Key string
}

const (
	recordSep = "\x1e"
	unitSep   = "\x1f"
)

// computeHash returns the 16-character lowercase-hex prefix of SHA-256 over
//
//	name || RS || trim(fields[0]) || US || trim(fields[1]) || ...
//
// where RS=0x1e, US=0x1f, trim=strings.TrimSpace. The instance name is
// included so the same raw row under two Driver instances hashes differently.
func computeHash(name string, fields []string) string {
	h := sha256.New()
	h.Write([]byte(name))
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
