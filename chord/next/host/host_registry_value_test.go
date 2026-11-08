package host

import (
	"sync"
	"testing"
)

// A registry value source composes when it is read: a read from another goroutine runs as a section of the host's loop, so it
// cannot race a contribution changed there meanwhile. Run under -race.
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
				source, err := registry.host.Registry(background, benchSum, GetOptions{})
				if err != nil {
					t.Errorf("Registry: %v", err)
					return
				}
				if _, ok := source.Value().(float64); !ok {
					t.Errorf("Value is %T, want the composed sum", source.Value())
					return
				}
			}
		}()
	}
	for i := range 500 {
		// A change is made inside a section of the host's loop, as an object's code makes it.
		var err error
		registry.host.(*chordHost).enter(func() { err = registry.writables[i%len(registry.writables)].Set(float64(i + 1)) })
		if err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
}
