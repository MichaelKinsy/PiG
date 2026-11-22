package codingagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Ports the cases Pi a635f3901 (#12111, issue #12106) adds: after a delete or a rename the /resume picker reloads its list
// progressively, and the first partial list holds only the session that finished loading first. A touched selection missing
// from a partial list stays the selection target until it arrives, instead of becoming whichever session its index points to.

// settleSelectorLoads runs the selector's queued load updates until the visible scope's load has settled, as flushPromises
// does for Pi's selector.
func settleSelectorLoads(t *testing.T, s *SessionSelectorComponent) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); s.scopeLoad(s.scope) != nil && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		s.drainLoadUpdates()
	}
	if s.scopeLoad(s.scope) != nil {
		t.Fatal("the session load did not settle")
	}
}

// progressiveReloadSessions is the tests' [1, 2, 3, 4, 5].map(makeSession): s1 is the most recently modified.
func progressiveReloadSessions(path func(id string) string) []SessionInfo {
	sessions := make([]SessionInfo, 0, 5)
	for i := 1; i <= 5; i++ {
		session := pathDeleteSession(fmt.Sprintf("s%d", i))
		session.Modified = time.UnixMilli(int64(10_000 - i*1000))
		if path != nil {
			session.Path = path(session.ID)
		}
		sessions = append(sessions, session)
	}
	return sessions
}

func noSessions(context.Context, SessionListProgress) ([]SessionInfo, error) {
	return []SessionInfo{}, nil
}

// session-selector-path-delete.test.ts:359 "keeps the next session selected when a progressive reload after delete starts with
// another session": real files and a real deletion; the loader lists the files that still exist.
func TestUpstreamSessionSelectorKeepsTheNextSessionSelectedWhenAProgressiveReloadAfterDeleteStartsWithAnotherSession(t *testing.T) {
	dir := t.TempDir()
	sessions := progressiveReloadSessions(func(id string) string { return filepath.Join(dir, id+".jsonl") })
	for _, session := range sessions {
		if err := os.WriteFile(session.Path, []byte("session\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	loader := func(_ context.Context, onProgress SessionListProgress) ([]SessionInfo, error) {
		var remaining []SessionInfo
		for _, session := range sessions {
			if _, err := os.Stat(session.Path); err == nil {
				remaining = append(remaining, session)
			}
		}
		// The first partial list holds only the file that finished loading first, here the oldest
		onProgress(1, len(remaining), []SessionInfo{remaining[len(remaining)-1]})
		onProgress(len(remaining), len(remaining), remaining)
		return remaining, nil
	}
	selector := NewSessionSelectorComponent(loader, noSessions, func(string) {}, func() {}, func() {}, func() {},
		&SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t), DeleteSession: unlinkDeleter(os.Remove)})
	defer selector.close()
	settleSelectorLoads(t, selector)

	list := selector.GetSessionList()
	list.HandleInput("\x1b[B")
	list.HandleInput("\x1b[A")
	if got := list.GetSelectedSessionPath(); got != sessions[0].Path {
		t.Fatalf("selected %q before the delete, want %q", got, sessions[0].Path)
	}

	list.HandleInput("\x04")
	list.HandleInput("\r")
	settleSelectorLoads(t, selector)

	if _, err := os.Stat(sessions[0].Path); !os.IsNotExist(err) {
		t.Fatalf("the deleted session's file is still there: %v", err)
	}
	if got := list.GetSelectedSessionPath(); got != sessions[1].Path {
		t.Fatalf("selected %q after the reload, want the next session %q", got, sessions[1].Path)
	}
}

// session-selector-rename.test.ts:112 "keeps the renamed session selected when the reload starts with another session".
func TestUpstreamSessionSelectorKeepsTheRenamedSessionSelectedWhenTheReloadStartsWithAnotherSession(t *testing.T) {
	sessions := progressiveReloadSessions(nil)
	var loads atomic.Int32
	loader := func(_ context.Context, onProgress SessionListProgress) ([]SessionInfo, error) {
		// On reload, the first partial list holds only the file that finished loading first
		if loads.Add(1) > 1 {
			onProgress(1, len(sessions), []SessionInfo{sessions[4]})
		}
		return sessions, nil
	}
	selector := NewSessionSelectorComponent(loader, noSessions, func(string) {}, func() {}, func() {}, func() {},
		&SessionSelectorOptions{RenameSession: func(string, string) error { return nil }, ShowRenameHint: new(true), Keybindings: sessionSelectorInputBindings(t)})
	defer selector.close()
	settleSelectorLoads(t, selector)

	list := selector.GetSessionList()
	list.HandleInput("\x1b[B")
	list.HandleInput("\x1b[B")
	if got := list.GetSelectedSessionPath(); got != sessions[2].Path {
		t.Fatalf("selected %q before the rename, want %q", got, sessions[2].Path)
	}

	list.HandleInput("\x1b[114;5u")
	selector.HandleInput("X")
	selector.HandleInput("\r")
	settleSelectorLoads(t, selector)

	if got := loads.Load(); got != 2 {
		t.Fatalf("the sessions were loaded %d times, want 2", got)
	}
	if got := selector.GetSessionList().GetSelectedSessionPath(); got != sessions[2].Path {
		t.Fatalf("selected %q after the reload, want the renamed session %q", got, sessions[2].Path)
	}
}

// The interactive /resume picker lists through SessionManager.ListCurrentSessions (interactive_commands.go PickSession), whose
// progress publishes the file that finished loading first, as Pi's listing does. Here the next session's file is the slowest to
// read, so the reload after the delete starts with another session; the picker must still end on the next session.
func TestResumePickerOverTheSessionListingKeepsTheNextSessionSelectedAcrossTheReloadAfterADelete(t *testing.T) {
	// No trash command: the session file is unlinked.
	t.Setenv("PATH", t.TempDir())
	dir, cwd := t.TempDir(), t.TempDir()
	name := func(i int) string { return fmt.Sprintf("s%d.jsonl", i) }
	for i := 1; i <= 5; i++ {
		modified := time.Date(2026, 8, 3, 0, 0, 10-i, 0, time.UTC)
		content := fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":\"s%d\",\"timestamp\":%q,\"cwd\":%q}\n", i, modified.Format("2006-01-02T15:04:05.000Z"), cwd)
		if i == 2 {
			content += strings.Repeat("{\"type\":\"custom\",\"customType\":\"pad\",\"data\":\""+strings.Repeat("x", 1000)+"\"}\n", 16000)
		}
		path := filepath.Join(dir, name(i))
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	sm := NewSessionManagerWithDir(cwd, dir)
	var firstSnapshots [][]string
	selector := NewSessionSelectorComponent(
		func(ctx context.Context, onProgress SessionListProgress) ([]SessionInfo, error) {
			first := true
			return sm.ListCurrentSessions(SessionListOptions{Context: ctx, OnProgress: func(loaded, total int, partial []SessionInfo) {
				if first && partial != nil {
					first = false
					var names []string
					for _, session := range partial {
						names = append(names, filepath.Base(session.Path))
					}
					firstSnapshots = append(firstSnapshots, names)
				}
				onProgress(loaded, total, partial)
			}})
		},
		func(ctx context.Context, onProgress SessionListProgress) ([]SessionInfo, error) {
			return sm.ListAllSessions(SessionListOptions{Context: ctx, OnProgress: onProgress})
		},
		nil, nil, nil, nil,
		&SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t), DeleteSession: sm.deleteListedSession},
	)
	defer selector.close()
	settleSelectorLoads(t, selector)

	list := selector.GetSessionList()
	list.HandleInput("\x1b[B")
	list.HandleInput("\x1b[A")
	if got := filepath.Base(list.GetSelectedSessionPath()); got != name(1) {
		t.Fatalf("selected %q before the delete, want %q", got, name(1))
	}

	list.HandleInput("\x04")
	list.HandleInput("\r")
	settleSelectorLoads(t, selector)

	if _, err := os.Stat(filepath.Join(dir, name(1))); !os.IsNotExist(err) {
		t.Fatalf("the deleted session's file is still there: %v", err)
	}
	// The rule holds whichever file loads first; the log shows that the reload did start with another session.
	t.Logf("first snapshot of each load: %q", firstSnapshots)
	if got := filepath.Base(list.GetSelectedSessionPath()); got != name(2) {
		t.Fatalf("selected %q after the reload, want the next session %q", got, name(2))
	}
}

// session-selector.ts:618 handleInput: a key that reaches the list's selection or search handling gives up the awaited session.
// The reload after a delete first holds only s5 while s4, the next session, is awaited; the user then moves the selection or
// types a search, and the complete list must leave the selection on the row the user is on instead of jumping to s4.
func TestSessionSelectorMovingOrSearchingDuringAProgressiveReloadGivesUpTheAwaitedSession(t *testing.T) {
	// Pi 37b74c546 (#12246): a search selects the top row, the best match, and clears the awaited session, so the complete list
	// leaves the selection on its first row; moving the selection keeps the row the user is on.
	for _, tc := range []struct {
		name, key string
		want      int
	}{
		{"moving the selection", "\x1b[A", 4},
		{"typing a search", "h", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := progressiveReloadSessions(nil)
			remaining := []SessionInfo{sessions[0], sessions[1], sessions[3], sessions[4]}
			reload := make(chan sessionLoadResult, 1)
			loads := 0
			loader := func(_ *sessionLoad, onProgress SessionListProgress) <-chan sessionLoadResult {
				if loads++; loads == 1 {
					loaded := make(chan sessionLoadResult, 1)
					loaded <- sessionLoadResult{sessions: sessions}
					return loaded
				}
				onProgress(1, len(remaining), []SessionInfo{sessions[4]})
				return reload
			}
			s := newSessionSelectorWithLoaders(loader, resolvedSessionLoader(func() ([]SessionInfo, error) { return nil, nil }), nil, unlinkDeleter(func(string) error { return nil }), "", sessionSelectorInputBindings(t))
			t.Cleanup(s.close)
			s.drainLoadUpdates()
			list := s.GetSessionList()
			list.HandleInput("\x1b[B")
			list.HandleInput("\x1b[B")
			list.HandleInput("\x04")
			list.HandleInput("\r")
			if got := list.GetSelectedSessionPath(); got != sessions[4].Path || s.pendingSelectedPath != sessions[3].Path {
				t.Fatalf("after the first partial list: selected %q awaiting %q, want %q awaiting %q", got, s.pendingSelectedPath, sessions[4].Path, sessions[3].Path)
			}

			list.HandleInput(tc.key)
			reload <- sessionLoadResult{sessions: remaining}
			s.drainLoadUpdates()

			if got := list.GetSelectedSessionPath(); got != sessions[tc.want].Path {
				t.Fatalf("selected %q after the complete list, want %q", got, sessions[tc.want].Path)
			}
		})
	}
}

// session-selector.ts:1048 toggleScope passes partial while the scope it switches to is still loading or has not been loaded:
// the list is empty until that scope's sessions arrive, and the touched selection is selected again when they do.
func TestSessionSelectorKeepsTheSelectedSessionWhenTheScopeToggledToHasNotLoadedYet(t *testing.T) {
	sessions := progressiveReloadSessions(nil)
	newest := pathDeleteSession("other-folder")
	newest.Modified = time.UnixMilli(20_000)
	all := append([]SessionInfo{newest}, sessions...)
	s := newLoadedSessionSelector(func() ([]SessionInfo, error) { return sessions, nil }, func() ([]SessionInfo, error) { return all, nil }, nil, nil, "", sessionSelectorInputBindings(t))
	t.Cleanup(s.close)
	list := s.GetSessionList()
	list.HandleInput("\x1b[B")
	list.HandleInput("\x1b[B")
	if got := list.GetSelectedSessionPath(); got != sessions[2].Path {
		t.Fatalf("selected %q before the toggle, want %q", got, sessions[2].Path)
	}

	list.HandleInput("\t")
	s.drainLoadUpdates()

	if s.scope != sessionScopeAll || len(s.filtered) != len(all) {
		t.Fatalf("scope %v with %d rows, want the All scope with %d", s.scope, len(s.filtered), len(all))
	}
	if got := list.GetSelectedSessionPath(); got != sessions[2].Path {
		t.Fatalf("selected %q in the All scope, want the session selected before the toggle, %q", got, sessions[2].Path)
	}
}
