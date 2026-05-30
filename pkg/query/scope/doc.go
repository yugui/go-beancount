// Package scope implements the entry-stream pre-pass for BQL FROM-clause
// scoping: OPEN ON, CLOSE ON, and CLEAR.
//
// The public surface is [Spec] (a value describing the scoping parameters)
// and [View] (a pure function returning a filtered directive iterator).
// View operates on an immutable [github.com/yugui/go-beancount/pkg/ast.Ledger]
// and never mutates it. Each call to View returns a fresh iterator; iterators
// share no mutable state, so concurrent calls to View and to the iterators it
// returns are safe without external locking.
package scope
