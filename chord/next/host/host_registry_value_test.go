package host

import (
	"sync"
	"testing"
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
