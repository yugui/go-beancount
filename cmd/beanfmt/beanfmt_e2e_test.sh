#!/usr/bin/env bash
# End-to-end test for the beanfmt binary.
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <beanfmt-binary>" >&2
  exit 2
fi

bin="$(pwd)/$1"
tmp="$(mktemp -d "${TEST_TMPDIR:-/tmp}/beanfmt.XXXXXX")"

unformatted=$'2024-01-01 open Assets:Cash\n2024-01-02 open Expenses:Food\n'
formatted=$'2024-01-01 open Assets:Cash\n\n2024-01-02 open Expenses:Food\n'

# 1. stdin -> stdout
printf '%s' "$unformatted" | "$bin" >"$tmp/out_stdin"
if ! diff -u <(printf '%s' "$formatted") "$tmp/out_stdin"; then
  echo "FAIL: stdin -> stdout output mismatch" >&2
  exit 1
fi

# 2. file arg -> stdout
printf '%s' "$unformatted" >"$tmp/in.beancount"
"$bin" "$tmp/in.beancount" >"$tmp/out_file"
if ! diff -u <(printf '%s' "$formatted") "$tmp/out_file"; then
  echo "FAIL: file -> stdout output mismatch" >&2
  exit 1
fi

# 3. -w rewrites the file in place
cp "$tmp/in.beancount" "$tmp/w.beancount"
"$bin" -w "$tmp/w.beancount" >"$tmp/out_w"
if [[ -s "$tmp/out_w" ]]; then
  echo "FAIL: -w produced stdout output" >&2
  cat "$tmp/out_w" >&2
  exit 1
fi
if ! diff -u <(printf '%s' "$formatted") "$tmp/w.beancount"; then
  echo "FAIL: -w file contents mismatch" >&2
  exit 1
fi

# 4. -h prints usage and exits 0
"$bin" -h >"$tmp/help" 2>&1
if ! grep -q 'Usage: beanfmt' "$tmp/help"; then
  echo "FAIL: -h output missing 'Usage: beanfmt'" >&2
  cat "$tmp/help" >&2
  exit 1
fi
if ! grep -q -- '-column' "$tmp/help"; then
  echo "FAIL: -h output missing '-column' flag documentation" >&2
  cat "$tmp/help" >&2
  exit 1
fi

# 5. Unknown flag fails with non-zero exit
if "$bin" -bogus </dev/null >/dev/null 2>&1; then
  echo "FAIL: -bogus should have exited non-zero" >&2
  exit 1
fi

echo "OK"
