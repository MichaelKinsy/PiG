package host

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
)

// A registry value's watch ends from the goroutine of its context's listener; its unfollow must run as a section of the
// host, as the other code touching the registry does (Pi runs both on one event loop). Under -race, aborting the watch
// while the host closes was a data race between RegistryValueSource.Unfollowed and RegistryValueSource.Close.
func TestRegistryWatchAbortedWhileTheHostClosesDoesNotRace(t *testing.T) {
	for range 100 {
		host, _ := rvPlugins(t, HostOptions{}, rvPlugin("@test/sum", func(h FacetHost, self FacetSelf) error {
			if err := rvServeSum(h, rvTotalCompose, nil); err != nil {
				return err
			}
			return rvSetter(1)(h, self)
		}))
		value, err := host.Registry(background, rvSum, GetOptions{})
		must(t, err)
		ctx, cancel := context.WithCancel(background)
		watch, err := value.Watch(ctx)
		must(t, err)
		watch.Start(func(context.Context, chord.NativeWatchCall[any]) error { return nil })
		var done sync.WaitGroup
		done.Add(2)
		go func() { defer done.Done(); cancel() }()
		go func() { defer done.Done(); _ = host.Close(background) }()
		done.Wait()
	}
}
