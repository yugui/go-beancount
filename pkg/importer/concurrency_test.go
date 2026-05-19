package importer

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrency_RegisterAndLookup(t *testing.T) {
	withCleanRegistry(t)

	const numReaders = 8
	const numImporters = 20

	var wg sync.WaitGroup

	// Start readers before registrations begin.
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = Names()
			}
		}()
	}

	// Register importers concurrently with the readers.
	var regWg sync.WaitGroup
	for i := 0; i < numImporters; i++ {
		name := fmt.Sprintf("importer-%02d", i)
		regWg.Add(1)
		go func(n string) {
			defer regWg.Done()
			Register(n, &fakeImporter{name: n})
		}(name)
	}

	regWg.Wait()

	// Lookup readers that interleave with the tail of registrations.
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numImporters; j++ {
				name := fmt.Sprintf("importer-%02d", j)
				_, _ = Lookup(name) // probe locking
			}
		}()
	}

	wg.Wait()

	// Verify all registrations landed.
	names := Names()
	if len(names) != numImporters {
		t.Errorf("Names() len = %d, want %d", len(names), numImporters)
	}
}
