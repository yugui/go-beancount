package parser

import (
	"time"

	"github.com/cockroachdb/apd/v3"
)

// Select is the untyped syntax tree for a single SELECT statement. The parser
// performs no type checking, column resolution, or overload resolution; every
// field carries source text and positions for a later compilation step.
type Select struct {
	// Distinct reports whether the SELECT carried the DISTINCT keyword.
	Distinct bool
	// Star reports whether the target list was the literal '*'. When true,
	// Targets is empty.
	Star bool
	// Targets are the projection items, in source order. Empty when Star.
	Targets []Target
	// From is the FROM clause, or nil when absent.
	From *FromClause
	// Where is the WHERE predicate, or nil when absent.
	Where Expr
	// GroupBy holds the GROUP BY expressions, or nil/empty when absent.
	GroupBy []Expr
	// OrderBy holds the ORDER BY items, or nil/empty when absent.
	OrderBy []OrderItem
	// Limit is the LIMIT value, or nil when absent.
	Limit *int64
	// Pos is the position of the SELECT keyword.
	Pos Position
}

// Target is one projection item: an expression with an optional AS alias.
type Target struct {
	Expr Expr
	// As is the alias identifier, or "" when no AS clause was given.
	As  string
	Pos Position
}

// OrderItem is one ORDER BY element: an expression with a sort direction.
type OrderItem struct {
	Expr Expr
	// Desc reports a DESC sort; ASC (the default) is Desc == false.
	Desc bool
	Pos  Position
}

// FromClause records the raw FROM content. The parser stays catalog-free: it
// parses the content as an expression and flags whether it was exactly one
// bare identifier. A later compilation step decides whether that identifier
// names a table or is a single-column filter expression.
type FromClause struct {
	// Expr is the parsed FROM expression. When IsBareName it is a *ColumnRef.
	// Nil when FROM carried no filter expression (e.g. "FROM OPEN ON D").
	Expr Expr
	// Name is the identifier text when IsBareName, otherwise "".
	Name string
	// IsBareName is true iff FROM was exactly one bare identifier.
	IsBareName bool
	// Scoping is the optional OPEN/CLOSE/CLEAR suffix, nil when absent.
	Scoping *Scoping
	Pos     Position
}

// Scoping holds the optional entry-stream scoping directives that may follow
// the filter expression in a FROM clause:
//
//	FROM [expr] [OPEN ON date] [CLOSE ON date] [CLEAR]
//
// A nil pointer on FromClause.Scoping means no scoping was specified. Within a
// non-nil Scoping, nil Open or Close means that clause was absent; Clear
// distinguishes "CLEAR present" from "CLEAR absent".
type Scoping struct {
	Open  *time.Time
	Close *time.Time
	Clear bool
	// Pos is the position of the first scoping keyword (OPEN, CLOSE, or CLEAR).
	Pos Position
}

// Expr is the sealed interface implemented by every expression node. Pos
// returns the node's source position for diagnostics.
type Expr interface {
	Pos() Position
	exprNode()
}

// ColumnRef is a bare identifier naming a column.
type ColumnRef struct {
	Name     string
	Position Position
}

// StringLit is a string literal with quotes removed and escapes resolved.
type StringLit struct {
	Value    string
	Position Position
}

// IntLit is an integer literal.
type IntLit struct {
	Value    int64
	Position Position
}

// DecimalLit is a decimal literal parsed into an exact apd.Decimal.
type DecimalLit struct {
	Value    apd.Decimal
	Position Position
}

// DateLit is a YYYY-MM-DD date literal as UTC midnight.
type DateLit struct {
	Value    time.Time
	Position Position
}

// BoolLit is a TRUE or FALSE literal.
type BoolLit struct {
	Value    bool
	Position Position
}

// NullLit is the NULL literal.
type NullLit struct {
	Position Position
}

// UnaryOp identifies a prefix operator.
type UnaryOp uint8

const (
	OpNeg UnaryOp = iota // -
	OpPos                // +
	OpNot                // NOT
)

func (op UnaryOp) String() string {
	switch op {
	case OpNeg:
		return "-"
	case OpPos:
		return "+"
	case OpNot:
		return "NOT"
	default:
		return "UNKNOWN"
	}
}

// Unary is a prefix-operator expression.
type Unary struct {
	Op       UnaryOp
	X        Expr
	Position Position
}

// BinaryOp identifies a binary operator.
type BinaryOp uint8

const (
	OpOr    BinaryOp = iota // OR
	OpAnd                   // AND
	OpAdd                   // +
	OpSub                   // -
	OpMul                   // *
	OpDiv                   // /
	OpMod                   // %
	OpEq                    // =
	OpNe                    // !=
	OpLt                    // <
	OpLe                    // <=
	OpGt                    // >
	OpGe                    // >=
	OpMatch                 // ~
)

func (op BinaryOp) String() string {
	switch op {
	case OpOr:
		return "OR"
	case OpAnd:
		return "AND"
	case OpAdd:
		return "+"
	case OpSub:
		return "-"
	case OpMul:
		return "*"
	case OpDiv:
		return "/"
	case OpMod:
		return "%"
	case OpEq:
		return "="
	case OpNe:
		return "!="
	case OpLt:
		return "<"
	case OpLe:
		return "<="
	case OpGt:
		return ">"
	case OpGe:
		return ">="
	case OpMatch:
		return "~"
	default:
		return "UNKNOWN"
	}
}

// Binary is a binary-operator expression with left and right operands.
type Binary struct {
	Op       BinaryOp
	L, R     Expr
	Position Position
}

// In is the membership test X IN List. List is typically a *ListLit but may be
// any expression. Neg reports the negated form X NOT IN List.
type In struct {
	X        Expr
	List     Expr
	Neg      bool
	Position Position
}

// IsNull is the null test X IS NULL, or its negation X IS NOT NULL when Neg.
// Unlike a comparison against NULL (which yields NULL), it always evaluates to
// a definite boolean.
type IsNull struct {
	X        Expr
	Neg      bool
	Position Position
}

// ListLit is a parenthesized comma-separated list of expressions, used as the
// right operand of IN.
type ListLit struct {
	Elems    []Expr
	Position Position
}

// FuncCall is a function invocation with zero or more argument expressions.
type FuncCall struct {
	Name     string
	Args     []Expr
	Position Position
}

func (e *ColumnRef) Pos() Position  { return e.Position }
func (e *StringLit) Pos() Position  { return e.Position }
func (e *IntLit) Pos() Position     { return e.Position }
func (e *DecimalLit) Pos() Position { return e.Position }
func (e *DateLit) Pos() Position    { return e.Position }
func (e *BoolLit) Pos() Position    { return e.Position }
func (e *NullLit) Pos() Position    { return e.Position }
func (e *Unary) Pos() Position      { return e.Position }
func (e *Binary) Pos() Position     { return e.Position }
func (e *In) Pos() Position         { return e.Position }
func (e *IsNull) Pos() Position     { return e.Position }
func (e *ListLit) Pos() Position    { return e.Position }
func (e *FuncCall) Pos() Position   { return e.Position }

func (*ColumnRef) exprNode()  {}
func (*StringLit) exprNode()  {}
func (*IntLit) exprNode()     {}
func (*DecimalLit) exprNode() {}
func (*DateLit) exprNode()    {}
func (*BoolLit) exprNode()    {}
func (*NullLit) exprNode()    {}
func (*Unary) exprNode()      {}
func (*Binary) exprNode()     {}
func (*In) exprNode()         {}
func (*IsNull) exprNode()     {}
func (*ListLit) exprNode()    {}
func (*FuncCall) exprNode()   {}
