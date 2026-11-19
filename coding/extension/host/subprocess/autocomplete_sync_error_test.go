//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// An installed editor that throws from setAutocompleteProvider answers the host's autocomplete.sync with its own error. Pi's setupAutocompleteProvider
// throws it to whichever caller rebuilt the provider (interactive-mode.ts:848-850), so the sync names the extension and keeps the editor's text and
// stack, in a shared cell and in an isolated one.
func TestSyncAutocompleteNamesTheExtensionWhoseEditorRefused(t *testing.T) {
	shortSockDir(t)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "refusing-editor.mjs")
			script := `export default function(pi){pi.on("session_start",(_,ctx)=>{ctx.ui.setEditorComponent(()=>({render(){return ["editor"]},handleInput(){},getText(){return ""},setText(){},setAutocompleteProvider(provider){if((provider.triggerCharacters??[]).includes("!"))throw new Error("EDITOR_SYNC_REFUSED")}}))})}`
			if err := os.WriteFile(source, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			bridge := NewUIBridge(func() {})
			ui := &autocompleteSyncUI{testUIContext: newTestUIContext(), bridge: bridge, ctx: t.Context(), installed: make(chan struct{})}
			bridge.SetUIContext(ui)
			host := newTestHost(t)
			host.SetUIBridge(bridge)
			defer host.Shutdown("test done")
			extensions, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "refusing", Source: source, Enabled: true, Isolation: isolation}})
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			if _, err := extensions[0].Handlers["session_start"][0](map[string]any{"type": "session_start"}, t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-ui.installed:
			case <-t.Context().Done():
				t.Fatal("editor was not installed")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			refused := &extension.AutocompleteProvider{TriggerCharacters: []string{"!"}}
			err := bridge.SyncAutocomplete(ctx, refused)
			got, ok := errors.AsType[*extension.EditorSyncError](err)
			if !ok {
				t.Fatalf("SyncAutocomplete returned %v, want an *extension.EditorSyncError", err)
			}
			if got.Error() != "EDITOR_SYNC_REFUSED" || got.ExtensionPath != source {
				t.Errorf("sync error = %q from %q, want EDITOR_SYNC_REFUSED from %q", got.Error(), got.ExtensionPath, source)
			}
			if stack := extension.ErrorStack(err); !strings.Contains(stack, "refusing-editor.mjs") {
				t.Errorf("sync error stack %q does not name the extension's source", stack)
			}
			// A provider the editor takes is no error: the refusal is the editor's, not the sync's.
			if err := bridge.SyncAutocomplete(ctx, &extension.AutocompleteProvider{}); err != nil {
				t.Errorf("SyncAutocomplete of a provider the editor takes returned %v", err)
			}
		})
	}
}

// unboundEditorUI is a UI whose owner loop has not applied the editor install yet: SetEditorComponent leaves the editor unbound.
type unboundEditorUI struct{ *testUIContext }

func (unboundEditorUI) SetEditorComponent(extension.EditorFactory) {}

func installTestEditor(bridge *UIBridge) {
	bridge.HandleNotifyFrom("ext", nil, &NotifyPayload{Method: NotifyEditorInstall, Args: json.RawMessage(`{"key":"k"}`)})
}

// pendingEditorBridge is a bridge with a ready UI and an installed editor the host has not bound.
func pendingEditorBridge() *UIBridge {
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(unboundEditorUI{newTestUIContext()})
	installTestEditor(bridge)
	return bridge
}

func awaitingEditor(bridge *UIBridge, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- bridge.AwaitEditorInstalled(ctx) }()
	return done
}

// requireAwaiting waits until every goroutine of the bubble is blocked and fails when the awaiting caller has returned by then.
func requireAwaiting(t *testing.T, done <-chan error) {
	t.Helper()
	synctest.Wait()
	select {
	case err := <-done:
		t.Fatalf("AwaitEditorInstalled returned %v with the editor not bound", err)
	default:
	}
}

func currentEditor(bridge *UIBridge) *editorProxy {
	bridge.mu.RLock()
	defer bridge.mu.RUnlock()
	return bridge.editor
}

// Pi's session_start handler installs its editor synchronously, so the bind that continues after the emit finds it in place. The install here is a notice the
// owner loop applies later; the awaiting caller returns only once the host bound the editor.
func TestAwaitEditorInstalledWaitsForTheHostToBindTheEditor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge := pendingEditorBridge()
		done := awaitingEditor(bridge, t.Context())
		requireAwaiting(t, done)
		currentEditor(bridge).Bind(&testEditorHost{ui: newTestUIContext()})
		if err := <-done; err != nil {
			t.Fatalf("a bound editor is in place: %v", err)
		}
	})
}

// No editor installed, or a UI that is not ready (it installs the editor when it becomes so): the bind goes on at once. A context that has ended shows it: a
// caller that waited would return the cancellation.
func TestAwaitEditorInstalledDoesNotWaitWithoutAnEditor(t *testing.T) {
	ended, cancel := context.WithCancelCause(t.Context())
	cancel(errors.New("ended"))
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(unboundEditorUI{newTestUIContext()})
	if err := bridge.AwaitEditorInstalled(ended); err != nil {
		t.Fatalf("no editor installed: %v", err)
	}
	notReady := NewUIBridge(func() {})
	installTestEditor(notReady)
	if err := notReady.AwaitEditorInstalled(ended); err != nil {
		t.Fatalf("a UI that is not ready: %v", err)
	}
	// An editor that is bound is in place whatever the state of the context. A select that chose at random between the two would fail one of these.
	bound := pendingEditorBridge()
	currentEditor(bound).releaseBound()
	for range 64 {
		if err := bound.AwaitEditorInstalled(ended); err != nil {
			t.Fatalf("a bound editor with an ended context: %v", err)
		}
	}
}

// An extension that dies, or that its owner stops, while the host has not yet bound its editor: the connection's end releases the wait. No timer ends it.
func TestAwaitEditorInstalledReturnsWhenTheExtensionEndsWhileTheBindIsPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge := pendingEditorBridge()
		done := awaitingEditor(bridge, t.Context())
		requireAwaiting(t, done)
		bridge.ClearExtensionConn("ext", nil)
		if err := <-done; err != nil {
			t.Fatalf("the extension's editor is gone, so there is nothing to wait for: %v", err)
		}
	})
}

// The session is swapped or shut down while the bind is pending and the extension keeps its editor unbound: the context of the session ends the wait.
func TestAwaitEditorInstalledReturnsTheCauseOfItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge := pendingEditorBridge()
		ctx, cancel := context.WithCancelCause(t.Context())
		done := awaitingEditor(bridge, ctx)
		requireAwaiting(t, done)
		stop := errors.New("session ended")
		cancel(stop)
		if err := <-done; !errors.Is(err, stop) {
			t.Fatalf("AwaitEditorInstalled returned %v, want the cause of its context", err)
		}
	})
}

// An extension that installs a second editor replaces the first, which releases it: the bind waits for the editor that is now installed.
func TestAwaitEditorInstalledWaitsForTheEditorThatReplacedTheOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge := pendingEditorBridge()
		first := currentEditor(bridge)
		done := awaitingEditor(bridge, t.Context())
		requireAwaiting(t, done)
		installTestEditor(bridge)
		if second := currentEditor(bridge); second == first {
			t.Fatal("the second install did not replace the first editor")
		}
		requireAwaiting(t, done)
		currentEditor(bridge).releaseBound()
		if err := <-done; err != nil {
			t.Fatalf("the replacing editor is bound: %v", err)
		}
	})
}

// gatedEditorUI holds the host's dispatcher inside the application of the editor install, as an owner loop that is busy does.
type gatedEditorUI struct {
	*testUIContext
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

// open lets the application of the install go on; a test that fails opens it too, so the host's shutdown does not wait for it.
func (u *gatedEditorUI) open() { u.once.Do(func() { close(u.release) }) }

func (u *gatedEditorUI) SetEditorComponent(factory extension.EditorFactory) {
	close(u.entered)
	<-u.release
	u.testUIContext.SetEditorComponent(factory)
}

// parkedIn reports whether a goroutine waits in a function whose name contains fn.
func parkedIn(fn string) bool {
	buf := make([]byte, 1<<20)
	for g := range strings.SplitSeq(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		header, _, _ := strings.Cut(g, "\n")
		if strings.Contains(header, "[select") && strings.Contains(g, fn) {
			return true
		}
	}
	return false
}

// Pi's emit of session_start returns once the handler has run, and an editor the handler installed is in place. The install is a notice the host dispatches in
// order after the frames before it; the handler's reply is accepted only once that dispatch is done, whatever the dispatcher is busy with.
func TestSessionStartReplyWaitsForTheEditorInstallNotice(t *testing.T) {
	shortSockDir(t)
	source := filepath.Join(t.TempDir(), "installing-editor.mjs")
	script := `export default function(pi){pi.on("session_start",(_,ctx)=>{ctx.ui.setEditorComponent(()=>({render(){return ["editor"]},handleInput(){},getText(){return ""},setText(){}}))})}`
	if err := os.WriteFile(source, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	bridge := NewUIBridge(func() {})
	ui := &gatedEditorUI{testUIContext: newTestUIContext(), entered: make(chan struct{}), release: make(chan struct{})}
	bridge.SetUIContext(ui)
	host := newTestHost(t)
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")
	defer ui.open()
	extensions, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "installing", Source: source, Enabled: true}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	returned := make(chan error, 1)
	go func() {
		_, err := extensions[0].Handlers["session_start"][0](map[string]any{"type": "session_start"}, t.Context())
		returned <- err
	}()
	select {
	case <-ui.entered:
	case <-t.Context().Done():
		t.Fatal("the editor install did not reach the UI")
	}
	// The dispatcher is inside the install; the reply of the handler, which the runtime sent after it, waits for the dispatcher.
	for !parkedIn("awaitReplyDispatch") {
		select {
		case err := <-returned:
			t.Fatalf("the session_start reply was accepted (%v) before the host dispatched the editor install", err)
		case <-t.Context().Done():
			t.Fatal("the session_start reply neither arrived nor waited for the dispatcher")
		case <-time.After(time.Millisecond):
		}
	}
	ui.open()
	if err := <-returned; err != nil {
		t.Fatalf("session_start: %v", err)
	}
	if currentEditor(bridge) == nil {
		t.Fatal("the handler returned before its editor was installed")
	}
}
