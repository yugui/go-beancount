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
// a [Cells] and may read prior steps' outputs via [Value]. A step returns
// (value, nil, nil) on success, (zero, diag, nil) to soft-fail (attaching
// the [ast.Diagnostic] to the key for downstream steps to inspect), or
// (_,_,err) for a hard error that aborts the row. [Builder.Emit] freezes the
// steps into an immutable [Pipeline] that satisfies [RowMapper].
//
// # Layer 3 — Standard steps and EmitTransaction
//
// Ready-made step constructors cover the common field-resolution patterns:
//
//   - Primitive steps: [Column], [Const], [ParseDate], [SumAmounts], [Split],
//     [Group], [MapValue], [JoinKeys].
//   - Business-logic resolver steps (mirror csvimp semantics): [ResolveAccount],
//     [ResolveCounter], [ResolveCurrency], [ResolvePayee],
//     [NarrationFromColumns], [NarrationFromTemplate], [ResolveCost].
//
// [EmitTransaction] consumes a [TxConfig] of pre-resolved keys and assembles
// the standard primary+counter+cost transaction, handling soft-fail drop/keep
// semantics in one place. It is the canonical emit callback for importers that
// produce one transaction per row.
package csvbase
