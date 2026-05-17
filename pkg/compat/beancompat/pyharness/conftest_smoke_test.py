"""Smoke test for the overlay conftest.

Verifies that importing the overlay conftest:
  - registers GoBeancountAdapter under "gobeancount" in tests.conftest.ADAPTERS,
  - removes all other adapters,
  - and that DENIED_FIXTURES has the expected shape and initial entries.

Uses unittest so the test runs under Bazel's py_test without depending on
the pytest harness it validates.
"""

from __future__ import annotations

import importlib.util
import pathlib
import re
import unittest


def _load_overlay_conftest():
    spec = importlib.util.spec_from_file_location(
        "overlay_conftest",
        pathlib.Path(__file__).parent / "conftest.py",
    )
    overlay_conftest = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(overlay_conftest)
    return overlay_conftest


class ConfTestSmokeTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.overlay = _load_overlay_conftest()

    def test_adapter_registered(self):
        import tests.conftest

        self.assertIn("gobeancount", tests.conftest.ADAPTERS)
        self.assertEqual(
            tests.conftest.ADAPTERS["gobeancount"].__name__,
            "GoBeancountAdapter",
        )

    def test_other_adapters_removed(self):
        import tests.conftest

        self.assertEqual(set(tests.conftest.ADAPTERS), {"gobeancount"})

    def test_denylist_shape(self):
        from denylist import DENIED_FIXTURES

        self.assertIsInstance(DENIED_FIXTURES, dict)
        pattern = re.compile(r"^(parse|check)/.+\.json$")
        for entry, reason in DENIED_FIXTURES.items():
            self.assertIsInstance(entry, str)
            self.assertRegex(
                entry, pattern, f"entry {entry!r} does not match expected format"
            )
            self.assertIsInstance(reason, str)
            self.assertTrue(
                reason.strip(),
                f"entry {entry!r} has empty reason; every denylist entry must "
                f"document why it is denylisted",
            )

    def test_denylist_initial_entries(self):
        """Sanity check on the migration: the remaining parse-tier
        options-envelope divergence that the Go-side denylist also lists must
        be present.

        Catches a typo in the rename from accidentally passing the suite.
        Once the underlying serializer gap is fixed and the Go-side entries
        are removed, update this test to match.
        """
        from denylist import DENIED_FIXTURES

        self.assertNotIn("parse/display_precision_by_currency.json", DENIED_FIXTURES)
        self.assertIn("parse/options_coverage.json", DENIED_FIXTURES)


if __name__ == "__main__":
    unittest.main()
