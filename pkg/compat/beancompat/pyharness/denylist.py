"""Local divergence registry for the go-beancount pyharness.

DENIED_FIXTURES records upstream beancompat fixtures whose actual output
from the gobeancount adapter is known to diverge from the fixture's
expected envelope. Each entry is applied as
``pytest.mark.xfail(strict=False, reason=...)`` by the overlay conftest,
so the fixture runs, the failure is reported as XFAIL, and an
inadvertent fix surfaces as XPASS in the suite output.

This is the local-only layer of a two-tier divergence registry. The
other tier is the upstream fixture file's
``known_divergences["gobeancount"]`` entry; upstream
``tests/test_fixtures.py`` honors that inline via ``pytest.xfail(...)``
before the test body runs. Once a divergence has been accepted upstream
the local entry should be removed; entries listed here are divergences
not yet reflected upstream — annotate the reason with "upstream-PR
pending" or "go-beancount fix pending" so stale entries are easy to
triage. Mirrors ``pkg/compat/beancompat/denylist.go`` on the Go side.
"""

DENIED_FIXTURES: dict[str, str] = {
    "parse/display_precision_by_currency.json": (
        "go-beancount fix pending: parse-tier serializer does not yet "
        "emit the options envelope (display_precision_by_currency expected)."
    ),
    "parse/options_coverage.json": (
        "go-beancount fix pending: parse-tier serializer does not yet "
        "emit the options envelope (~30 BeancountOptions keys expected)."
    ),
}
