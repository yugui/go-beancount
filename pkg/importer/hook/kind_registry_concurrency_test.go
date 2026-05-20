package hook

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrency_KindRegistry(t *testing.T) {
	withCleanKindRegistry(t)

	const numReaders = 8
	const numKinds = 20

	var wg sync.WaitGroup

	// KindNames readers run throughout; they race with the writers below.
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = KindNames()
			}
		}()
	}

	// New readers also start before registrations complete.
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

	for i := 0; i < numKinds; i++ {
		kind := fmt.Sprintf("kind-%02d", i)
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			f := FactoryFunc(func(name string, decode func(dest any) error) (Hook, error) {
				return &fakeHook{name: name}, nil
			})
			RegisterFactory(k, f)
		}(kind)
	}

	wg.Wait()

	names := KindNames()
	if len(names) != numKinds {
		t.Errorf("KindNames() len = %d, want %d", len(names), numKinds)
	}
}
