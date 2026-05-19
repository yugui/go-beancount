package hook

import (
	"errors"
	"sync"
	"testing"
)

func TestFactory_ParallelCallsProduceIndependentInstances(t *testing.T) {
	// Each factory call receives a distinct name and produces an independent
	// Hook; no shared state may leak between instances.
	const n = 16
	f := FactoryFunc(func(name string, decode func(dest any) error) (Hook, error) {
		return &fakeHook{name: name}, nil
	})

	type result struct {
		h   Hook
		err error
	}
	results := make([]result, n)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		name := instanceName(i)
		idx := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := f.New(name, func(dest any) error { return nil })
			mu.Lock()
			results[idx] = result{h, err}
			mu.Unlock()
		}()
	}
	wg.Wait()

	seen := make(map[string]bool, n)
	for i, r := range results {
		if r.err != nil {
			t.Errorf("results[%d] error: %v", i, r.err)
			continue
		}
		if r.h == nil {
			t.Errorf("results[%d] Hook is nil", i)
			continue
		}
		want := instanceName(i)
		if r.h.Name() != want {
			t.Errorf("results[%d].Name() = %q, want %q", i, r.h.Name(), want)
		}
		if seen[r.h.Name()] {
			t.Errorf("duplicate instance name %q", r.h.Name())
		}
		seen[r.h.Name()] = true
	}
}

// instanceName returns a unique test instance name for index i.
func instanceName(i int) string {
	return "inst-" + string(rune('a'+i%26))
}

func TestFactory_DecodeError(t *testing.T) {
	decodeErr := errors.New("bad config")
	f := FactoryFunc(func(name string, decode func(dest any) error) (Hook, error) {
		if err := decode(new(any)); err != nil {
			return nil, err
		}
		return &fakeHook{name: name}, nil
	})

	h, err := f.New("x", func(dest any) error { return decodeErr })
	if !errors.Is(err, decodeErr) {
		t.Errorf("FactoryFunc.New: error = %v, want %v", err, decodeErr)
	}
	if h != nil {
		t.Error("FactoryFunc.New: returned non-nil Hook on decode error")
	}
}

func TestFactory_ValidationError(t *testing.T) {
	validateErr := errors.New("invalid config")
	f := FactoryFunc(func(name string, decode func(dest any) error) (Hook, error) {
		return nil, validateErr
	})

	h, err := f.New("x", func(dest any) error { return nil })
	if !errors.Is(err, validateErr) {
		t.Errorf("FactoryFunc.New: error = %v, want %v", err, validateErr)
	}
	if h != nil {
		t.Error("FactoryFunc.New: returned non-nil Hook on validation error")
	}
}

func TestFactoryFunc_ImplementsFactory(t *testing.T) {
	var _ Factory = FactoryFunc(nil)
}

func TestFactoryFunc_New(t *testing.T) {
	f := FactoryFunc(func(name string, decode func(dest any) error) (Hook, error) {
		return &fakeHook{name: name}, nil
	})
	h, err := f.New("myname", func(dest any) error { return nil })
	if err != nil {
		t.Fatalf("FactoryFunc.New: returned error: %v", err)
	}
	if got := h.Name(); got != "myname" {
		t.Errorf("FactoryFunc.New(%q).Name() = %q, want %q", "myname", got, "myname")
	}
}
