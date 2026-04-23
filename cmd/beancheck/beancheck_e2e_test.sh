#!/usr/bin/env bash
# End-to-end test for the beancheck binary.
set -uo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <beancheck-binary>" >&2
  exit 2
fi

bin="$(pwd)/$1"
tmp="$(mktemp -d "${TEST_TMPDIR:-/tmp}/beancheck.XXXXXX")"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# Fixture 1: clean ledger — every posting balances, every account opened.
good="$tmp/good.beancount"
cat >"$good" <<'EOF'
option "title" "Good"
option "operating_currency" "USD"

2024-01-01 open Assets:Cash    USD
2024-01-01 open Income:Salary  USD

2024-01-10 * "Paycheck"
  Assets:Cash      1000.00 USD
  Income:Salary   -1000.00 USD
EOF

# Fixture 2: bad ledger — an unbalanced transaction.
bad="$tmp/bad.beancount"
cat >"$bad" <<'EOF'
option "title" "Bad"
option "operating_currency" "USD"

2024-01-01 open Assets:Cash    USD
2024-01-01 open Income:Salary  USD

2024-01-10 * "Oops"
  Assets:Cash      1000.00 USD
  Income:Salary    -999.00 USD
EOF

# 1. Clean ledger → exit 0, no stderr.
if ! "$bin" "$good" >"$tmp/good.out" 2>"$tmp/good.err"; then
  fail "clean ledger should exit 0; stderr:"$'\n'"$(cat "$tmp/good.err")"
fi
if [[ -s "$tmp/good.err" ]]; then
  fail "clean ledger wrote to stderr:"$'\n'"$(cat "$tmp/good.err")"
fi
if [[ -s "$tmp/good.out" ]]; then
  fail "clean ledger wrote to stdout:"$'\n'"$(cat "$tmp/good.out")"
fi

# 2. Bad ledger → exit 1, stderr contains "error:" and the source path.
set +e
"$bin" "$bad" >"$tmp/bad.out" 2>"$tmp/bad.err"
bad_rc=$?
set -e
if [[ "$bad_rc" -ne 1 ]]; then
  fail "bad ledger exit code = $bad_rc, want 1; stderr:"$'\n'"$(cat "$tmp/bad.err")"
fi
if ! grep -q 'error:' "$tmp/bad.err"; then
  fail "bad ledger stderr missing 'error:':"$'\n'"$(cat "$tmp/bad.err")"
fi
if ! grep -qF "$bad" "$tmp/bad.err"; then
  fail "bad ledger stderr missing source path $bad:"$'\n'"$(cat "$tmp/bad.err")"
fi

# 3. Nonexistent file → exit 1 with an error: line. ast.Load records the read
# failure as a diagnostic and loader.Load still returns a nil error.
set +e
"$bin" "$tmp/does_not_exist.beancount" >"$tmp/nx.out" 2>"$tmp/nx.err"
nx_rc=$?
set -e
if [[ "$nx_rc" -ne 1 ]]; then
  fail "nonexistent file exit code = $nx_rc, want 1; stderr:"$'\n'"$(cat "$tmp/nx.err")"
fi
if ! grep -q 'error:' "$tmp/nx.err"; then
  fail "nonexistent file stderr missing 'error:':"$'\n'"$(cat "$tmp/nx.err")"
fi

# 4. Missing argument → exit 2.
set +e
"$bin" </dev/null >"$tmp/noarg.out" 2>"$tmp/noarg.err"
noarg_rc=$?
set -e
if [[ "$noarg_rc" -ne 2 ]]; then
  fail "missing argument exit code = $noarg_rc, want 2; stderr:"$'\n'"$(cat "$tmp/noarg.err")"
fi

# 5. -h prints usage that mentions beancheck and -strict, exits 0.
if ! "$bin" -h >"$tmp/help" 2>&1; then
  fail "-h should exit 0"
fi
if ! grep -q 'beancheck' "$tmp/help"; then
  fail "-h output missing 'beancheck':"$'\n'"$(cat "$tmp/help")"
fi
if ! grep -q -- '-strict' "$tmp/help"; then
  fail "-h output missing '-strict':"$'\n'"$(cat "$tmp/help")"
fi

# 6. Unknown flag → non-zero exit.
set +e
"$bin" -bogus </dev/null >/dev/null 2>"$tmp/bogus.err"
bogus_rc=$?
set -e
if [[ "$bogus_rc" -eq 0 ]]; then
  fail "-bogus should exit non-zero"
fi

echo "OK"
