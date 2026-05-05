// Package comment recognizes commented-out beancount directives in source
// text and emits [ast.Directive] values back as commented-out blocks.
//
// A commented-out directive is a contiguous run of `;`-prefixed lines whose
// first line begins with `;`, optional ASCII spaces and tabs, and a
// YYYY-MM-DD date. The shared prefix of `;` plus that whitespace is the
// block's Indent; every continuation line must begin with the same byte
// sequence to remain part of the block. Once collected, the recognizer
// strips the Indent and tries to parse the result via [ast.Load], shrinking
// the tail one line at a time until either some prefix parses to at least
// one directive (the block is reported) or the candidate is exhausted (the
// block is treated as plain comment text and silently dropped).
//
// A recognized block can be re-emitted by passing its captured Indent back
// to [Emit]:
//
//	for _, b := range comment.Extract(src, path) {
//	    if err := comment.Emit(w, b.Directive, b.Indent); err != nil {
//	        return err
//	    }
//	}
//
// The emitter renders through the standard printer, so the byte-for-byte
// content of the re-emitted block reflects the printer's formatting choices
// rather than the original source spacing.
package comment
