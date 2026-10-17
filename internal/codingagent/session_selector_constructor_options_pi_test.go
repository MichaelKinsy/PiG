package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func settleSelector(t *testing.T, s *SessionSelectorComponent, rows int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); len(s.filtered) < rows && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		s.drainLoadUpdates()
	}
	if len(s.filtered) < rows {
		t.Fatalf("the selector shows %d rows, want %d", len(s.filtered), rows)
	}
}

// session-selector.ts:757-770 constructor: the current-scope loader receives the load's abort signal and a progress callback that publishes partial
// snapshots; closing the selector aborts the signal.
func TestNewSessionSelectorComponentLoaderReceivesTheSignalAndProgressCallback(t *testing.T) {
	var signal context.Context
	release := make(chan struct{})
	loader := func(ctx context.Context, progress SessionListProgress) ([]SessionInfo, error) {
		signal = ctx
		if progress == nil {
			t.Error("the loader got no progress callback")
			return nil, nil
		}
		progress(1, 2, []SessionInfo{scopeSession("partial")})
		<-release
		return []SessionInfo{scopeSession("partial"), scopeSession("final")}, nil
	}
	selector := NewSessionSelectorComponent(loader, loader, nil, nil, nil, nil, &SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t)})
	settleSelector(t, selector, 1)
	if got := selector.filtered[0].Session.ID; got != "partial" {
		t.Fatalf("progress snapshot row = %q, want partial", got)
	}
	if signal == nil || signal.Err() != nil {
		t.Fatalf("the loader's signal = %v, want a live context", signal)
	}
	close(release)
	selector.close()
	if signal.Err() == nil {
		t.Fatal("closing the selector must abort the loader's signal")
	}
}

// session-selector.ts:757-770 options: options.keybindings is the selector's keybinding manager; renameSession enables the rename key and the hint;
// currentSessionFilePath marks the session that refuses deletion; a missing deleteSession falls back to the file deletion of deleteSessionFile.
func TestNewSessionSelectorComponentOptionsAndCurrentSessionPath(t *testing.T) {
	loader := func(context.Context, SessionListProgress) ([]SessionInfo, error) {
		return []SessionInfo{scopeSession("one")}, nil
	}
	one := scopeSession("one").Path

	custom := NewKeybindingsManagerFromBindings(map[string][]KeyID{"app.session.toggleSort": {"ctrl+x"}}, "")
	sortSelector := NewSessionSelectorComponent(loader, loader, nil, nil, nil, nil, &SessionSelectorOptions{Keybindings: custom})
	defer sortSelector.close()
	settleSelector(t, sortSelector, 1)
	before := sortSelector.sortMode
	sortSelector.HandleInput("\x18")
	if sortSelector.sortMode == before {
		t.Fatal("options.keybindings must drive the selector: ctrl+x did not toggle the sort")
	}

	renamed := NewSessionSelectorComponent(loader, loader, nil, nil, nil, nil, &SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t), RenameSession: func(string, string) error { return nil }})
	defer renamed.close()
	settleSelector(t, renamed, 1)
	renamed.HandleInput("\x12")
	if !renamed.renameMode {
		t.Fatal("with options.renameSession the rename key must open the rename input")
	}
	plain := NewSessionSelectorComponent(loader, loader, nil, nil, nil, nil, &SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t)})
	defer plain.close()
	settleSelector(t, plain, 1)
	plain.HandleInput("\x12")
	if plain.renameMode {
		t.Fatal("without options.renameSession the rename key must do nothing")
	}

	current := NewSessionSelectorComponent(loader, loader, nil, nil, nil, nil, &SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t)}, one)
	defer current.close()
	settleSelector(t, current, 1)
	current.HandleInput("\x04")
	if current.confirmDelete != "" || current.statusState.message != "Cannot delete the currently active session" {
		t.Fatalf("deleting the current session: confirm %q status %q", current.confirmDelete, current.statusState.message)
	}
	other := NewSessionSelectorComponent(loader, loader, nil, nil, nil, nil, &SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t)})
	defer other.close()
	settleSelector(t, other, 1)
	other.HandleInput("\x04")
	if other.confirmDelete != one {
		t.Fatalf("without a current session path the delete key must ask to confirm %q, got %q", one, other.confirmDelete)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "share"))
	file := filepath.Join(t.TempDir(), "doomed.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doomed := SessionInfo{Path: file, ID: "doomed", Created: time.UnixMilli(0), Modified: time.UnixMilli(0), MessageCount: 1, FirstMessage: "x", AllMessagesText: "x"}
	deleting := NewSessionSelectorComponent(func(context.Context, SessionListProgress) ([]SessionInfo, error) { return []SessionInfo{doomed}, nil }, loader, nil, nil, nil, nil, &SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t)})
	defer deleting.close()
	settleSelector(t, deleting, 1)
	deleting.HandleInput("\x04")
	deleting.HandleInput("\r")
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the default deleteSession must remove the session file, stat error = %v", err)
	}
}
