// White-box tests for Session. The package is tested from within (package
// session) to access the unexported loadFunc field, which serves as a test
// hook to count loader invocations and inject blocking stubs. This is a
// documented CLAUDE.md exception: measuring load count and driving coalescing
// tests via the exported API alone would require fragile timing-based tests.
package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/loader"
)

// writeFile writes content to path, failing the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// countingLoader wraps loader.LoadFile, incrementing count on each call.
func countingLoader(count *atomic.Int64) func(context.Context, string, ...loader.Option) (*ast.Ledger, error) {
	return func(ctx context.Context, path string, opts ...loader.Option) (*ast.Ledger, error) {
		count.Add(1)
		return loader.LoadFile(ctx, path, opts...)
	}
}

// stoppableLoader signals entered once it starts, blocks until release closes
// (or ctx is done), then delegates to loader.LoadFile. count tracks calls.
func stoppableLoader(count *atomic.Int64, entered chan<- struct{}, release <-chan struct{}) func(context.Context, string, ...loader.Option) (*ast.Ledger, error) {
	return func(ctx context.Context, path string, opts ...loader.Option) (*ast.Ledger, error) {
		count.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return loader.LoadFile(ctx, path, opts...)
	}
}

func openCurrency(ledger *ast.Ledger, account string) string {
	for _, d := range ledger.All() {
		if o, ok := d.(*ast.Open); ok && o.Account == ast.Account(account) {
			if len(o.Currencies) > 0 {
				return o.Currencies[0]
			}
		}
	}
	return ""
}

func TestNew_LoadsLedger(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	ledger, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if openCurrency(ledger, "Assets:Bank") == "" {
		t.Error("Snapshot() ledger missing expected Open directive")
	}
}

func TestNew_EmptyRootPath(t *testing.T) {
	s, err := New("")
	if s != nil || err == nil {
		t.Errorf("New(\"\") = (%v, %v), want (nil, non-nil error)", s, err)
	}
}

func TestNew_NonExistentRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "nonexistent.beancount")

	// loader.LoadFile records I/O errors as Diagnostics rather than returning
	// an error, so New succeeds and returns a non-nil session whose cached
	// ledger carries an Error-severity diagnostic for the missing file.
	s, err := New(root)
	if err != nil {
		t.Fatalf("New() with non-existent root returned unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("New() with non-existent root returned nil session")
	}
	defer s.Close()
	ledger, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	var hasError bool
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Error("Snapshot() for non-existent root: expected Error diagnostic, got none")
	}
}

func TestNew_OverridesUserWithOverlay(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	// User supplies an overlay that would change the currency to EUR. The
	// session appends its own WithOverlay(empty-map) last, which wins and
	// clears the user-supplied one. The disk content (USD) should load.
	userOverlay := map[string][]byte{
		root: []byte("2024-01-01 open Assets:Bank EUR\n"),
	}
	s, err := New(root, loader.WithOverlay(userOverlay))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	ledger, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	for _, d := range ledger.All() {
		if o, ok := d.(*ast.Open); ok && o.Account == "Assets:Bank" {
			if len(o.Currencies) == 1 && o.Currencies[0] == "EUR" {
				t.Error("Snapshot(): user-supplied WithOverlay was not overridden by session's own overlay")
			}
		}
	}
}

func TestSnapshot_CachedAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	var count atomic.Int64
	s.loadFunc = countingLoader(&count)
	// count starts at 0; New used the real loader before hook was installed.

	if _, err := s.Snapshot(context.Background()); err != nil {
		t.Fatalf("Snapshot() #1 error = %v", err)
	}
	if _, err := s.Snapshot(context.Background()); err != nil {
		t.Fatalf("Snapshot() #2 error = %v", err)
	}
	if n := count.Load(); n != 0 {
		t.Errorf("Snapshot() on valid cache: load count = %d, want 0", n)
	}
}

func TestSnapshot_InvalidationByOverlay(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	if err := s.SetOverlay(root, []byte("2024-01-01 open Assets:Bank EUR\n")); err != nil {
		t.Fatalf("SetOverlay() error = %v", err)
	}
	ledger, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() after SetOverlay error = %v", err)
	}
	currency := openCurrency(ledger, "Assets:Bank")
	if currency != "EUR" {
		t.Errorf("Snapshot() after SetOverlay: currency = %q, want EUR", currency)
	}
}

func TestSetOverlay_NonAbsoluteError(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	err = s.SetOverlay("relative/path.beancount", []byte("content"))
	if !errors.Is(err, ErrOverlayKeyNotAbsolute) {
		t.Errorf("SetOverlay(relative) = %v, want ErrOverlayKeyNotAbsolute", err)
	}
	// Cache remains valid; Snapshot still serves disk.
	ledger, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() after failed SetOverlay error = %v", err)
	}
	if ledger == nil {
		t.Error("Snapshot() after failed SetOverlay returned nil ledger")
	}
}

func TestSetOverlay_EmptyPathError(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	err = s.SetOverlay("", []byte("content"))
	if !errors.Is(err, ErrOverlayKeyNotAbsolute) {
		t.Errorf("SetOverlay(\"\") = %v, want ErrOverlayKeyNotAbsolute", err)
	}
}

func TestSetOverlay_ContentBorrowed(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	// Use a buffer long enough to overwrite "Assets:Borrowed" with "Assets:Mutated0".
	buf := []byte("2024-01-01 open Assets:Borrowed EUR\n")
	if err := s.SetOverlay(root, buf); err != nil {
		t.Fatalf("SetOverlay() error = %v", err)
	}
	// First snapshot: should see "Assets:Borrowed".
	ledger1, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() #1 error = %v", err)
	}
	if openCurrency(ledger1, "Assets:Borrowed") != "EUR" {
		t.Error("Snapshot() #1: overlay content not applied")
	}

	// Mutate buf after Snapshot; since content is borrowed (no copy), a
	// subsequent reload will use the mutated bytes. This test primarily
	// verifies that SetOverlay does not defensively copy — we observe the
	// mutation on next Snapshot by forcing a reload via SetOverlay again.
	copy(buf[16:], "Assets:Mutated0")
	// Re-set the same key to trigger invalidation (content pointer unchanged).
	if err := s.SetOverlay(root, buf); err != nil {
		t.Fatalf("SetOverlay() #2 error = %v", err)
	}
	ledger2, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() #2 error = %v", err)
	}
	if openCurrency(ledger2, "Assets:Mutated0") != "EUR" {
		t.Error("Snapshot() #2: mutated buffer not reflected (borrow contract broken)")
	}
}

func TestClearOverlay_Idempotent(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	var count atomic.Int64
	s.loadFunc = countingLoader(&count)

	// ClearOverlay on a key that was never set.
	if err := s.ClearOverlay(root); err != nil {
		t.Fatalf("ClearOverlay() on absent key error = %v", err)
	}
	if n := count.Load(); n != 0 {
		t.Errorf("ClearOverlay() on absent key triggered reload: load count = %d, want 0", n)
	}
	// Snapshot must not trigger a reload either (cache still valid).
	if _, err := s.Snapshot(context.Background()); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if n := count.Load(); n != 0 {
		t.Errorf("Snapshot() after no-op ClearOverlay: load count = %d, want 0", n)
	}
}

func TestClearOverlay_RemovesEntry(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	// Set overlay → currency becomes EUR.
	if err := s.SetOverlay(root, []byte("2024-01-01 open Assets:Bank EUR\n")); err != nil {
		t.Fatalf("SetOverlay() error = %v", err)
	}
	ledger, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() after SetOverlay error = %v", err)
	}
	if openCurrency(ledger, "Assets:Bank") != "EUR" {
		t.Error("Snapshot() after SetOverlay: EUR not found")
	}

	// Clear overlay → currency reverts to USD.
	if err := s.ClearOverlay(root); err != nil {
		t.Fatalf("ClearOverlay() error = %v", err)
	}
	ledger, err = s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() after ClearOverlay error = %v", err)
	}
	if openCurrency(ledger, "Assets:Bank") != "USD" {
		t.Errorf("Snapshot() after ClearOverlay: currency = %q, want USD", openCurrency(ledger, "Assets:Bank"))
	}
}

func TestOverlays_ReturnsCopy(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	extraPath := filepath.Join(dir, "extra.beancount")
	if err := s.SetOverlay(extraPath, []byte("content")); err != nil {
		t.Fatalf("SetOverlay() error = %v", err)
	}

	m := s.Overlays()
	// Mutate the returned map.
	delete(m, extraPath)
	m[filepath.Join(dir, "injected.beancount")] = []byte("injected")

	// Session state must be unchanged.
	m2 := s.Overlays()
	if _, ok := m2[extraPath]; !ok {
		t.Error("Overlays(): delete from returned map affected session state")
	}
	if _, ok := m2[filepath.Join(dir, "injected.beancount")]; ok {
		t.Error("Overlays(): add to returned map affected session state")
	}
}

func TestReload_Forced(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	// Snapshot → stale cached version.
	if _, err := s.Snapshot(context.Background()); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	// Modify disk content.
	writeFile(t, root, "2024-01-01 open Assets:Updated EUR\n")

	// Reload forces a fresh load.
	ledger, err := s.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if openCurrency(ledger, "Assets:Updated") != "EUR" {
		t.Errorf("Reload() after disk change: currency = %q, want EUR", openCurrency(ledger, "Assets:Updated"))
	}
}

func TestReload_Serialized(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	const n = 10
	var count atomic.Int64
	// about counts goroutines that have signaled intent to call Reload.
	// The runner spins until about reaches n-1 before proceeding, which
	// guarantees those goroutines call Reload while s.reloading is still true.
	var about atomic.Int64
	entered := make(chan struct{}, 1)

	s.loadFunc = func(ctx context.Context, path string, opts ...loader.Option) (*ast.Ledger, error) {
		count.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		for about.Load() < n-1 {
			runtime.Gosched()
		}
		return loader.LoadFile(ctx, path, opts...)
	}

	var wg sync.WaitGroup
	ledgers := make([]*ast.Ledger, n)
	errs := make([]error, n)
	wg.Add(n)

	go func() {
		defer wg.Done()
		ledgers[0], errs[0] = s.Reload(context.Background())
	}()
	<-entered // runner is inside loadFunc; s.reloading == true

	for i := 1; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			about.Add(1)
			ledgers[i], errs[i] = s.Reload(context.Background())
		}(i)
	}
	wg.Wait()

	if got := count.Load(); got != 1 {
		t.Errorf("Reload() serialized: load count = %d, want 1", got)
	}
	var first *ast.Ledger
	for i, l := range ledgers {
		if errs[i] != nil {
			t.Errorf("Reload() goroutine %d error = %v", i, errs[i])
			continue
		}
		if l == nil {
			t.Errorf("Reload() goroutine %d returned nil ledger", i)
			continue
		}
		if first == nil {
			first = l
		} else if first != l {
			t.Errorf("Reload() goroutine %d returned different ledger pointer", i)
		}
	}
}

func TestSnapshot_Coalesced(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	// Invalidate cache so the next Snapshot triggers a reload.
	if err := s.SetOverlay(root, []byte("2024-01-01 open Assets:Bank USD\n")); err != nil {
		t.Fatalf("SetOverlay() error = %v", err)
	}

	const n = 10
	var count atomic.Int64
	var about atomic.Int64
	entered := make(chan struct{}, 1)

	s.loadFunc = func(ctx context.Context, path string, opts ...loader.Option) (*ast.Ledger, error) {
		count.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		for about.Load() < n-1 {
			runtime.Gosched()
		}
		return loader.LoadFile(ctx, path, opts...)
	}

	var wg sync.WaitGroup
	ledgers := make([]*ast.Ledger, n)
	errs := make([]error, n)
	wg.Add(n)

	go func() {
		defer wg.Done()
		ledgers[0], errs[0] = s.Snapshot(context.Background())
	}()
	<-entered // runner inside loadFunc; s.reloading == true

	for i := 1; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			about.Add(1)
			ledgers[i], errs[i] = s.Snapshot(context.Background())
		}(i)
	}
	wg.Wait()

	if got := count.Load(); got != 1 {
		t.Errorf("Snapshot() coalesced: load count = %d, want 1", got)
	}
	var first *ast.Ledger
	for i, l := range ledgers {
		if errs[i] != nil {
			t.Errorf("Snapshot() goroutine %d error = %v", i, errs[i])
			continue
		}
		if l == nil {
			t.Errorf("Snapshot() goroutine %d returned nil ledger", i)
			continue
		}
		if first == nil {
			first = l
		} else if first != l {
			t.Errorf("Snapshot() goroutine %d returned different ledger pointer", i)
		}
	}
}

func TestSnapshot_Concurrent(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			ledger, err := s.Snapshot(context.Background())
			if err != nil {
				t.Errorf("Snapshot() concurrent: error = %v", err)
			}
			if ledger == nil {
				t.Errorf("Snapshot() concurrent: nil ledger")
			}
		}()
	}
	wg.Wait()
}

func TestSnapshot_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	// Invalidate cache so next Snapshot triggers a reload.
	if err := s.SetOverlay(root, []byte("2024-01-01 open Assets:Bank EUR\n")); err != nil {
		t.Fatalf("SetOverlay() error = %v", err)
	}

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var count atomic.Int64
	s.loadFunc = stoppableLoader(&count, entered, release)

	ctx, cancel := context.WithCancel(context.Background())

	runnerDone := make(chan error, 1)
	go func() {
		_, err := s.Snapshot(ctx)
		runnerDone <- err
	}()

	<-entered
	cancel()

	// Let the stoppable loader exit; runner's result (error or nil) is acceptable either way.
	close(release)
	<-runnerDone

	// Cache must not be poisoned; fresh ctx Snapshot must succeed.
	s.loadFunc = loader.LoadFile
	ledger, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() after canceled ctx: error = %v", err)
	}
	if ledger == nil {
		t.Error("Snapshot() after canceled ctx: nil ledger")
	}
}

func TestSnapshot_WaiterContextCanceled(t *testing.T) {
	// Path (b): a waiter parked on <-done with its own already-canceled ctx
	// must return (nil, ctx.Err()) immediately while the runner's load
	// completes normally.
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	if err := s.SetOverlay(root, []byte("2024-01-01 open Assets:Bank USD\n")); err != nil {
		t.Fatalf("SetOverlay() error = %v", err)
	}

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var count atomic.Int64
	s.loadFunc = stoppableLoader(&count, entered, release)

	// runner uses background ctx; completes normally once release closes.
	runnerResult := make(chan error, 1)
	go func() {
		_, err := s.Snapshot(context.Background())
		runnerResult <- err
	}()
	<-entered // runner is in loadFunc; s.reloading == true

	// Cancel waiter's ctx before calling Snapshot so <-ctx.Done() fires
	// immediately in the select, without affecting the runner's load.
	waiterCtx, waiterCancel := context.WithCancel(context.Background())
	waiterCancel()
	_, waiterErr := s.Snapshot(waiterCtx)
	if !errors.Is(waiterErr, context.Canceled) {
		t.Errorf("waiter Snapshot() with canceled ctx = %v, want context.Canceled", waiterErr)
	}

	// Release the runner; it should complete normally.
	close(release)
	if err := <-runnerResult; err != nil {
		t.Errorf("runner Snapshot() = %v, want nil", err)
	}

	// One load only: the runner's.
	if got := count.Load(); got != 1 {
		t.Errorf("Snapshot() load count = %d, want 1", got)
	}
}

func TestClose_Idempotent(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() #1 error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() #2 (idempotent) error = %v", err)
	}
}

func TestClose_AfterClose_Errors(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, err := s.Snapshot(context.Background()); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("Snapshot() after Close: err = %v, want ErrSessionClosed", err)
	}
	if err := s.SetOverlay(root, []byte("content")); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("SetOverlay() after Close: err = %v, want ErrSessionClosed", err)
	}
	if err := s.ClearOverlay(root); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("ClearOverlay() after Close: err = %v, want ErrSessionClosed", err)
	}
	if _, err := s.Reload(context.Background()); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("Reload() after Close: err = %v, want ErrSessionClosed", err)
	}
}

func TestClose_OverlaysStillWorks(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	writeFile(t, root, "2024-01-01 open Assets:Bank USD\n")

	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	extraPath := filepath.Join(dir, "extra.beancount")
	if err := s.SetOverlay(extraPath, []byte("content")); err != nil {
		t.Fatalf("SetOverlay() error = %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	m := s.Overlays()
	if _, ok := m[extraPath]; !ok {
		t.Errorf("Overlays() after Close: expected entry for %s missing", extraPath)
	}
}
