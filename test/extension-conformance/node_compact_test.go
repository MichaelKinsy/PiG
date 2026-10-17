package extensionconformance

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// compactRig gives the Host a compaction that ends when the test releases it, so a row can observe the extension while compaction is pending.
type compactRig struct {
	*nodeAPIRig
	started chan struct{}
	release chan struct{}
}

func newCompactRig(t *testing.T, isolation string, fixture nodeAPIFixture) *compactRig {
	t.Helper()
	started, release := make(chan struct{}, 4), make(chan struct{})
	actions := &subprocess.HostCallbacks{
		IsIdle:             func() bool { return true },
		HasPendingMessages: func() bool { return false },
		Compact: func(_ context.Context, opts *extension.CompactOptions) {
			started <- struct{}{}
			go func() {
				<-release
				opts.OnComplete(extension.CompactionResult{Summary: "short", FirstKeptEntryID: "e9", TokensBefore: 12})
			}()
		},
	}
	return &compactRig{nodeAPIRig: newNodeAPIRig(t, isolation, actions, fixture), started: started, release: release}
}

// A handler that starts compaction and returns does not cancel it: the call outlives its request and its callbacks still run (agent-session.ts:3370 detaches it from the calling handler).
func TestNodeCompactOutlivesTheHandlerThatStartedIt(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newCompactRig(t, isolation, nodeAPIFixture{name: "starter", code: `
export default function (pi) {
  pi.registerCommand("go", { handler: async (_args, ctx) => {
    ctx.compact({ onComplete: () => ctx.ui.notify("completed"), onError: (e) => ctx.ui.notify("error:" + e.message) });
    ctx.ui.notify("handler-returned");
  } });
}
`})
		rig.command("starter", "go", "")
		rig.waitNotification("handler-returned")
		select {
		case <-rig.started:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("compaction was not started")
		}
		close(rig.release)
		rig.waitNotification("completed")
	})
}
