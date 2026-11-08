package host

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
)

// RegistryValue reads a registry's composition inside a section of the host's loop: a registry value source composes when it
// is read, so a read from another goroutine races a contribution changed meanwhile. Run under -race.
func TestRegistryValueReadsRaceFreeWhileAContributionChanges(t *testing.T) {
	registry := benchRegistryOf(t, 4)
	var readers sync.WaitGroup
	stop := make(chan struct{})
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
				if _, err := registry.host.RegistryValue(background, benchSum, GetOptions{}); err != nil {
					t.Errorf("RegistryValue: %v", err)
					return
				}
			}
		}()
	}
	for i := range 500 {
		if err := registry.writables[i%len(registry.writables)].Set(float64(i + 1)); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
	value, err := registry.host.RegistryValue(background, benchSum, GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := value.(float64); !ok {
		t.Fatalf("value is %T, want the composed sum", value)
	}
}

// A registry value source's Value and Watch are sections of the host's loop: a goroutine of a native's opener or of a job
// reads and watches it while objects go live and park, and the source composes when it is read and records the waiters of
// the objects that park and the follows of its watches. Run under -race.
func TestRegistryValueSourceReadAndWatchedFromGoroutinesDoNotRaceTheContributions(t *testing.T) {
	registry := benchRegistryOf(t, 4)
	source, err := registry.host.Registry(background, benchSum, GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var readers sync.WaitGroup
	stop := make(chan struct{})
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
				_ = source.Value()
				ctx, cancel := context.WithCancel(background)
				watch, err := source.Watch(ctx)
				if err != nil {
					t.Errorf("Watch: %v", err)
					cancel()
					return
				}
				watch.Start(func(context.Context, chord.NativeWatchCall[any]) error { return nil })
				cancel()
			}
		}()
	}
	for i := range 500 {
		if err := registry.writables[i%len(registry.writables)].Set(float64(i + 1)); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
}
