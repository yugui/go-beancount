//go:build beancompat_fixtures

package beancompat

// enabledParseFixtures gates which parse-tier fixtures actually execute.
// The map value is a free-form note (typically a date or commit reference)
// recording when the fixture was deliberately enabled. A fixture absent
// from this map is reported as SKIP, not failure, so a build remains green
// even when go-beancount's serializer cannot yet produce a matching
// Result for it.
var enabledParseFixtures = map[string]string{
	"open_single":          "verified 2026-05-11",
	"price":                "verified 2026-05-11",
	"transaction_balanced": "verified 2026-05-11",
}

// enabledCheckFixtures gates which check-tier fixtures actually execute,
// using the same convention as enabledParseFixtures.
var enabledCheckFixtures = map[string]string{
	"transaction_with_cost": "verified 2026-05-12",
}
