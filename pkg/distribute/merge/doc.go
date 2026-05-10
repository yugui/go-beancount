// Package merge writes new directives into a destination beancount
// file while preserving every byte of surrounding content.
//
// [Merge] adds the directives in a [Plan] to plan.Path, creating the
// file (and parent directories) if it does not yet exist. Each write
// is atomic. For an existing file every byte outside the merger's
// own insertions — independent comment blocks, blank lines, undated
// header directives, and any unusual formatting — is preserved
// byte-for-byte. The single deliberate exception is when the existing
// file lacks a trailing newline and the merger appends past EOF: a
// terminating "\n" is folded into the patch text so the resulting
// file ends with exactly one newline.
//
// # Types
//
// A [Plan] groups all inserts targeted at one path together with the
// file-level layout settings (sort order, target blank-line count
// between directives, whether to actively insert blank lines). Each
// [Insert] carries one [ast.Directive] plus body-level formatting
// overrides. The split matches each option's scope: file-wide spacing
// lives on the Plan; body-level rendering lives on the Insert so a
// future commented insert can render with a different style.
//
// # Spacing rule (B/N)
//
// Let N be Plan.BlankLinesBetweenDirectives, B be
// Plan.InsertBlankLinesBetweenDirectives, and X be the count of
// blank lines already present on the existing side adjacent to the
// insertion offset. The merger contributes max(0, N-X) extra newlines
// on that side when B is true, and zero when B is false. The merger
// never reduces pre-existing blank lines (X > N is left as-is) —
// whole-file normalization is the job of a separate beanfmt pass.
//
// # Sort orders
//
// [Plan].Order controls how new directives are positioned relative to
// existing dated directives:
//
//   - [route.OrderAscending]: older dates precede newer dates
//     (oldest-first). Default.
//   - [route.OrderDescending]: newer dates precede older dates
//     (newest-first).
//   - [route.OrderAppend]: each insert lands unconditionally at end
//     of file, after any trailing trivia.
//
// Within a single target offset, multiple inserts from the same Plan
// are emitted in their original input order (stable FIFO). The merger
// does not analyze or normalize the existing file's ordering: when
// the file disagrees with the requested order, the only invariant
// is that no existing directive, comment, or blank line changes its
// relative position. Correcting layout is the job of a separate
// beanfmt pass.
package merge
