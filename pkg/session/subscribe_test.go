// White-box tests for Session.Subscribe. Tests are in package session to
// reuse the unexported loadFunc hook established in session_test.go. Reaching
// into unexported state is justified by the same CLAUDE.md exception: timing
// and coalescing assertions require the hook.
package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/loader"
)

// recvWithin receives from ch within d. Returns (value, true) on success,
// (nil, false) on timeout or closed channel.
func recvWithin(t *testing.T, ch <-chan *ast.Ledger, d time.Duration) (*ast.Ledger, bool) {
	t.Helper()
	select {
	case l, ok := <-ch:
		if !ok {
			return nil, false
		}
		return l, true
	case <-time.After(d):
		return nil, false
	}
}

// chanEmptyAndClosed returns true if ch is closed and holds no unread value.
// Precondition: the channel must not hold an unread value (a buffered value
// confuses the drain-and-check semantics).
func chanEmptyAndClosed(t *testing.T, ch <-chan *ast.Ledger) bool {
	t.Helper()
	select {
	case _, ok := <-ch:
		return !ok
	default:
		return false
	}
}

func newTestSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	root := dir + "/main.beancount"
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")
	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSubscribe_DeliversOnReload(t *testing.T) {
	s := newTestSession(t)
	ch, cancel := s.Subscribe()
	defer cancel()

	if _, err := s.Reload(context.Background()); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	ledger, ok := recvWithin(t, ch, time.Second)
	if !ok {
		t.Fatal("Subscribe: no ledger received after Reload")
	}
	if ledger == nil {
		t.Fatal("Subscribe: received nil ledger")
	}
}

func TestSubscribe_NoInitialDelivery(t *testing.T) {
	s := newTestSession(t)
	ch, cancel := s.Subscribe()
	defer cancel()

	// No Reload yet; channel must be empty.
	select {
	case v := <-ch:
		t.Errorf("Subscribe: unexpected initial delivery: %v", v)
	default:
	}
}

func TestSubscribe_LatestWins(t *testing.T) {
	s := newTestSession(t)
	ch, cancel := s.Subscribe()
	defer cancel()

	// Run three Reloads without reading from ch; latest-wins means only the
	// last ledger pointer is in the channel.
	var ledgers [3]*ast.Ledger
	for i := range 3 {
		l, err := s.Reload(context.Background())
		if err != nil {
			t.Fatalf("Reload() #%d error = %v", i, err)
		}
		ledgers[i] = l
	}
	// Precondition: each Reload produces a distinct ledger pointer so the
	// latest-wins check is meaningful.
	if ledgers[0] == ledgers[1] || ledgers[1] == ledgers[2] || ledgers[0] == ledgers[2] {
		t.Fatal("TestSubscribe_LatestWins: Reload returned duplicate ledger pointers; test assumption violated")
	}

	got, ok := recvWithin(t, ch, time.Second)
	if !ok {
		t.Fatal("Subscribe: no ledger after three Reloads")
	}
	if got != ledgers[2] {
		t.Errorf("Subscribe: latest-wins: got %p, want %p (last ledger)", got, ledgers[2])
	}
}

func TestSubscribe_CancelClosesChannel(t *testing.T) {
	s := newTestSession(t)
	ch, cancel := s.Subscribe()
	cancel() // synchronous: cancel returns after channel is closed

	if _, ok := <-ch; ok {
		t.Error("Subscribe: channel not closed after cancel")
	}
}

func TestSubscribe_CancelIdempotent(t *testing.T) {
	s := newTestSession(t)
	_, cancel := s.Subscribe()

	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cancel()
		}()
	}
	wg.Wait() // must not panic
}

func TestSubscribe_CancelStopsDelivery(t *testing.T) {
	s := newTestSession(t)
	ch, cancel := s.Subscribe()
	cancel()

	if _, err := s.Reload(context.Background()); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	// Channel is closed; for-range terminates; no late delivery arrives.
	for range ch {
		t.Error("Subscribe: received value after cancel")
	}
}

func TestSubscribe_MultipleSubscribers(t *testing.T) {
	s := newTestSession(t)

	const n = 10
	channels := make([]<-chan *ast.Ledger, n)
	cancels := make([]func(), n)
	for i := range n {
		channels[i], cancels[i] = s.Subscribe()
		defer cancels[i]()
	}

	ledger, err := s.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	for i, ch := range channels {
		got, ok := recvWithin(t, ch, time.Second)
		if !ok {
			t.Errorf("Subscribe: subscriber %d: no delivery after Reload", i)
			continue
		}
		if got != ledger {
			t.Errorf("Subscribe: subscriber %d: got %p, want %p", i, got, ledger)
		}
	}
}

func TestSubscribe_AfterClose(t *testing.T) {
	s := newTestSession(t)
	s.Close()

	ch, cancel := s.Subscribe()
	cancel() // must not panic

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("Subscribe after Close: channel not already closed")
		}
	default:
		t.Error("Subscribe after Close: channel not already closed (blocked)")
	}
}

func TestClose_ClosesAllSubscribers(t *testing.T) {
	s := newTestSession(t)

	const n = 3
	channels := make([]<-chan *ast.Ledger, n)
	for i := range n {
		channels[i], _ = s.Subscribe()
	}

	s.Close()

	for i, ch := range channels {
		select {
		case _, ok := <-ch:
			if ok {
				t.Errorf("Close: subscriber %d channel not closed", i)
			}
		case <-time.After(time.Second):
			t.Errorf("Close: subscriber %d channel not closed within timeout", i)
		}
	}
}

func TestSubscribe_FailedReloadNoBroadcast(t *testing.T) {
	s := newTestSession(t)
	ch, cancel := s.Subscribe()
	defer cancel()

	s.loadFunc = func(_ context.Context, _ string, _ ...loader.Option) (*ast.Ledger, error) {
		return nil, context.DeadlineExceeded
	}

	if _, err := s.Reload(context.Background()); err == nil {
		t.Fatal("Reload() expected error, got nil")
	}

	if _, ok := recvWithin(t, ch, 50*time.Millisecond); ok {
		t.Error("Subscribe: received delivery after failed Reload")
	}
}

func TestSubscribe_NoBlockOnSlowSubscriber(t *testing.T) {
	s := newTestSession(t)
	ch, cancel := s.Subscribe()
	defer cancel()

	const n = 5
	var last *ast.Ledger
	for i := range n {
		l, err := s.Reload(context.Background())
		if err != nil {
			t.Fatalf("Reload() #%d error = %v", i, err)
		}
		last = l
	}

	// Despite never reading from ch, all 5 Reloads completed. The channel
	// holds exactly the last-delivered value.
	got, ok := recvWithin(t, ch, time.Second)
	if !ok {
		t.Fatal("Subscribe: no value after 5 Reloads")
	}
	if got != last {
		t.Errorf("Subscribe: slow subscriber: got %p, want %p (last ledger)", got, last)
	}
	// Verify channel is empty (not closed).
	if chanEmptyAndClosed(t, ch) {
		t.Error("Subscribe: channel unexpectedly closed")
	}
}
