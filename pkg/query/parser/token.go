package parser

// Position identifies a location in the query source. Offset is the 0-based
// byte offset; Line and Column are 1-based, counted in bytes (not runes).
type Position struct {
	Offset int
	Line   int
	Column int
}

// tokenKind enumerates the lexical categories the scanner produces. Keyword
// kinds are recognized case-insensitively; every other kind is determined by
// the lexical shape of the run.
type tokenKind uint8

const (
	tokIllegal tokenKind = iota // unrecognized input
	tokEOF                      // end of input

	// Literals
	tokString  // 'text' or "text"
	tokInt     // 42
	tokDecimal // 4.20, .5, 10.
	tokDate    // 2020-01-01
	tokIdent   // bare identifier / column reference / function name

	// Keywords
	tokSelect
	tokDistinct
	tokAs
	tokFrom
	tokWhere
	tokGroup
	tokOrder
	tokBy
	tokAsc
	tokDesc
	tokLimit
	tokAnd
	tokOr
	tokNot
	tokIn
	tokBetween
	tokIs
	tokTrue
	tokFalse
	tokNull
	tokOpen
	tokClose
	tokClear
	tokOn

	// Operators and punctuation
	tokLParen   // (
	tokRParen   // )
	tokComma    // ,
	tokStar     // *
	tokPlus     // +
	tokMinus    // -
	tokSlash    // /
	tokPercent  // %
	tokEq       // =
	tokNe       // !=
	tokLt       // <
	tokLe       // <=
	tokGt       // >
	tokGe       // >=
	tokTilde    // ~
	tokSemi     // ;
	tokDot      // .
	tokLBracket // [
	tokRBracket // ]

	tokKindCount
)

var tokenKindNames = [tokKindCount]string{
	tokIllegal:  "ILLEGAL",
	tokEOF:      "EOF",
	tokString:   "STRING",
	tokInt:      "INT",
	tokDecimal:  "DECIMAL",
	tokDate:     "DATE",
	tokIdent:    "IDENT",
	tokSelect:   "SELECT",
	tokDistinct: "DISTINCT",
	tokAs:       "AS",
	tokFrom:     "FROM",
	tokWhere:    "WHERE",
	tokGroup:    "GROUP",
	tokOrder:    "ORDER",
	tokBy:       "BY",
	tokAsc:      "ASC",
	tokDesc:     "DESC",
	tokLimit:    "LIMIT",
	tokAnd:      "AND",
	tokOr:       "OR",
	tokNot:      "NOT",
	tokIn:       "IN",
	tokBetween:  "BETWEEN",
	tokIs:       "IS",
	tokTrue:     "TRUE",
	tokFalse:    "FALSE",
	tokNull:     "NULL",
	tokOpen:     "OPEN",
	tokClose:    "CLOSE",
	tokClear:    "CLEAR",
	tokOn:       "ON",
	tokLParen:   "(",
	tokRParen:   ")",
	tokComma:    ",",
	tokStar:     "*",
	tokPlus:     "+",
	tokMinus:    "-",
	tokSlash:    "/",
	tokPercent:  "%",
	tokEq:       "=",
	tokNe:       "!=",
	tokLt:       "<",
	tokLe:       "<=",
	tokGt:       ">",
	tokGe:       ">=",
	tokTilde:    "~",
	tokSemi:     ";",
	tokDot:      ".",
	tokLBracket: "[",
	tokRBracket: "]",
}

func (k tokenKind) String() string {
	if int(k) < len(tokenKindNames) {
		if name := tokenKindNames[k]; name != "" {
			return name
		}
	}
	return "UNKNOWN"
}

// keywords maps the lowercased identifier text to its keyword kind. Lookups
// must lowercase the source run first, since keywords are case-insensitive.
var keywords = map[string]tokenKind{
	"select":   tokSelect,
	"distinct": tokDistinct,
	"as":       tokAs,
	"from":     tokFrom,
	"where":    tokWhere,
	"group":    tokGroup,
	"order":    tokOrder,
	"by":       tokBy,
	"asc":      tokAsc,
	"desc":     tokDesc,
	"limit":    tokLimit,
	"and":      tokAnd,
	"or":       tokOr,
	"not":      tokNot,
	"in":       tokIn,
	"between":  tokBetween,
	"is":       tokIs,
	"true":     tokTrue,
	"false":    tokFalse,
	"null":     tokNull,
	"open":     tokOpen,
	"close":    tokClose,
	"clear":    tokClear,
	"on":       tokOn,
}

// token is one lexical unit. Text is the exact source slice; for strings it
// excludes the surrounding quotes and has escapes resolved.
type token struct {
	kind tokenKind
	text string
	pos  Position
}
