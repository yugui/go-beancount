# Go Test Comments — Key Rules

Source: https://go.dev/wiki/TestComments

Supplement to Go Code Review Comments, targeted at test code.

## Assert Libraries

**Do not** use assert libraries (`assert.Equal`, `assert.NotNil`, etc.). They either stop tests early or hide information. Write tests in plain Go:

```go
// Good
if obj == nil || obj.Type != "blogPost" || obj.Comments != 2 || obj.Body == "" {
    t.Errorf("AddPost() = %+v", obj)
}

// Bad — assert style
assert.IsNotNil(t, "obj", obj)
assert.StringEq(t, "obj.Type", obj.Type, "blogPost")
```

## Subtest Names

When using `t.Run`, choose names that remain readable after escaping (spaces → underscores, non-printing chars escaped). Use `t.Log` or include inputs in failure messages to avoid relying on the escaped name.

## Compare Full Structures

Don't compare structs field-by-field. Build the expected struct and compare in one shot using `cmp.Diff` or `cmp.Equal`. Same applies to arrays and maps. For multiple return values, compare them individually (don't wrap in an anonymous struct just for comparison).

Use `cmpopts` (`IgnoreInterfaces`, `IgnoreUnexported`, etc.) for semantic or approximate equality.

## Compare Stable Results

Do not compare output that depends on packages you don't own. `json.Marshal` makes no guarantee about exact bytes. Parse and compare data structures rather than exact formatted strings.

## Equality Comparison and Diffs

- Prefer `cmp.Equal` / `cmp.Diff` from `github.com/google/go-cmp/cmp` over `reflect.DeepEqual`
- `cmp` is stable across Go versions and user-configurable
- For protobuf messages: use `cmp.Comparer(proto.Equal)` or `protocmp.Transform()`
- `reflect.DeepEqual` is sensitive to unexported fields and implementation details

## Got Before Want

Actual value before expected value:

```go
t.Errorf("YourFunc(%v) = %v, want %v", in, got, want)
```

For diffs, label direction: `"diff (-want +got):\n%s"` (pass `(want, got)` to `cmp.Diff` so `-`/`+` align with actual diff prefixes).

## Identify the Function

Include the function name in failure messages, even if it's in the test name:

```go
// Good
t.Errorf("YourFunc(%v) = %v, want %v", in, got, want)

// Bad — ambiguous when multiple assertions fail
t.Errorf("got %v, want %v", got, want)
```

## Identify the Input

Include short inputs in failure messages. For large or opaque inputs, name test cases descriptively and print the name. **Do not** rely on test table indices — developers should not have to count entries to find a failure.

## Keep Going

Prefer `t.Error` over `t.Fatal` so all failed checks show in a single run. Use `t.Fatal` only when:
- Test setup fails before the test can run
- Before the test loop in table-driven tests
- Subsequent checks would be meaningless

Inside a table loop without `t.Run`: use `t.Error` + `continue`. With `t.Run`: `t.Fatal` ends the subtest and lets the next run.

## Mark Test Helpers

Call `t.Helper()` in helper functions so failures are attributed to the caller:

```go
func readFile(t *testing.T, filename string) string {
    t.Helper()
    contents, err := os.ReadFile(filename)
    if err != nil {
        t.Fatal(err)
    }
    return string(contents)
}
```

Do not use `t.Helper` when it obscures the connection between the failure and its cause (e.g. assert-style helpers).

## Print Diffs

For large output, print a diff rather than both values:

```go
if diff := cmp.Diff(want, got); diff != "" {
    t.Errorf("YourFunc(%v) mismatch (-want +got):\n%s", in, diff)
}
```

- Precede the diff with a newline (it spans multiple lines)
- Label the direction clearly

## Table-Driven vs. Multiple Functions

- **Table-driven**: when many cases share the same logic
- **Multiple functions**: when cases need different logic
- **Combine**: separate table-driven tests for success cases vs. error cases

Avoid complex conditional logic per-row inside a table loop.

## Test Error Semantics

- String matching on error messages is fragile → creates change-detector tests
- OK to check that error messages contain specific properties (e.g. parameter names)
- For exact error classification: expose typed errors or sentinel values, separate from human-readable strings
- `fmt.Errorf` destroys semantic error info — don't use it when the caller needs to distinguish error types
- If your API doesn't promise specific error types, just check for non-nil errors
