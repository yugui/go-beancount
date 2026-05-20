package importer

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestConcurrency_KindRegistryRegisterAndLookup(t *testing.T) {
	withCleanKindRegistry(t)

	const numReaders = 8
	const numKinds = 20

	var wg sync.WaitGroup

	// Start readers before registrations begin.
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = KindNames()
			}
		}()
	}

	// Register factories concurrently with the readers.
	var regWg sync.WaitGroup
	for i := 0; i < numKinds; i++ {
		kind := fmt.Sprintf("kind-%02d", i)
		regWg.Add(1)
		go func(k string) {
			defer regWg.Done()
			RegisterFactory(k, FactoryFunc(func(name string, decode func(dest any) error) (Importer, error) {
				return &fakeImporter{name: name}, nil
			}))
		}(kind)
	}

	regWg.Wait()

	// New readers that interleave with the tail of registrations.
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numKinds; j++ {
				kind := fmt.Sprintf("kind-%02d", j)
				_, _ = New(kind, "test", func(dest any) error { return nil })
			}
		}()
	}

	wg.Wait()

	names := KindNames()
	if len(names) != numKinds {
		t.Errorf("KindNames() len = %d, want %d", len(names), numKinds)
	}
}

// TestConcurrency_FrozenImporter pins the contract that Identify and Extract
// are safe for concurrent invocation on a fully-constructed Importer.
func TestConcurrency_FrozenImporter(t *testing.T) {
	const numGoroutines = 16
	const numOps = 50

	imp := &fakeImporter{
		name:       "frozen",
		identifyFn: func(in Input) bool { return true },
		extractFn: func(in Input) (Output, error) {
			return Output{}, nil
		},
	}

	in := newTestInput("test.csv", "content")

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				_ = imp.Identify(context.Background(), in)
				_, _ = imp.Extract(context.Background(), in)
			}
		}()
	}
	wg.Wait()
}
