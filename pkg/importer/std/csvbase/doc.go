// Package csvbase provides a three-layer framework for building CSV/TSV
// importers that produce beancount directives.
//
// # Layer 1 — Driver
//
// [Driver] implements [importer.Importer]. Callers supply per-row logic via
// [RowMapper] ([MapperFunc] adapts a plain function, [Pipeline] is the
// composable form). [RowHash] enables idempotency stamping. [Gate],
// [DefaultGate], [PathMatch], and [AllGates] control which files the Driver
// claims in Identify. Underlying CSV/TSV parsing is handled by csvkit.
//
// # Layer 2 — Pipeline (Builder / AddStep / Key / Value)
//
// [Builder] accumulates typed build steps; [AddStep] registers one step and
// returns a [Key]. During [Pipeline.Map] each step's eval function receives
// a [MappingState] and may read prior steps' outputs via [Value]. A step returns
// (value, nil, nil) on success, (zero, diag, nil) to soft-fail (attaching
// the [ast.Diagnostic] to the key for downstream steps to inspect), or
// (_,_,err) for a hard error that aborts the row. [Builder.Emit] freezes the
// steps into an immutable [Pipeline] that satisfies [RowMapper].
//
// # Layer 3 — Standard steps and transaction construction
//
// Ready-made step constructors cover the common field-resolution patterns:
//
//   - Column reading: [Column], [Columns], [Row], [Split], [SplitColumns], [Group].
//   - Value construction: [Const], [Hint], [JoinKeys], [Trim], [MapValue], [MapEach].
//   - Flow control: [Coalesce], [Else], [Require], [DiagAsWarning].
//   - Parsing: [ParseDate], [ParseAmount], [NegateAmount], [AddAmounts], [CurrencyHint].
//   - Templating: [Merge], [Template].
//
// Callers compose these primitives to express any resolution logic. For cases
// not covered by the primitives, [AddStep] registers an arbitrary typed step.
//
// Transaction construction is itself expressed as steps, so a pipeline can build
// any grammatically valid transaction (three or more postings, auto-balanced
// postings, per-posting metadata): [Amount] forms a posting amount, [Posting]
// builds one posting from a [PostingSpec], [Postings] gathers a posting list (or
// [DoubleEntry] for the common primary+counter shape), [StringList] and [Meta]
// build tags/links and metadata, and [Transaction] assembles a
// [TxnSpec] into a transaction key. [EmitTx] is the terminal emit callback that
// emits that key's transaction, surfacing any warnings recorded via
// [MappingState.Warn].
//
// Postings may carry a price annotation built with [Price]. Beyond transactions,
// [Balance] builds a balance-assertion key; [AsDirective] lifts a transaction or
// balance key into a [Key] of [ast.Directive] so rows producing different
// directive kinds can be unified, and [EmitDirectives] is the terminal that emits
// any number of such directive keys per row.
//
// # Leaf-only invariant
//
// The only steps that read raw row cells ([MappingState.At] / [MappingState.Row]
// / [Builder.Require]) are leaves: [Column] (and the [Columns] / [SplitColumns]
// conveniences built on it), [Row] (the whole row as a map), and any third-party
// step a caller writes via [AddStep]. Every standard resolver step — [Template]
// and [Merge] included — takes [Key] source(s) and returns a [Key], so values
// from raw columns, split groups, maps, joins, and other steps are
// interchangeable.
package csvbase
