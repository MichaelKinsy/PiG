package subprocess

import (
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// pig additive (D614): a client that attaches changes the abilities every extension reads, and the bridge pushes them
// off the caller's goroutine. The push worker ends when nothing is pending, so a session with a view client attached and
// no mount holds no goroutine for it.
func TestStatePushWorkerEndsWhenIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := NewUIBridge(func() {})
		before := testenv.LiveGoroutines()
		var pushes atomic.Int32
		b.OnStateChanged = func() { pushes.Add(1) }
		abilities := uiview.ClientAbilities{
			Slots: []uiview.Slot{uiview.SlotPanel}, Nodes: []string{ViewKindText}, ImageFormats: []string{},
			Limits: uiview.Limits{Nodes: 8, Depth: 4, SnapshotBytes: 4096, TextBytes: 1024, Mounts: 1, IntentArgBytes: 64},
		}
		if err := b.SetClientAbilities(&abilities); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if n := pushes.Load(); n != 1 {
			t.Fatalf("attaching a client pushed the state %d times, want 1", n)
		}
		if n := testenv.LiveGoroutines() - before; n != 0 {
			t.Fatalf("after the push the bridge holds %d goroutine(s)", n)
		}
		b.SetFrontend(true)
		synctest.Wait()
		if n := pushes.Load(); n != 2 {
			t.Fatalf("a change after the worker went idle pushed the state %d times in all, want 2", n)
		}
		if err := b.SetClientAbilities(nil); err != nil {
			t.Fatal(err)
		}
		b.SetFrontend(false)
		synctest.Wait()
		if n := pushes.Load(); n < 3 || n > 4 {
			t.Fatalf("two changes pushed the state %d times in all, want 3 or 4", n)
		}
		b.StopFrontendPushes()
		b.SetFrontend(true)
		synctest.Wait()
		if n := pushes.Load(); n > 4 {
			t.Fatalf("a change after StopFrontendPushes pushed the state (%d)", n)
		}
	})
}
