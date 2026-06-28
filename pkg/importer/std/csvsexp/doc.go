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
//	:rowhash      "csvsexp-rowhash"        stamp an idempotency hash under this key on every directive
//
// :rowhash stamps the hash globally on every directive a row emits. For
// per-directive control — choosing the key at construction, or stamping only
// some directives — omit :rowhash and place the (rowhash) form's value under a
// key with (meta ...) instead. Using a distinct key per instance avoids the
// dedup veto that a shared key would trigger across sources (see csvimp's
// "Identity metadata").
//
// BODY is a single (let* (BINDINGS) BODY), (emit-transaction ...), or
// (emit ...) form. let* binds names sequentially in a fresh lexical scope; each
// binding may reference the ones before it, and the body sees them all.
// emit-transaction is the convenience for the primary+counter shape; emit takes
// zero or more transaction, balance, or directive keys built from the
// construction forms below and is the way to produce three-or-more-posting,
// auto-balanced, or posting-annotated entries, balance assertions, and rows that
// yield several directives at once (or none).
//
// # Forms
//
// Leaves and transforms map onto csvbase steps of the same role:
//
//	(column "N")                       raw cell of column N            -> string-key
//	(row)                              the whole row as a map          -> row-key
//	(rowhash)                          this row's content hash          -> string-key
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
//	(date-offset d N)                  shift a date by N days, N<0 back -> date-key
//	(parse-amount k :format nf :split-currency #t :code "C")          -> amount-key
//	(negate-amount a)                                                 -> amount-key
//	(add-amounts a b "CODE")                                          -> amount-key
//	(scale-amount a S "CODE")          multiply by scalar S          -> amount-key
//	(divide-amount a S :scale N :code "C")  divide by scalar S       -> amount-key
//	(round-amount a N) (floor-amount a N) (ceil-amount a N)          -> amount-key
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
// # Transaction construction
//
// These forms assemble an arbitrary transaction, decoupling construction from
// emission so a program can express any grammatically valid entry. emit-transaction
// remains the convenience for the common primary+counter shape:
//
//	(amount amt :currency cur)         amount-key + currency            -> amount-value-key
//	(require-amount amt "CODE")        amount-key in, soft-fail if nil  -> amount-key
//	(price amt :currency cur :total #t)  @ (or @@ when :total) annotation -> price-key
//	(posting :account k :amount av :cost ck :price pk :flag "!" :meta (("n" k))) -> posting-key
//	(postings p1 p2 p3 ...)            gather posting legs              -> posting-list-key
//	(double-entry primary counter)     primary + balancing counter leg  -> posting-list-key
//	(tags a b ...) (links a b ...)     gather non-blank strings         -> string-list-key
//	(meta ("name" k) ...)              string metadata                  -> metadata-key
//	(transaction :date d :postings pl :flag "x" :payee p :narration n
//	             :tags t :links l :meta m)                              -> transaction-key
//	(balance :date d :account a :amount av :meta m)  balance assertion   -> balance-key
//	(directive X)                      lift a transaction/balance key    -> directive-key
//	(emit X ...)                       body terminal; each X is a transaction,
//	                                   balance, or directive key. Emits one
//	                                   directive per non-nil argument, in order;
//	                                   a nil argument contributes nothing, and a
//	                                   soft-failed argument drops the whole row.
//
// In (posting ...), :amount takes an amount-value-key (from (amount ...)); a
// missing :amount yields an auto-balanced posting. (double-entry ...) reproduces
// emit-transaction's counter handling: a negated counter amount (or an elided
// cash leg when the primary carries a cost), and a soft-failed counter account
// surfaces a warning while keeping the row's single posting.
//
// Rows that produce different directive kinds (some transactions, some balances)
// are unified by wrapping each branch in (directive ...) so an (if ...) yields a
// single directive-key, which (emit ...) emits. A nil-valued directive
// contributes no output; when emit's every argument is nil the row is skipped.
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
// (cond (test result)... (else result)) is the multi-way conditional: the tests
// are bool-keys tried in order, the first true clause's result is chosen, and the
// mandatory final (else result) clause supplies the value when none hold. It
// desugars to right-nested if, so every result must share one runtime key kind
// and a literal test folds at compile time.
//
//	(account (cond ((negative? amt)        (const "Expenses:Misc"))
//	               ((matches? payee (regex "(?i)salary")) (const "Income:Salary"))
//	               (else                   (const "Income:Misc"))))
//
// (when cond X) and (unless cond X) gate a single directive: they yield X's
// directive when the condition holds (fails) and a nil directive otherwise. As a
// directive-key they slot directly into a variadic emit, so a row can carry an
// optional second entry or be conditionally suppressed without an else branch.
//
//	(emit txn (when (positive? amt) (balance :date d :account a :amount av)))
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
// emit-transaction wires resolved keys into one transaction per row, assembling
// a primary posting plus an optional balancing counter posting: :date and
// :amount are required, while :currency, :account, :counter, :payee, :narration,
// :cost, :flag, :tags, :links, and :meta are optional.
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
