// Package merge writes new directives into a destination beancount file
// while preserving every byte of surrounding content.
//
// # Patch-composition model
//
// For an existing file, [Merge] never edits the CST in place. It parses
// the file once, identifies the byte offset at which each new directive
// should be inserted (via binary search on the existing dated directives;
// see the design doc §8), then composes the output by interleaving the
// original bytes with rendered insert text. As a result every byte not
// covered by a new insertion - independent comment blocks, blank lines,
// undated header directives, and any unusual formatting - is preserved
// byte-for-byte. The single deliberate exception is when the existing
// file lacks a trailing newline and the merger appends content past EOF:
// a terminating "\n" is then folded into the patch text so the resulting
// file ends with exactly one newline (POSIX text-file convention).
//
// # Types
//
// A [Plan] groups all inserts targeted at one path together with the
// file-level layout settings (sort order, target blank-line count between
// directives, whether to actively insert blank lines). Each [Insert]
// carries one [ast.Directive] plus body-level formatting overrides. The
// distinction matches the scope of each option: the two spacing options
// describe the file's layout and live on the [Plan]; the body-printing
// options stay on the [Insert] so a future commented insert may render
// with a different style.
//
// Spacing rule (B/N)
//
// Let N be Plan.BlankLinesBetweenDirectives, B be
// Plan.InsertBlankLinesBetweenDirectives, and X be the count of blank
// lines already present on the existing side adjacent to the insertion
// offset. The merger contributes max(0, N-X) extra newlines on that side
// when B is true, and zero when B is false. The merger never reduces
// pre-existing blank lines (X > N is left as-is) - whole-file
// normalization is the job of a separate beanfmt pass.
//
// # Sub-phase scope
//
// This package implements the active and commented emit paths from §9
// rows 7.5b and 7.5e of the beanfile design. Plan.Order other than
// OrderAscending is rejected with [ErrOrderNotSupported] so callers can
// [errors.Is] it and route to 7.5h. The Insert.StripMetaKeys field is
// accepted but not read; it lands in 7.5g.
package merge
