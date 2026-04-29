package quote

import (
	"time"

	"github.com/yugui/go-beancount/pkg/quote/api"
)

// EventKind classifies the four observable points in a Fetch run that
// the Observer hook receives. The orchestrator emits exactly one event
// per occurrence — there is no aggregation or filtering above the
// hook.
type EventKind uint8

const (
	// EventLevelStart is emitted at the start of each scheduling
	// level, before any source on that level is dispatched. Source,
	// Pair, Symbol, Mode, Duration, Err, and NumPrice are zero on
	// LevelStart events; only Kind and Level are populated.
	EventLevelStart EventKind = iota
	// EventCallStart is emitted just before a Source method is
	// invoked. Level identifies the level the call belongs to;
	// Source, Pair, Symbol, and Mode describe the call. Duration,
	// Err, and NumPrice are zero (the call has not yet returned).
	EventCallStart
	// EventCallDone is emitted after a Source method returns. All
	// fields are populated: Duration is the wall-clock between the
	// matching CallStart and the call's return; Err is the call's
	// error (nil if the call succeeded); NumPrice is the number of
	// ast.Price entries the call produced.
	EventCallDone
	// EventLevelEnd is emitted after every call on a level has
	// completed and per-unit results have been merged in. Field
	// population matches LevelStart.
	EventLevelEnd
)

// Event carries the observation payload Fetch hands to a registered
// Observer. The hook runs synchronously on the goroutine that
// originated the corresponding scheduling step; observers must not
// block (do non-trivial work in another goroutine if needed).
type Event struct {
	// Kind is the event class.
	Kind EventKind
	// Level is the scheduler level number, starting at 0 for the
	// primary source and incrementing once per fallback step.
	Level int
	// Source is the source name the call targets. Empty for
	// LevelStart and LevelEnd events.
	Source string
	// Pair is the logical pair the call concerns. Zero for
	// non-call events.
	Pair api.Pair
	// Symbol is the source-specific ticker for the call. Empty for
	// non-call events.
	Symbol string
	// Mode is the source-method mode the orchestrator decided on
	// (after capability ↔ mode demotion). Zero for non-call events.
	Mode api.Mode
	// Duration is the wall-clock duration of a completed call.
	// Populated only on CallDone.
	Duration time.Duration
	// Err is the error returned by a completed call (nil on
	// success). Populated only on CallDone.
	Err error
	// NumPrice is the number of ast.Price entries returned by a
	// completed call. Populated only on CallDone.
	NumPrice int
}
