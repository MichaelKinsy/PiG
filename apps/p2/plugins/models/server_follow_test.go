package models

import (
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

type releasableNative struct {
	mu        sync.Mutex
	resources *Resources
	released  bool
}

func (n *releasableNative) Value() (any, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.released {
		return nil, errors.New("p2.model-config native closed")
	}
	return n.resources, nil
}

func (n *releasableNative) release() {
	n.mu.Lock()
	n.released = true
	n.mu.Unlock()
}

// A change published as the facet closes (its native is released before Resources is closed) reaches the follow's
// listener after the close began: the read fails with an error where it once panicked the process.
func TestFollowedStateOfAReleasedNativeFailsInsteadOfPanicking(t *testing.T) {
	native := &releasableNative{resources: openFake(t, t.TempDir())}
	read := func(*Resources) (delta.JsonValue, error) { return delta.NewJsonObject(0), nil }
	var failed sync.WaitGroup
	for range 8 {
		failed.Add(1)
		go func() {
			defer failed.Done()
			stop := subscribeResources(native, func() {
				_, _ = readResources(native, read)
			})
			defer stop()
			for range 50 {
				native.resources.Publish()
			}
		}()
	}
	native.release()
	failed.Wait()
	if _, err := readResources(native, read); err == nil {
		t.Fatal("a released native was read")
	}
	stop := subscribeResources(native, func() { t.Error("a released native was subscribed") })
	stop()
}
