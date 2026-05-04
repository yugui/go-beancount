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
// Sub-phase scope (7.5b)
//
// This package implements the MVP from §9 row 7.5b of the beanfile
// design: ascending order only, active inserts only. Out-of-scope inputs
// (Plan.Order != OrderAscending, Insert.Commented == true) are rejected
// with sentinel errors so callers can [errors.Is] them and route to the
// right follow-up sub-phase. Other Insert fields reserved for those
// sub-phases (Prefix, StripMetaKeys) are accepted but not read.
package merge
