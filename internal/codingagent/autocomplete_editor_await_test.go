package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// swallowingEditorUI is a UI whose owner loop never applies the editor install: the extension's editor stays unbound.
type swallowingEditorUI struct{ *ExtUIContext }

func (swallowingEditorUI) SetEditorComponent(extension.EditorFactory) {}

// installExtensionEditor delivers the notice an extension's setEditorComponent sends to the bridge, from the goroutine of its session_start handler.
func installExtensionEditor(bridge *subprocess.UIBridge) {
	bridge.HandleNotifyFrom("ext", nil, &subprocess.NotifyPayload{Method: subprocess.NotifyEditorInstall, Args: json.RawMessage(`{"key":"k"}`)})
}

// A session_start handler installs its editor while the owner loop is running another task (a modal extension request's answer, a render): the install
// queues behind it, the bind that waits for the editor does so off the loop, and the loop applies the install when it is free. The bind neither returns
// before the editor is in place nor deadlocks with the loop.
func TestInstalledEditorIsBoundWhileTheOwnerLoopIsBusyAtTheInstall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, ctx := newPhaseShutdownMode(t)
		m.backgroundCtx = ctx
		bridge := subprocess.NewUIBridge(func() {})
		m.opts.SubprocessUIBridge = bridge
		bridge.SetUIContext(&ExtUIContext{m: m})
		busy := make(chan struct{})
		waited := make(chan error, 1)
		phase := make(chan error, 1)
		go func() {
			phase <- m.awaitExtensionUI(ctx, func(ctx context.Context) error {
				m.runOnMain(ctx, func() { <-busy })
				installExtensionEditor(bridge)
				waited <- m.awaitInstalledEditor(ctx)
				return nil
			})
		}()
		synctest.Wait()
		select {
		case err := <-waited:
			t.Fatalf("the bind went on (%v) while the loop had not applied the install", err)
		default:
		}
		close(busy)
		if err := <-waited; err != nil {
			t.Fatalf("awaitInstalledEditor = %v, want nil once the loop applied the install", err)
		}
		if err := <-phase; err != nil {
			t.Fatalf("awaitExtensionUI = %v", err)
		}
		if m.remoteEditor == nil {
			t.Fatal("the installed editor is not in place when the bind goes on")
		}
	})
}

// The session shuts down while the extension's editor is installed and not bound: the wait ends with the phase, which returns errInteractiveShutdown, and the
// bubble holds no goroutine of the wait at its end.
func TestShutdownWhileTheInstalledEditorIsNotBoundEndsTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, ctx := newPhaseShutdownMode(t)
		bridge := subprocess.NewUIBridge(func() {})
		m.opts.SubprocessUIBridge = bridge
		bridge.SetUIContext(swallowingEditorUI{&ExtUIContext{m: m}})
		waited := make(chan error, 1)
		err := m.awaitExtensionUI(ctx, func(ctx context.Context) error {
			installExtensionEditor(bridge)
			m.requestShutdown()
			err := m.awaitInstalledEditor(ctx)
			waited <- err
			return err
		})
		if !errors.Is(err, errInteractiveShutdown) {
			t.Fatalf("awaitExtensionUI = %v, want errInteractiveShutdown", err)
		}
		if err := <-waited; err == nil {
			t.Fatal("the wait for an editor that was never bound returned nil at shutdown")
		}
	})
}
