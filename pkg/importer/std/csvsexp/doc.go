// Package csvsexp is an experimental CSV/TSV importer whose configuration is a
// single S-expression program rather than a tree of TOML fields. It registers
// an [importer.Factory] under the kind "csv-sexp"; each factory call compiles
// one program into a fully-configured importer for a single CSV/TSV shape.
//
// Where csvimp exposes the [github.com/yugui/go-beancount/pkg/importer/std/csvbase]
// pipeline indirectly through flat TOML fields, csvsexp exposes the csvbase
// combinators directly: each form maps roughly one-to-one onto a csvbase step,
// and let* names the intermediate Keys. The whole program — reader options and
// pipeline alike — lives in one TOML string field:
//
//	kind = "csv-sexp"
//	name = "mybank"
//	program = """
//	  (csv-import
//	    :match      "mybank.*\\.csv$"
//	    :number     (number-format :thousands-sep ",")
//	    (let* ((date (parse-date (column "Date") "2006-01-02"))
//	           (amt  (parse-amount (column "Amount") :split-currency #t))
//	           (acct (or-else (hint "account") (const "Assets:Checking")))
//	           (cur  (coalesce (currency-hint amt) (const "USD")))
//	           (narr (join-keys " / "
//	                   (trim (column "Description"))
//	                   (trim (column "Memo")))))
//	      (emit-transaction
//	        :date date :amount amt :currency cur :account acct
//	        :payee (column "Payee") :narration narr)))
//	"""
//
// # Syntax
//
// The reader accepts Scheme-flavoured S-expressions: parenthesised lists,
// double-quoted strings (with \n \t \r \" \\ escapes), integers, the booleans
// #t and #f, symbols, and ; line comments. Keyword arguments are written
// :name — a convenience that is deliberately not R7RS Scheme. Form names that
// would collide with core Scheme procedures or auxiliary syntax are renamed
// (see below), so the surface borrows Scheme's reader without redefining its
// vocabulary.
//
// # Top-level form
//
// The program is one (csv-import OPTIONS... BODY) form. Recognised options:
//
//	:match        "regex"                 path gate (combined with the ext/MIME gate)
//	:delimiter    ","                     field separator; one rune
//	:encoding     "Shift_JIS"             IANA charset decoded to UTF-8
//	:skip-lines   1                       banner lines before the header
//	:header-match ("Date" "Amount")       locate a header past a variable banner
//	:columns      (("Date" 0) ("Amt" 3))  headerless column index (exclusive with :header-match)
//	:number       (number-format ...)     default number format for parse-amount and cost
//	:exclude      ((exclude :match "^Total") (exclude :col "Date" :match "^※"))
//	:rowhash      "csvsexp-rowhash"        stamp an idempotency hash under this key
//
// BODY is a single (let* (BINDINGS) BODY) or (emit-transaction ...) form. let*
// binds names sequentially in a fresh lexical scope; each binding may reference
// the ones before it, and the body sees them all.
//
// # Forms
//
// Leaves and transforms map onto csvbase steps of the same role:
//
//	(column "N")                       raw cell of column N            -> string-key
//	(row)                              the whole row as a map          -> row-key
//	(const "x")                        constant string                 -> string-key
//	(hint "account")                   caller Hints[name]              -> string-key
//	(trim k)                                                           -> string-key
//	(required k "CODE")                soft-fail when blank            -> string-key
//	(coalesce a b ...)                 first non-blank                 -> string-key
//	(or-else primary fallback)         primary unless blank            -> string-key
//	(join-keys "sep" a b ...)                                          -> string-key
//	(map-value k (dict ...) :strict "CODE")                           -> string-key
//	(diag-as-warning k "CODE")                                        -> string-key
//	(parse-date k "layout" "CODE")                                    -> date-key
//	(parse-amount k :format nf :split-currency #t :code "C")          -> amount-key
//	(negate-amount a)                                                 -> amount-key
//	(add-amounts a b "CODE")                                          -> amount-key
//	(currency-hint a)                                                 -> string-key
//	(split k (regex "..."))            named groups of a match         -> row-key
//	(group split "name")                                              -> string-key
//	(merge base (bindings ("n" k) ...))                              -> row-key
//	(template "src" data)             text/template over a row map     -> string-key
//	(cost :per-unit k :default-currency "USD" :date k :date-format "..." ...) -> cost-key
//	(regex "pattern")                                                 -> regex
//	(dict ("k" "v") ...)              translation table                -> dict
//	(number-format :thousands-sep "," :decimal-sep "." :placeholders ("-"))
//	:strict / :verbatim              map-lookup modes
//
// Predicates yield a bool-key and arithmetic extends the amount steps:
//
//	(empty? k)                        blank after trim                 -> bool-key
//	(equal? a b)                      exact string equality            -> bool-key
//	(matches? k (regex "..."))        regex match                      -> bool-key
//	(and a b ...) (or a b ...) (not x)                                 -> bool-key
//	(negative? amt) (positive? amt) (zero? amt)                        -> bool-key
//	(amount<? a b "CODE") (amount>? ...) (amount=? ...) same-currency   -> bool-key
//	(sub-amounts a b "CODE")                                          -> amount-key
//	(abs-amount a)                                                    -> amount-key
//
// # Conditionals and functions
//
// (if cond then else) chooses a value per row: cond is a bool-key (a literal
// #t/#f folds at compile time), and the two branches must share one runtime key
// kind, which becomes the result kind. Only the chosen branch's value and
// diagnostic propagate, so a soft-fail in the untaken branch is harmless.
//
//	(account (if (negative? amt) (const "Expenses:Misc") (const "Income:Misc")))
//
// (lambda (params...) body) is a compile-time, macro-style function: bind it
// with let* and apply it as (f args...). Each application re-evaluates body with
// the arguments bound to the parameters, emitting a fresh set of pipeline steps,
// so functions factor out repeated sub-pipelines. Recursion is not supported —
// a function cannot see its own let* name.
//
//	(let* ((mapped (lambda (col table)
//	                 (map-value (trim col) table :strict "csvsexp-unmapped"))))
//	  (emit-transaction ... :account (mapped (column "Category") (dict ...))))
//
// emit-transaction wires resolved keys into one transaction per row; its
// keywords mirror csvbase.TxConfig: :date and :amount are required, while
// :currency, :account, :counter, :payee, :narration, :cost, :flag, :tags,
// :links, :meta, and the :missing-*-code overrides are optional.
//
// # Naming differences from Scheme
//
//   - dict is the dictionary literal (Scheme's map is the list combinator).
//   - or-else is the left-biased fallback (else is reserved in cond/case).
//   - required marks a mandatory field (require is Racket's module form).
//   - map-lookup modes are the keywords :strict and :verbatim.
//
// # Relationship to csvimp
//
// csvsexp reproduces csvimp's feature set (dates, multi-column and
// debit/credit amounts, currency resolution, account and counter-account
// mapping, payee, narration via columns or templates, split groups, lot cost,
// row exclusion, header location, encoding, number formats) by composing the
// same csvbase steps. Diagnostics carry the same csvbase-* codes.
//
// # Concurrency
//
// The importer's state is frozen at construction. Identify and Extract are safe
// for concurrent invocation on the same value.
package csvsexp

import "github.com/yugui/go-beancount/pkg/importer"

func init() {
	importer.RegisterFactory("csv-sexp", importer.FactoryFunc(newImporter))
}
