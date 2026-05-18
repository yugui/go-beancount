package hook

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrency_RegisterAndLookup(t *testing.T) {
	withCleanRegistry(t)

	const numReaders = 8
	const numHooks = 20

	var wg sync.WaitGroup

	// Names readers run throughout; they race with the writers below.
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = Names()
			}
		}()
	}

	// Lookup readers also start before registrations complete.
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numHooks; j++ {
				name := fmt.Sprintf("hook-%02d", j)
				_, _ = Lookup(name)
			}
		}()
	}

	for i := 0; i < numHooks; i++ {
		name := fmt.Sprintf("hook-%02d", i)
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			Register(n, &fakeHook{name: n})
		}(name)
	}

	wg.Wait()

	names := Names()
	if len(names) != numHooks {
		t.Errorf("Names() len = %d, want %d", len(names), numHooks)
	}
}
