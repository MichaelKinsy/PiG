package codingagent_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's handleReloadCommand runs setupAutocompleteProvider inside the try of the reload (interactive-mode.ts), so an installed editor that refuses the rebuilt
// provider ends the reload as "Reload failed: <message>"; the session goes on with the editor it had.
func TestReloadWhoseInstalledEditorRefusesTheProviderIsReloadFailed(t *testing.T) {
	// The reload resets the extension UI, and the extension installs its editor again from its session_start, as the Pi extensions that do so.
	var h *icodingagent.TestHarness
	var syncs func() int
	h = reloadCallerPair(t, t.TempDir(), t.TempDir(), "", func() { syncs = h.InstallRefusingEditor("/ext/editor.ts", "EDITOR_REFUSED") })
	if err := h.ReloadFromExtension(t.Context()); err != nil {
		t.Fatalf("the reload reported its failure through the session's error line, not the caller: %v", err)
	}
	if syncs() != 1 {
		t.Fatalf("the editor was asked for the provider %d times in one reload, want 1:\n%s", syncs(), h.Chat())
	}
	if got := h.EditorAwaits(); got != 1 {
		t.Fatalf("the reload waited %d times for the editor session_start installed, want 1", got)
	}
	// Pi's catch focuses the editor the reload replaced (dismissReloadBox(previousEditor)), not the one session_start installed.
	if h.RemoteEditorInstalled() {
		t.Fatal("a failed reload must hand the input back to the previous (default) editor, as Pi does, not keep the editor the reload installed")
	}
	chat := h.Chat()
	if !strings.Contains(chat, "Error: Reload failed: EDITOR_REFUSED") || strings.Contains(chat, "Reloaded") {
		t.Fatalf("a reload whose editor refused the provider must show Pi's `Error: Reload failed: EDITOR_REFUSED` and no success:\n%s", chat)
	}
}

// Pi's bindCurrentSessionExtensions runs setupAutocompleteProvider after the bind, outside any handler: the throw fails the bind, which Pi's runtime turns into
// the fatal error of /new, /resume and /fork, and into the crash of the startup.
func TestRebindWhoseInstalledEditorRefusesTheProviderFailsTheBind(t *testing.T) {
	f := newRebindFixture(t, func(_ *coding.Session, _ ...any) (any, error) { return nil, nil }, nil)
	f.h.InstallRefusingEditor("/ext/editor.ts", "EDITOR_REFUSED")
	session := f.runtime.Session()
	err := f.h.RebindSession(t.Context(), session, f.runners[session], f.events[session], true)
	var refused *extension.EditorSyncError
	if !errors.As(err, &refused) || refused.Error() != "EDITOR_REFUSED" {
		t.Fatalf("the bind returned %v, want the editor's EDITOR_REFUSED", err)
	}
	if got := f.h.EditorAwaits(); got != 1 {
		t.Fatalf("the bind waited %d times for the editor session_start installed, want 1", got)
	}
}
