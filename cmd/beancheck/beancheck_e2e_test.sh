#!/usr/bin/env bash
# End-to-end test for the beancheck binary.
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <beancheck-binary>" >&2
  exit 2
fi

bin="$(pwd)/$1"
tmp="$(mktemp -d "${TEST_TMPDIR:-/tmp}/beancheck.XXXXXX")"

# 1. A valid ledger exits 0 with no output on stderr/stdout.
cat >"$tmp/ok.beancount" <<'EOF'
2024-01-01 open Assets:Cash USD
2024-01-02 open Expenses:Food USD

2024-01-02 * "Lunch"
  Expenses:Food   10.00 USD
  Assets:Cash    -10.00 USD
EOF

if ! "$bin" "$tmp/ok.beancount" >"$tmp/ok.out" 2>"$tmp/ok.err"; then
  echo "FAIL: valid ledger should exit 0" >&2
  echo "stdout:" >&2; cat "$tmp/ok.out" >&2
  echo "stderr:" >&2; cat "$tmp/ok.err" >&2
  exit 1
fi
if [[ -s "$tmp/ok.out" || -s "$tmp/ok.err" ]]; then
  echo "FAIL: valid ledger should produce no diagnostic output" >&2
  echo "stdout:" >&2; cat "$tmp/ok.out" >&2
  echo "stderr:" >&2; cat "$tmp/ok.err" >&2
  exit 1
fi

# 2. An unbalanced transaction is reported and exit status is 1.
cat >"$tmp/bad.beancount" <<'EOF'
2024-01-01 open Assets:Cash USD
2024-01-02 open Expenses:Food USD

2024-01-02 * "Bad"
  Expenses:Food   10.00 USD
  Assets:Cash     -9.00 USD
EOF

set +e
"$bin" "$tmp/bad.beancount" >"$tmp/bad.out" 2>"$tmp/bad.err"
rc=$?
set -e
if [[ $rc -ne 1 ]]; then
  echo "FAIL: invalid ledger should exit 1 (got $rc)" >&2
  cat "$tmp/bad.err" >&2
  exit 1
fi
if ! grep -q 'bad.beancount' "$tmp/bad.err"; then
  echo "FAIL: expected a diagnostic mentioning the file on stderr" >&2
  cat "$tmp/bad.err" >&2
  exit 1
fi

# 3. A missing input file is a beancheck-side failure → exit 2.
set +e
"$bin" "$tmp/does-not-exist.beancount" >/dev/null 2>"$tmp/missing.err"
rc=$?
set -e
if [[ $rc -ne 2 ]]; then
  echo "FAIL: missing file should exit 2 (got $rc)" >&2
  cat "$tmp/missing.err" >&2
  exit 1
fi
if ! grep -q 'beancheck:' "$tmp/missing.err"; then
  echo "FAIL: expected 'beancheck:' prefix on missing-file error" >&2
  cat "$tmp/missing.err" >&2
  exit 1
fi
if ! grep -q 'does-not-exist.beancount' "$tmp/missing.err"; then
  echo "FAIL: expected a diagnostic mentioning the missing file" >&2
  cat "$tmp/missing.err" >&2
  exit 1
fi

# 4. No arguments prints usage and exits 2.
set +e
"$bin" >"$tmp/noargs.out" 2>"$tmp/noargs.err"
rc=$?
set -e
if [[ $rc -ne 2 ]]; then
  echo "FAIL: no-argument invocation should exit 2 (got $rc)" >&2
  exit 1
fi
if ! grep -q 'Usage: beancheck' "$tmp/noargs.err"; then
  echo "FAIL: no-argument invocation should print usage" >&2
  cat "$tmp/noargs.err" >&2
  exit 1
fi

# 5. -h prints usage and exits 0.
"$bin" -h >"$tmp/help" 2>&1
if ! grep -q 'Usage: beancheck' "$tmp/help"; then
  echo "FAIL: -h output missing 'Usage: beancheck'" >&2
  cat "$tmp/help" >&2
  exit 1
fi
if ! grep -q -- '-Werror' "$tmp/help"; then
  echo "FAIL: -h output missing '-Werror' flag documentation" >&2
  cat "$tmp/help" >&2
  exit 1
fi

# 6. -Werror is accepted and does not flip exit status on a clean ledger.
if ! "$bin" -Werror "$tmp/ok.beancount" >"$tmp/werror.out" 2>"$tmp/werror.err"; then
  echo "FAIL: -Werror on a clean ledger should exit 0" >&2
  cat "$tmp/werror.err" >&2
  exit 1
fi

echo "OK"
