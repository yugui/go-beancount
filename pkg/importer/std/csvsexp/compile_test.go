package csvsexp

import (
	"strings"
	"testing"
)

// TestCompileErrors exercises the compiler's type checker, parser, and form
// dispatch. These are package-internal building blocks, but every failure mode
// is observable through the registered factory (newImporter returns the compile
// error), so the tests drive that observable surface rather than the unexported
// evaluator.
func TestCompileErrors(t *testing.T) {
	cases := []struct {
		name    string
		program string
		want    string
	}{
		{
			name:    "unterminated list",
			program: `(csv-import (emit-transaction :date d`,
			want:    "unterminated list",
		},
		{
			name:    "unterminated string",
			program: `(csv-import "oops)`,
			want:    "unterminated string",
		},
		{
			name:    "more than one top form",
			program: `(csv-import) (csv-import)`,
			want:    "more than one top-level form",
		},
		{
			name:    "wrong top form",
			program: `(frobnicate)`,
			want:    "top-level form must be (csv-import ...)",
		},
		{
			name:    "no body",
			program: `(csv-import :match "x")`,
			want:    "has no body form",
		},
		{
			name:    "unknown form",
			program: `(csv-import (let* ((x (frobnicate))) (emit-transaction :date x :amount x)))`,
			want:    `unknown form "frobnicate"`,
		},
		{
			name:    "unbound symbol",
			program: `(csv-import (emit-transaction :date nope :amount nope))`,
			want:    `unbound symbol "nope"`,
		},
		{
			name: "type mismatch: date wants date-key",
			program: `(csv-import (emit-transaction
				:date (parse-amount (column "A"))
				:amount (parse-amount (column "A"))))`,
			want: "expected date-key, got amount-key",
		},
		{
			name: "type mismatch: amount wants amount-key",
			program: `(csv-import (emit-transaction
				:date (parse-date (column "D") "2006-01-02")
				:amount (column "A")))`,
			want: "expected amount-key, got string-key",
		},
		{
			name: "type mismatch: trim wants string-key",
			program: `(csv-import (let* ((x (trim (parse-date (column "D") "2006-01-02"))))
				(emit-transaction :date x :amount x)))`,
			want: "expected string-key, got date-key",
		},
		{
			name:    "arity: column",
			program: `(csv-import (let* ((x (column))) (emit-transaction :date x :amount x)))`,
			want:    "column expects exactly 1 argument",
		},
		{
			name: "emit requires date",
			program: `(csv-import (emit-transaction
				:amount (parse-amount (column "A"))))`,
			want: "requires :date",
		},
		{
			name: "emit requires amount",
			program: `(csv-import (emit-transaction
				:date (parse-date (column "D") "2006-01-02")))`,
			want: "requires :amount",
		},
		{
			name: "cost requires exactly one of per-unit/total",
			program: `(csv-import (let* ((c (cost :per-unit (column "P") :total (column "T") :default-currency "USD")))
				(emit-transaction :date (parse-date (column "D") "2006-01-02")
				  :amount (parse-amount (column "A")) :cost c)))`,
			want: "exactly one of :per-unit or :total",
		},
		{
			name:    "bad regex",
			program: `(csv-import (let* ((s (split (column "C") (regex "(")))) (emit-transaction :date s :amount s)))`,
			want:    "missing closing )",
		},
		{
			name: "columns and header-match exclusive",
			program: `(csv-import
				:columns (("Date" 0))
				:header-match ("Date")
				(emit-transaction :date (parse-date (column "Date") "2006-01-02")
				  :amount (parse-amount (column "A"))))`,
			want: "mutually exclusive",
		},
		{
			name: "if branch type mismatch",
			program: `(csv-import (let* ((acct (if (empty? (column "C"))
				  (const "Assets:Cash")
				  (parse-amount (column "A")))))
				(emit-transaction :date (parse-date (column "D") "2006-01-02")
				  :amount (parse-amount (column "A")) :account acct)))`,
			want: "if branches must have the same type",
		},
		{
			name: "cond requires else clause",
			program: `(csv-import (let* ((acct (cond ((empty? (column "C")) (const "X")))))
				(emit-transaction :date (parse-date (column "D") "2006-01-02")
				  :amount (parse-amount (column "A")) :account acct)))`,
			want: "cond requires a final (else result) clause",
		},
		{
			name: "cond else must be last",
			program: `(csv-import (let* ((acct (cond (else (const "X")) ((empty? (column "C")) (const "Y")))))
				(emit-transaction :date (parse-date (column "D") "2006-01-02")
				  :amount (parse-amount (column "A")) :account acct)))`,
			want: "else must be the last cond clause",
		},
		{
			name: "cond clause must be (test result)",
			program: `(csv-import (let* ((acct (cond ((empty? (column "C")))  (else (const "Y")))))
				(emit-transaction :date (parse-date (column "D") "2006-01-02")
				  :amount (parse-amount (column "A")) :account acct)))`,
			want: "each cond clause must be (test result)",
		},
		{
			name: "cond result type mismatch",
			program: `(csv-import (let* ((acct (cond ((empty? (column "C")) (const "X"))
				  (else (parse-amount (column "A"))))))
				(emit-transaction :date (parse-date (column "D") "2006-01-02")
				  :amount (parse-amount (column "A")) :account acct)))`,
			want: "if branches must have the same type",
		},
		{
			name: "cond test wants bool-key",
			program: `(csv-import (let* ((acct (cond ((column "C") (const "X")) (else (const "Y")))))
				(emit-transaction :date (parse-date (column "D") "2006-01-02")
				  :amount (parse-amount (column "A")) :account acct)))`,
			want: "expected bool-key, got string-key",
		},
		{
			name: "if cond wants bool-key",
			program: `(csv-import (let* ((acct (if (column "C") (const "X") (const "Y"))))
				(emit-transaction :date (parse-date (column "D") "2006-01-02")
				  :amount (parse-amount (column "A")) :account acct)))`,
			want: "expected bool-key, got string-key",
		},
		{
			name: "empty? wants string-key",
			program: `(csv-import (let* ((p (empty? (parse-amount (column "A")))))
				(emit-transaction :date (parse-date (column "D") "2006-01-02")
				  :amount (parse-amount (column "A")))))`,
			want: "expected string-key, got amount-key",
		},
		{
			name: "negative? wants amount-key",
			program: `(csv-import (let* ((p (negative? (column "A"))))
				(emit-transaction :date (parse-date (column "D") "2006-01-02")
				  :amount (parse-amount (column "A")))))`,
			want: "expected amount-key, got string-key",
		},
		{
			name:    "lambda arity",
			program: `(csv-import (let* ((f (lambda (x)))) (emit-transaction :date f :amount f)))`,
			want:    "lambda expects exactly 2 arguments",
		},
		{
			name: "lambda parameter must be symbol",
			program: `(csv-import (let* ((f (lambda ("x") (const "y"))))
				(emit-transaction :date f :amount f)))`,
			want: "lambda parameter must be a symbol",
		},
		{
			name: "function arity mismatch",
			program: `(csv-import (let* ((f (lambda (a b) (const "y")))
				  (acct (f (const "1"))))
				(emit-transaction :date (parse-date (column "D") "2006-01-02")
				  :amount (parse-amount (column "A")) :account acct)))`,
			want: "f expects 2 argument(s), got 1",
		},
		{
			name: "not callable",
			program: `(csv-import (let* ((x (const "v")) (y (x (const "z"))))
				(emit-transaction :date (parse-date (column "D") "2006-01-02")
				  :amount (parse-amount (column "A")) :account y)))`,
			want: `"x" is not callable`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := importerFromProgram(t, "test", tc.program)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestEmptyProgramRejected(t *testing.T) {
	_, err := importerFromProgram(t, "test", "   \n  ")
	if err == nil || !strings.Contains(err.Error(), "program is required") {
		t.Fatalf("got %v, want program-is-required error", err)
	}
}
