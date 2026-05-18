//go:build beancompat_fixtures

// The build tag above (mirrored by gotags in BUILD.bazel) is what keeps a
// plain `go test ./...` from trying to load this package: go test cannot
// fetch the @beancompat archive on its own, so the fixture-dependent files
// must be excluded from the default Go build. Bazel sets the tag explicitly
// on the fixture test targets, so `bazel test //...` still exercises them.
// Do not drop the tag thinking it gates participation in `bazel test //...`
// — it does not.

package beancompat

// parseDivergences records parse-tier fixtures whose actual output is known
// to diverge from the upstream fixture's expected envelope. A fixture listed
// here is reported as SKIP with the recorded reason; absence means the
// fixture is expected to pass and any Match failure is a real regression.
//
// This is the local-only layer of a two-tier divergence registry. The other
// tier is the upstream fixture file's known_divergences["go-beancount"]
// entry; runFixtures honors that first, so once a divergence has been
// accepted upstream the local entry should be removed. Use this map for
// divergences not yet reflected upstream — annotate the reason with
// "upstream-PR pending" or "go-beancount fix pending" so stale entries are
// easy to triage.
var parseDivergences = map[string]string{}

// checkDivergences mirrors parseDivergences for check-tier fixtures, using
// the same two-tier policy and annotation convention.
var checkDivergences = map[string]string{}
