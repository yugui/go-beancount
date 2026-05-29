// Package parser turns a BQL (Beancount Query Language) query string into an
// untyped syntax tree rooted at [Select]. It is a hand-written scanner plus
// recursive-descent parser; it does no type checking, column or table
// resolution, or overload resolution — those belong to a later compilation
// step. The package depends only on the standard library and
// [github.com/cockroachdb/apd/v3] (for decimal literals); it does not import
// the rest of the query engine.
//
// # Statement grammar
//
// The lean subset supports a single SELECT statement:
//
//	SELECT [DISTINCT] (<target-list> | '*')
//	       [FROM (<table-name> | <bool-expr>)]
//	       [WHERE <expr>]
//	       [GROUP BY <expr-list>]
//	       [ORDER BY <order-item-list>]
//	       [LIMIT <integer>]
//
// A target is an expression with an optional `AS <identifier>` alias. An
// order item is an expression with an optional ASC (default) or DESC.
//
// # FROM stays catalog-free
//
// FROM content is parsed as an expression. [FromClause] additionally records
// whether that expression was exactly one bare identifier (IsBareName). The
// parser does not decide whether the identifier names a table or is a
// single-column filter; the compiler, which owns the table registry, makes
// that call.
//
// # Expression precedence
//
// Lowest to highest, matching beanquery:
//
//	OR
//	AND
//	NOT            (prefix)
//	comparison     = != < <= > >= ~  and  IN   (non-associative)
//	additive       + -
//	multiplicative * / %
//	unary sign     - +              (prefix)
//	primary        literal | column ref | func call | (expr) | (e1, e2, ...)
//
// All binary operators are left-associative except comparison, which is
// non-associative: a chained comparison such as `a = b = c` (or `a < b IN c`)
// is a parse error.
//
// # Lexical conventions
//
// Keywords are case-insensitive. Identifiers are
// `[A-Za-z_][A-Za-z0-9_]*` and serve as column references, function names,
// and aliases. String literals use single or double quotes; the quote
// character is escaped either by doubling it or with a backslash, and a
// backslash escapes the following byte literally (there are no C-style
// escape sequences such as \n). Integers contain no '.'; decimals contain a
// '.' (`.5` and `10.` are accepted) and parse to an exact apd.Decimal.
//
// A run matching exactly `\d{4}-\d{2}-\d{2}` is a single date token, so
// `2020-01-01` is one DateLit while `2020 - 1 - 1` is subtraction. The token
// `*` is multiplication inside expressions; it is the select-all target only
// in the SELECT target position, where the parser handles it specially.
//
// Whitespace separates tokens and is otherwise insignificant. There are no
// comments.
//
// # Errors
//
// [Parse] returns an [*Error] carrying a source [Position] for every lexical
// or syntactic failure and never panics, even on truncated or otherwise
// malformed input.
package parser
