package syntax

// TokenKind represents the type of a lexical token.
type TokenKind uint16

const (
	ILLEGAL TokenKind = iota // unrecognized input (zero value)

	// Literals
	DATE     // 2024-01-15, 2024/01/15
	NUMBER   // 1234.56, 1,234.56
	STRING   // "hello", "multi\nline"
	ACCOUNT  // Assets:US:BofA:Checking
	CURRENCY // USD, HOOL, VWCE.DE
	TAG      // #trip-2024
	LINK     // ^invoice-123

	// Identifiers
	IDENT // lowercase-starting word; keywords resolved by parser

	// Operators / Punctuation
	PLUS    // +
	MINUS   // -
	STAR    // *
	SLASH   // /
	LPAREN  // (
	RPAREN  // )
	LBRACE  // {
	RBRACE  // }
	LBRACE2 // {{
	RBRACE2 // }}
	AT      // @
	ATAT    // @@
	TILDE   // ~
	COMMA   // ,
	COLON   // :
	BANG    // !
	HASH    // bare #
	CARET   // bare ^

	// Structural
	EOF // end of file

	tokenKindCount // sentinel for iteration
)

var tokenKindNames = [tokenKindCount]string{
	ILLEGAL:  "ILLEGAL",
	DATE:     "DATE",
	NUMBER:   "NUMBER",
	STRING:   "STRING",
	ACCOUNT:  "ACCOUNT",
	CURRENCY: "CURRENCY",
	TAG:      "TAG",
	LINK:     "LINK",
	IDENT:    "IDENT",
	PLUS:     "PLUS",
	MINUS:    "MINUS",
	STAR:     "STAR",
	SLASH:    "SLASH",
	LPAREN:   "LPAREN",
	RPAREN:   "RPAREN",
	LBRACE:   "LBRACE",
	RBRACE:   "RBRACE",
	LBRACE2:  "LBRACE2",
	RBRACE2:  "RBRACE2",
	AT:       "AT",
	ATAT:     "ATAT",
	TILDE:    "TILDE",
	COMMA:    "COMMA",
	COLON:    "COLON",
	BANG:     "BANG",
	HASH:     "HASH",
	CARET:    "CARET",
	EOF:      "EOF",
}

// String returns the name of the token kind.
func (k TokenKind) String() string {
	if int(k) < len(tokenKindNames) {
		if name := tokenKindNames[k]; name != "" {
			return name
		}
	}
	return "UNKNOWN"
}
