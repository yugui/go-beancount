package csvbase

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
)

// DefaultRowHashKey is the metadata key used when a RowHash resolves an empty
// key (a nil KeyFunc, or one returning "").
const DefaultRowHashKey = "csvbase-rowhash"

// RowHash configures idempotency stamping. When set on a Config, the Driver
// stamps every directive a row produces with a stable content hash so a
// re-import can be deduplicated. The hash is computed over the raw record
// fields before the mapper sees them, so mapper transformations do not affect
// the value. The metadata key is resolved per row by KeyFunc, which lets a
// caller namespace or vary the key by data; a constant key is wrapped with
// StaticRowHashKey.
type RowHash struct {
	// KeyFunc resolves the metadata key for one row. A nil KeyFunc, or one
	// returning "", selects DefaultRowHashKey.
	KeyFunc func(RowContext) string
}

// StaticRowHashKey wraps a constant key as a RowHash.KeyFunc, for callers that
// do not vary the key per row.
func StaticRowHashKey(key string) func(RowContext) string {
	return func(RowContext) string { return key }
}

// RowHashValue returns a Key whose per-row value is the same content hash that
// RowHash stamps: a stable hash over the raw record fields namespaced by name.
// Use it to place the hash under a caller-chosen metadata key (for example via
// Meta) for per-directive idempotency stamping.
func RowHashValue(b *Builder, name string) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		return computeHash(name, c.raw), nil, nil
	})
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
