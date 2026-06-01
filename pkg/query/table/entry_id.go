package table

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"sort"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

// entryID returns a stable 32-character lowercase hex identifier for a
// directive: the same directive value yields the same id across runs and
// processes, and distinct directives almost certainly differ. The id is a
// purpose-built canonical encoding of the directive's salient fields (its
// type plus the data a reader would consider its identity) hashed with MD5.
//
// The id deliberately excludes metadata and source position (Span), so two
// directives that differ only in their meta or their location in the file
// share an id; collection-valued fields that carry no order (tags, links) are
// sorted before hashing, so reordering them does not change the id. This is a
// stability-and-uniqueness contract, not byte-compatibility with upstream
// beanquery's Python id.
func entryID(d ast.Directive) string {
	e := idHasher{h: md5.New()}
	e.str(directiveTypeName(d))
	switch v := d.(type) {
	case *ast.Transaction:
		e.date(v.Date)
		e.flag(v.Flag)
		e.str(v.Payee)
		e.str(v.Narration)
		e.sortedSet(v.Tags)
		e.sortedSet(v.Links)
		e.int(len(v.Postings))
		for i := range v.Postings {
			p := &v.Postings[i]
			e.flag(p.Flag)
			e.str(string(p.Account))
			e.optAmount(p.Amount)
		}
	case *ast.Open:
		e.date(v.Date)
		e.str(string(v.Account))
		e.sortedSet(v.Currencies)
		e.int(int(v.Booking))
	case *ast.Close:
		e.date(v.Date)
		e.str(string(v.Account))
	case *ast.Commodity:
		e.date(v.Date)
		e.str(v.Currency)
	case *ast.Pad:
		e.date(v.Date)
		e.str(string(v.Account))
		e.str(string(v.PadAccount))
	case *ast.Balance:
		e.date(v.Date)
		e.str(string(v.Account))
		e.amount(v.Amount)
	case *ast.Price:
		e.date(v.Date)
		e.str(v.Commodity)
		e.amount(v.Amount)
	case *ast.Note:
		e.date(v.Date)
		e.str(string(v.Account))
		e.str(v.Comment)
		e.sortedSet(v.Tags)
		e.sortedSet(v.Links)
	case *ast.Document:
		e.date(v.Date)
		e.str(string(v.Account))
		e.str(v.Path)
		e.sortedSet(v.Tags)
		e.sortedSet(v.Links)
	case *ast.Event:
		e.date(v.Date)
		e.str(v.Name)
		e.str(v.Value)
	case *ast.Query:
		e.date(v.Date)
	case *ast.Custom:
		e.date(v.Date)
		e.str(v.TypeName)
	default:
		e.date(d.DirDate())
	}
	var sum [md5.Size]byte
	e.h.Sum(sum[:0])
	return hex.EncodeToString(sum[:])
}

// idHasher feeds length-framed fields into h so that the concatenation is an
// injective encoding (no field boundary is ambiguous).
type idHasher struct {
	h   hash.Hash
	buf [8]byte
}

func (e *idHasher) raw(b []byte) {
	binary.BigEndian.PutUint64(e.buf[:], uint64(len(b)))
	e.h.Write(e.buf[:])
	e.h.Write(b)
}

func (e *idHasher) str(s string) { e.raw([]byte(s)) }

func (e *idHasher) int(n int) {
	binary.BigEndian.PutUint64(e.buf[:], uint64(n))
	e.h.Write(e.buf[:])
}

func (e *idHasher) flag(b byte) { e.h.Write([]byte{b}) }

func (e *idHasher) date(t time.Time) { e.str(t.Format(time.RFC3339Nano)) }

// sortedSet frames count followed by the elements in sorted order, so element
// reordering does not change the hash.
func (e *idHasher) sortedSet(elems []string) {
	sorted := append([]string(nil), elems...)
	sort.Strings(sorted)
	e.int(len(sorted))
	for _, s := range sorted {
		e.str(s)
	}
}

func (e *idHasher) amount(a ast.Amount) {
	e.str(a.Number.Text('f'))
	e.str(a.Currency)
}

// optAmount frames a presence byte then the amount, distinguishing an absent
// amount from a present zero amount.
func (e *idHasher) optAmount(a *ast.Amount) {
	if a == nil {
		e.h.Write([]byte{0})
		return
	}
	e.h.Write([]byte{1})
	e.amount(*a)
}
