package syntax

// NodeKind represents the type of a CST node.
type NodeKind uint16

const (
	FileNode NodeKind = iota // root node containing all directives

	// Undated directives
	OptionDirective  // option "key" "value"
	PluginDirective  // plugin "name" ["config"]
	IncludeDirective // include "path"
	PushtagDirective // pushtag #tag
	PoptagDirective  // poptag #tag

	// Dated directives
	OpenDirective        // open Account [Currency,...] ["Booking"]
	CloseDirective       // close Account
	CommodityDirective   // commodity Currency
	BalanceDirective     // balance Account Number [~ Number] Currency
	PadDirective         // pad Account Account
	NoteDirective        // note Account "description"
	DocumentDirective    // document Account "path"
	PriceDirective       // price Commodity Amount
	EventDirective       // event "name" "value"
	QueryDirective       // query "name" "sql"
	CustomDirective      // custom "type" Value...
	TransactionDirective // txn|*|! ["Payee"] "Narration" ...

	// Sub-nodes
	PostingNode       // [Flag] Account [Amount] [CostSpec] [PriceAnnotation]
	AmountNode        // Number Currency
	BalanceAmountNode // Number [~ Number] Currency (balance directive body)
	CostSpecNode      // {Amount [, Date] [, Label]} or {{Amount}} or {}
	PriceAnnotNode    // @ Amount or @@ Amount
	MetadataLineNode  // key: value
	ArithExprNode     // arithmetic expression (number, unary, binary, or parenthesized)

	// Error recovery
	ErrorNode            // contains tokens from a failed parse
	UnrecognizedLineNode // non-matching line at column 0

	nodeKindCount // sentinel
)

var nodeKindNames = [nodeKindCount]string{
	FileNode:             "FileNode",
	OptionDirective:      "OptionDirective",
	PluginDirective:      "PluginDirective",
	IncludeDirective:     "IncludeDirective",
	PushtagDirective:     "PushtagDirective",
	PoptagDirective:      "PoptagDirective",
	OpenDirective:        "OpenDirective",
	CloseDirective:       "CloseDirective",
	CommodityDirective:   "CommodityDirective",
	BalanceDirective:     "BalanceDirective",
	PadDirective:         "PadDirective",
	NoteDirective:        "NoteDirective",
	DocumentDirective:    "DocumentDirective",
	PriceDirective:       "PriceDirective",
	EventDirective:       "EventDirective",
	QueryDirective:       "QueryDirective",
	CustomDirective:      "CustomDirective",
	TransactionDirective: "TransactionDirective",
	PostingNode:          "PostingNode",
	AmountNode:           "AmountNode",
	BalanceAmountNode:    "BalanceAmountNode",
	CostSpecNode:         "CostSpecNode",
	PriceAnnotNode:       "PriceAnnotNode",
	MetadataLineNode:     "MetadataLineNode",
	ArithExprNode:        "ArithExprNode",
	ErrorNode:            "ErrorNode",
	UnrecognizedLineNode: "UnrecognizedLineNode",
}

// String returns the name of the node kind.
func (k NodeKind) String() string {
	if int(k) < len(nodeKindNames) {
		if name := nodeKindNames[k]; name != "" {
			return name
		}
	}
	return "UNKNOWN"
}
