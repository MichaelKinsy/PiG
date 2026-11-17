package codingagent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// refusingEditorBridge is the UI bridge of an extension whose installed editor refuses the provider the host hands it: every sync answers
// the EditorSyncError the subprocess bridge builds from the editor's own error.
type refusingEditorBridge struct {
	*captureUIBridge
	refusing atomic.Bool
	// pending is the real bridge's per-connection fact: the editor's process was answered {} and no sync has reached it since.
	pending atomic.Bool
	syncs   atomic.Int32
	awaits  atomic.Int32
	err     error
}

func newRefusingEditorBridge(path, message string) *refusingEditorBridge {
	return &refusingEditorBridge{captureUIBridge: &captureUIBridge{}, err: &extension.EditorSyncError{ExtensionPath: path, Err: errors.New(message)}}
}

func (b *refusingEditorBridge) SyncAutocomplete(context.Context, *extension.AutocompleteProvider) error {
	b.syncs.Add(1)
	b.pending.Store(false)
	if b.refusing.Load() {
		return b.err
	}
	return nil
}

func (b *refusingEditorBridge) SyncPendingAutocomplete(ctx context.Context, provider *extension.AutocompleteProvider) error {
	if !b.pending.CompareAndSwap(true, false) {
		return nil
	}
	return b.SyncAutocomplete(ctx, provider)
}

// AwaitEditorInstalled counts the waits the mode made for the editor session_start installed; the real bridge's answer is tested in its own package.
func (b *refusingEditorBridge) AwaitEditorInstalled(context.Context) error {
	b.awaits.Add(1)
	return nil
}

// InstallRefusingEditor installs an extension editor whose runtime refuses every provider hand-off, on the owner loop, and returns the
// number of hand-offs it has been asked for.
func (h *TestHarness) InstallRefusingEditor(path, message string) func() int {
	bridge := newRefusingEditorBridge(path, message)
	bridge.refusing.Store(true)
	h.Do(func() {
		h.m.opts.SubprocessUIBridge = bridge
		h.m.setRemoteEditor(&fakeDelegatedEditor{})
	})
	return func() int { return int(bridge.syncs.Load()) }
}

// RemoteEditorInstalled reports whether an extension editor component is installed in place of the default editor.
func (h *TestHarness) RemoteEditorInstalled() bool {
	installed := false
	h.Do(func() { installed = h.m.remoteEditor != nil })
	return installed
}

// EditorAwaits is the number of times the bind (startup, /new, /reload) waited for the editor session_start installed.
func (h *TestHarness) EditorAwaits() int {
	awaits := 0
	h.Do(func() { awaits = int(h.m.opts.SubprocessUIBridge.(*refusingEditorBridge).awaits.Load()) })
	return awaits
}

func extensionErrorTexts(m *InteractiveMode) []string {
	m.extensionErrorsMu.Lock()
	defer m.extensionErrorsMu.Unlock()
	texts := make([]string, 0, len(m.pendingExtensionErrors))
	for _, pending := range m.pendingExtensionErrors {
		texts = append(texts, pending.ExtensionPath+": "+pending.Error)
	}
	return texts
}

func pumpRefusalUntil(t *testing.T, m *InteractiveMode, what string, done func() bool) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !done() {
		select {
		case fn := <-m.uiTaskCh:
			fn()
		case <-timer.C:
			t.Fatalf("timed out waiting for %s", what)
		case <-time.After(time.Millisecond):
		}
	}
}

func shownChat(m *InteractiveMode) string {
	lines := m.chatContainer.Render(100)
	for i := range lines {
		lines[i] = widthx.StripAnsi(lines[i])
	}
	return strings.Join(lines, "\n")
}

// Pi's setupAutocompleteProvider ends with `this.editor.setAutocompleteProvider?.(provider)` (interactive-mode.ts), and an extension's editor may throw
// there. Callers that have no throw path of their own to carry it (the rebuild after an extension's connection ended, the editor's own provider
// change, the install of an editor whose process was built without the provider that exists by then) show it as the extension's error through
// showExtensionError and go on: the default editor holds the rebuilt provider, the extension's editor keeps the one it had, and one failed sync is
// one report however many keys follow.
func TestRefusedProviderIsAnExtensionErrorWhereNoCallerCarriesIt(t *testing.T) {
	const path, message = "/ext/editor.ts", "EDITOR_REFUSED"
	t.Run("provider change", func(t *testing.T) {
		m := autocompleteModeProbe(t)
		bridge := newRefusingEditorBridge(path, message)
		m.opts.SubprocessUIBridge = bridge
		m.setRemoteEditor(&fakeDelegatedEditor{})
		bridge.refusing.Store(true)
		m.editor.SetAutocomplete(tui.NewCombinedProvider([]tui.SlashCommand{{Name: "keep"}}, t.TempDir(), ""))
		drainAutocompleteWork(t, m)
		if got := extensionErrorTexts(m); len(got) != 1 || got[0] != path+": "+message {
			t.Fatalf("extension errors = %q, want one from %s", got, path)
		}
		m.showPendingExtensionErrors()
		if chat := shownChat(m); !strings.Contains(chat, `Extension "`+path+`" error: `+message) {
			t.Fatalf("the transcript lacks Pi's extension error line:\n%s", chat)
		}
		for _, key := range "abc" {
			m.editor.HandleInput(string(key))
		}
		drainAutocompleteWork(t, m)
		if bridge.syncs.Load() != 1 || len(extensionErrorTexts(m)) != 0 {
			t.Fatalf("typing synced the editor again: %d syncs, errors %q", bridge.syncs.Load(), extensionErrorTexts(m))
		}
	})
	t.Run("factory pruned with its connection", func(t *testing.T) {
		m := autocompleteModeProbe(t)
		bridge := newRefusingEditorBridge(path, message)
		m.opts.SubprocessUIBridge = bridge
		m.setRemoteEditor(&fakeDelegatedEditor{})
		owner, cancel := context.WithCancel(t.Context())
		ui := &ExtUIContext{m: m}
		autocompleteOwnerCall(t, m, func() error {
			return ui.AddAutocompleteProviderWithLifetime(owner, func(_ context.Context, current *extension.AutocompleteProvider) (*extension.AutocompleteProvider, error) {
				return current, nil
			})
		})
		bridge.refusing.Store(true)
		cancel()
		pumpRefusalUntil(t, m, "the rebuild after the connection ended", func() bool { return len(extensionErrorTexts(m)) > 0 })
		if got := extensionErrorTexts(m); len(got) != 1 || got[0] != path+": "+message {
			t.Fatalf("extension errors = %q, want one from %s", got, path)
		}
		if len(m.autocompleteFactories) != 0 {
			t.Fatal("the rebuild kept the ended connection's factory")
		}
	})
	t.Run("added by the extension", func(t *testing.T) {
		m := autocompleteModeProbe(t)
		bridge := newRefusingEditorBridge(path, message)
		m.opts.SubprocessUIBridge = bridge
		m.setRemoteEditor(&fakeDelegatedEditor{})
		bridge.refusing.Store(true)
		ui := &ExtUIContext{m: m}
		var err error
		autocompleteOwnerCall(t, m, func() error {
			err = ui.AddAutocompleteProvider(func(_ context.Context, current *extension.AutocompleteProvider) (*extension.AutocompleteProvider, error) {
				return current, nil
			})
			return nil
		})
		if err == nil || err.Error() != message {
			t.Fatalf("ctx.ui.addAutocompleteProvider returned %v, want the editor's %q thrown to the extension's own caller", err, message)
		}
		if got := extensionErrorTexts(m); len(got) != 0 {
			t.Fatalf("the add reported %q as well as returning it", got)
		}
	})
	t.Run("handed at the install", func(t *testing.T) {
		m := autocompleteModeProbe(t)
		bridge := newRefusingEditorBridge(path, message)
		bridge.refusing.Store(true)
		bridge.pending.Store(true)
		m.opts.SubprocessUIBridge = bridge
		m.autocompleteSetUp = true
		m.setRemoteEditor(&fakeDelegatedEditor{})
		drainAutocompleteWork(t, m)
		if got := extensionErrorTexts(m); len(got) != 1 || got[0] != path+": "+message {
			t.Fatalf("extension errors = %q, want one from %s", got, path)
		}
		if bridge.syncs.Load() != 1 {
			t.Fatalf("the install synced the editor %d times, want the one hand-over", bridge.syncs.Load())
		}
		m.setRemoteEditor(&fakeDelegatedEditor{})
		drainAutocompleteWork(t, m)
		if bridge.syncs.Load() != 1 {
			t.Fatalf("a second install of an editor that a sync had reached synced it again: %d syncs", bridge.syncs.Load())
		}
	})
}

// Pi leaves the throw of a settings change uncaught (the selector's handler runs inside the terminal's input handler): the session ends.
func TestRefusedProviderAfterASettingsChangeEndsTheSession(t *testing.T) {
	m := autocompleteModeProbe(t)
	bridge := newRefusingEditorBridge("/ext/editor.ts", "EDITOR_REFUSED")
	m.opts.SubprocessUIBridge = bridge
	m.setRemoteEditor(&fakeDelegatedEditor{})
	bridge.refusing.Store(true)
	m.setAutocompleteUncaught(tui.NewCombinedProvider([]tui.SlashCommand{{Name: "keep"}}, t.TempDir(), ""))
	crashed := func() (value any) {
		defer func() { value = recover() }()
		pumpRefusalUntil(t, m, "the uncaught exception", func() bool { return false })
		return nil
	}()
	if _, ok := crashed.(*extension.EditorSyncError); !ok {
		t.Fatalf("the settings rebuild ended with %v, want the editor's error as an uncaught exception", crashed)
	}
}
