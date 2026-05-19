package csvimp

import (
	"context"
	"sync"
	"testing"
)

// TestConcurrentIdentifyExtract verifies the goroutine-safety guarantee:
// concurrent Identify and Extract calls on the same [Importer] must not
// race. Run with -race.
func TestConcurrentIdentifyExtract(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	body := "Date,Amount\n2024-01-15,-4.50\n2024-01-16,100.00\n"

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			in := inputFromString("/tmp/concurrent.csv", "", body)
			if !imp.Identify(context.Background(), in) {
				t.Errorf("Identify(concurrent.csv): returned false")
				return
			}
			out, err := imp.Extract(context.Background(), in)
			if err != nil {
				t.Errorf("Extract(concurrent.csv): %v", err)
				return
			}
			if len(out.Directives) != 2 {
				t.Errorf("got %d directives, want 2", len(out.Directives))
			}
		}()
	}
	wg.Wait()
}
