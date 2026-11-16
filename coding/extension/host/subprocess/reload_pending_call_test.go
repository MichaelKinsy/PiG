package subprocess

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestReloadLeavesPendingCustomCallPending pins what a reload does to the command that awaits a ui.custom overlay of an extension it replaces or removes. Pi's reload
// removes the extension overlays and "leaves the promises pending" (interactive-mode.ts:2592-2598 resetExtensionUI, from handleReloadCommand 6628): the screen shows
// nothing, the command never goes on, and the old instance lives until its command or the session ends. Probed on Pi 1.5.2 by extensions-runtime scenarios 109 (the
// owner replaced) and 110 (the owner removed): the overlay text is gone after the reload and the code after the await never runs. The host takes the overlay off
// the screen at the reload (RetireExtensionConn) in both isolation modes and answers nothing.
func TestReloadLeavesPendingCustomCallPending(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping packed SDK builds in short mode")
	}
	t.Parallel()
	for _, sdk := range focusedPackedSDKCases() {
		for _, isolation := range []string{"shared-ok", "strict"} {
			for _, owner := range []string{"replaced", "removed"} {
				t.Run(sdk.name+"/"+isolation+"/"+owner, func(t *testing.T) {
					configs := sdk.configs(t)
					for i := range configs {
						configs[i].Isolation = isolation
					}
					fakeUI := newTestUIContext()
					host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					bridge := NewUIBridge(func() {})
					bridge.SetUIContext(fakeUI)
					bridge.SetHostAction("streamModel", func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error) {
						return packedTestModelStream()
					})
					host.SetUIBridge(bridge)
					defer host.Shutdown("test done")

					ctx, cancel := buildContext(t)
					defer cancel()
					loaded, errs := host.LoadAll(ctx, configs)
					if len(errs) > 0 || len(loaded) != 2 {
						t.Fatalf("LoadAll loaded=%d errors=%v", len(loaded), errs)
					}
					commandDone := make(chan error, 1)
					go func() { commandDone <- loaded[0].Commands["focused"].Handler(context.Background(), "") }()
					handle, _ := fakeUI.awaitOverlay(t, 5*time.Second)

					after := configs
					if owner == "removed" {
						after = nil
					}
					host.SetConfigLoader(func() ([]ExtConfig, error) { return after, nil })
					if _, err := host.Reload(ctx); err != nil {
						t.Fatal(err)
					}
					select {
					case <-handle.Retired():
					case <-time.After(5 * time.Second):
						t.Fatal("reload did not take the overlay off the screen")
					}
					select {
					case <-handle.Done():
						t.Fatal("reload ended the overlay's call; Pi leaves it pending")
					default:
					}
					select {
					case err := <-commandDone:
						t.Fatalf("the command that awaited the overlay returned after the reload: %v; Pi leaves it awaiting", err)
					case <-time.After(300 * time.Millisecond):
					}
					host.Shutdown("session ended")
					select {
					case <-commandDone:
					case <-time.After(5 * time.Second):
						t.Fatal("the command remained blocked after the session ended")
					}
				})
			}
		}
	}
}
