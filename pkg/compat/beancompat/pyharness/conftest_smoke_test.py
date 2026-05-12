"""Smoke test for the overlay conftest.

Verifies that importing the overlay conftest:
  - registers GoBeancountAdapter under "gobeancount" in tests.conftest.ADAPTERS,
  - removes all other adapters,
  - and that ALLOWED_FIXTURES has the expected shape.

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

    def test_allowlist_shape(self):
        from allowlist import ALLOWED_FIXTURES

        self.assertIsInstance(ALLOWED_FIXTURES, frozenset)
        self.assertTrue(ALLOWED_FIXTURES, "ALLOWED_FIXTURES must not be empty")
        pattern = re.compile(r"^(parse|check)/.+\.json$")
        for entry in ALLOWED_FIXTURES:
            self.assertIsInstance(entry, str)
            self.assertRegex(entry, pattern, f"entry {entry!r} does not match expected format")


if __name__ == "__main__":
    unittest.main()
