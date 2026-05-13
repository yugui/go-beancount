"""Smoke test for GoBeancountAdapter.

Verifies (1) the subprocess wire end-to-end against the real beanparse binary,
(2) the "NEVER raises" contract for subprocess-level failures: when the binary
cannot be located, parse_string must return a ParseResult with a non-empty
errors list rather than propagating an exception, and (3) that the
_CAP_PARSE / _CAP_BOOKING literals match the values imported from upstream
beancompat so capability advertisement cannot drift silently.

Uses unittest.main() so `py_test` invoking the file as a plain Python script
(`python adapter_test.py`) actually discovers and executes the test methods.
"""

from __future__ import annotations

import os
import tempfile
import unittest
import unittest.mock
from pathlib import Path

from adapter import GoBeancountAdapter
import adapter as _adapter_module


class GoBeancountAdapterSmokeTest(unittest.TestCase):
    def test_parse_open_directive(self):
        a = GoBeancountAdapter()
        result = a.parse_string("2024-01-01 open Assets:Cash USD\n")
        self.assertEqual(result.errors, [], f"unexpected errors: {result.errors}")
        self.assertGreaterEqual(
            len(result.directives), 1, "expected at least one directive"
        )
        self.assertEqual(
            result.directives[0].type,
            "open",
            f"expected first directive type 'open', got {result.directives[0].type!r}",
        )

    def test_cap_parse_literal_matches_upstream(self):
        """_CAP_PARSE must equal the value upstream beancompat exports as CAP_PARSE.

        The capabilities property is intentionally free of beancompat imports so
        the module remains introspectable outside a full Bazel sandbox. This test
        locks the duplicated string literal against upstream drift.
        """
        from implementations.adapter import CAP_PARSE

        self.assertIn(
            CAP_PARSE,
            GoBeancountAdapter().capabilities,
            "GoBeancountAdapter.capabilities does not contain upstream CAP_PARSE; "
            "update _CAP_PARSE in adapter/__init__.py",
        )

    def test_cap_booking_literal_matches_upstream(self):
        """_CAP_BOOKING must equal the value upstream beancompat exports as CAP_BOOKING.

        beanparse always runs the full loader pipeline (parse + booking +
        validations), so the adapter advertises CAP_BOOKING alongside CAP_PARSE.
        This locks the duplicated literal against upstream drift, same shape as
        test_cap_parse_literal_matches_upstream.
        """
        from implementations.adapter import CAP_BOOKING

        self.assertIn(
            CAP_BOOKING,
            GoBeancountAdapter().capabilities,
            "GoBeancountAdapter.capabilities does not contain upstream CAP_BOOKING; "
            "update _CAP_BOOKING in adapter/__init__.py",
        )

    def test_missing_binary_returns_diagnostic(self):
        """Contract: subprocess-level failures NEVER raise."""
        original = os.environ.get("BEANPARSE_BIN")
        os.environ["BEANPARSE_BIN"] = "/nonexistent/beanparse-does-not-exist"
        try:
            a = GoBeancountAdapter()
            # Must return a ParseResult, not raise.
            result = a.parse_string("2024-01-01 open Assets:Cash USD\n")
            self.assertEqual(result.directives, [])
            self.assertTrue(
                result.errors, "expected a diagnostic error for missing binary"
            )
        finally:
            if original is None:
                os.environ.pop("BEANPARSE_BIN", None)
            else:
                os.environ["BEANPARSE_BIN"] = original

    def test_resolve_binary_none_returns_diagnostic_in_check_file(self):
        """check_file must return a diagnostic ParseResult when _resolve_binary()
        is None, not raise.  This covers the branch exercised when the Bazel
        sandbox has no beanparse binary and BEANPARSE_BIN is unset.
        """
        with unittest.mock.patch.object(_adapter_module, "_resolve_binary", return_value=None):
            a = GoBeancountAdapter()
            # check_file calls _load_beancompat_types first; supply a real
            # (empty) file so any path logic doesn't interfere.
            with tempfile.NamedTemporaryFile(suffix=".beancount", delete=False) as f:
                tmp = f.name
            try:
                result = a.check_file(Path(tmp))
            finally:
                os.unlink(tmp)
        self.assertEqual(result.directives, [])
        self.assertTrue(
            result.errors,
            "expected a diagnostic error when _resolve_binary() returns None",
        )


if __name__ == "__main__":
    unittest.main()
