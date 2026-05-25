package main

import (
	"sync"
	"time"

	"go.lsp.dev/uri"
)

// debouncer schedules one deferred call per URI. A new schedule for the same
// URI cancels any pending call that has not yet fired.
type debouncer struct {
	mu     sync.Mutex
	timers map[uri.URI]*time.Timer
}

// newDebouncer returns a debouncer with an initialized timer map.
func newDebouncer() *debouncer {
	return &debouncer{timers: make(map[uri.URI]*time.Timer)}
}

// schedule arranges for fn to be called after d. If a timer is already pending
// for u, it is stopped and replaced.
func (db *debouncer) schedule(u uri.URI, d time.Duration, fn func()) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if t, ok := db.timers[u]; ok {
		t.Stop()
	}
	db.timers[u] = time.AfterFunc(d, func() {
		db.mu.Lock()
		delete(db.timers, u)
		db.mu.Unlock()
		fn()
	})
}

// stopAll cancels all pending timers. Calls in-flight are not interrupted.
func (db *debouncer) stopAll() {
	db.mu.Lock()
	defer db.mu.Unlock()
	for u, t := range db.timers {
		t.Stop()
		delete(db.timers, u)
	}
}
