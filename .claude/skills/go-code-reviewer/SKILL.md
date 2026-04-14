---
name: go-code-reviewer
description: Reviews modified Go files against Effective Go, Code Review Comments, Test Comments, and Google Go Style guidelines
argument-hint: [ref]
context: fork
model: sonnet
allowed-tools: Bash(git diff*) Bash(git status*) Bash(git show*) Bash(git log*) Read Glob Grep
---

# Go Code Reviewer

Review all Go files modified in the current working tree (or against an optional git ref) for adherence to Go best practices.

## Process

1. **Identify changed Go files**
   - If `$ARGUMENTS` is provided (e.g. `HEAD~1`, `main`), run:
     `git diff --name-only $ARGUMENTS -- '*.go'`
   - Otherwise, get both staged and unstaged changes:
     `git diff --name-only HEAD -- '*.go'` and `git diff --name-only -- '*.go'`
     (combine, deduplicate)
   - If no changed `.go` files are found, report "No Go files modified."

2. **For each changed `.go` file**
   - Read the diff: `git diff $ARGUMENTS -- <file>` (or `git diff HEAD -- <file>` if no ref)
   - Read the full file content using the Read tool
   - Apply review criteria from the resource files below

3. **Output a structured report**
   ```
   ## <file path>

   ### Issues
   - **[Category]** Description of issue.
     *Guideline:* Relevant rule from Effective Go / Code Review Comments / Test Comments / Google Style.
     *Location:* line N or function `Foo`
     *Suggestion:* Concrete fix.

   ### Looks Good
   - (Note anything notably well-done, if applicable)
   ```
   If no issues found in a file, write "No issues found."

4. **Summary** — End with a brief summary: total files reviewed, total issues found, and the most critical ones.

## Review Criteria

Focus on issues **in the changed lines** (diff), but note systemic issues across the whole file when relevant. Do **not** comment on formatting — `gofmt` is enforced automatically by project hooks.

Key areas to check (see resource files for full details):

- **Error handling**: silent discards (`_ = err`), capitalized or punctuated error strings, missing `%w` for wrapping
- **Naming**: initialisms (`URL` not `Url`), receiver names (short abbreviation, not `self`/`this`), package name repetition, meaningless package names
- **Comments/docs**: missing doc comments on exported identifiers, comments not starting with the declared name, non-sentence comments on exports
- **Context**: not first parameter, stored in struct fields
- **Interfaces**: defined on producer side, too large, unnecessary pre-use definition
- **Goroutines**: unclear exit conditions, potential leaks
- **Receiver type**: inconsistent pointer/value mix within a type
- **Testing**: unhelpful failure messages, wrong got/want order, missing function name in message, assert-library usage, field-by-field struct comparison, missing `t.Helper()` in helpers, fragile error-string matching
- **Imports**: dot imports, blank imports in non-main packages, missing grouping
- **Panic**: panic used for normal error handling

## Resources

Load these files for the detailed rules when reviewing:

- [Effective Go](resources/effective-go.md)
- [Go Code Review Comments](resources/code-review-comments.md)
- [Go Test Comments](resources/test-comments.md) — load when reviewing `*_test.go` files
- [Google Go Style Decisions](resources/google-go-style-decisions.md)
