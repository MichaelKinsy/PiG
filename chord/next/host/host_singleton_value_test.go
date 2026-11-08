package host

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// A singleton's Value reads inside a section of the host: Go code reads it from the goroutines of its jobs, which race the
// commits of the object's own changes (and its seal when the host closes) when the read takes no section. Run under -race.
func TestSingletonValueReadFromGoroutinesDoesNotRaceTheObjectsChanges(t *testing.T) {
	var mu sync.Mutex
	var list *documents.Singleton
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			mu.Lock()
			list = self.State(attemptListDoc).(*documents.Singleton)
			mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	page := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	defer page.Release()
	mu.Lock()
	handle := list
	mu.Unlock()

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := handle.Value(); err != nil {
					t.Errorf("Value: %v", err)
					return
				}
			}
		}()
	}
	for range 300 {
		if _, err := handle.Change(background, func(draft *delta.Object) (any, error) {
			_, err := draft.Array("items").Push("x")
			return nil, err
		}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
	value, err := handle.Value()
	if err != nil {
		t.Fatal(err)
	}
	if items, _ := value.Value("items").([]any); len(items) != 300 {
		t.Fatalf("items = %d, want 300", len(items))
	}
}

// singletonForBench is a live singleton handle of a served object, and the host to enter its sections.
func singletonForBench(b *testing.B) (*documents.Singleton, Host) {
	b.Helper()
	var mu sync.Mutex
	var list *documents.Singleton
	host, _ := loaded(b, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			mu.Lock()
			list = self.State(attemptListDoc).(*documents.Singleton)
			mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	page := mustGet(b, host, attemptPage, jsonObject("id", 1.0))
	b.Cleanup(page.Release)
	mu.Lock()
	defer mu.Unlock()
	return list, host
}

// BenchmarkSingletonValueOffLoop is a read from a job's goroutine: one section of the host's loop.
func BenchmarkSingletonValueOffLoop(b *testing.B) {
	handle, _ := singletonForBench(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := handle.Value(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSingletonValueInSection is a read from inside a section (a handler, a watch listener): inline.
func BenchmarkSingletonValueInSection(b *testing.B) {
	handle, host := singletonForBench(b)
	b.ReportAllocs()
	host.(*chordHost).enter(func() {
		for b.Loop() {
			if _, err := handle.Value(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
