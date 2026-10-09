package coding

import (
	"reflect"
	"testing"
)

// session-manager.ts:245 ReadonlySessionManager and runner.ts:582 `get sessionManager()`: an extension context hands the extension the
// session's own log through the read-only Pick, so every getter answers from the live Session and a later append is visible to a context made before it.
func TestExtensionContextSessionManagerReadsTheSessionsOwnLog(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	log := h.session.inner
	ctx := h.session.currentRunner().CreateCommandContext()
	manager, err := ctx.SessionManager()
	if err != nil || manager == nil {
		t.Fatalf("SessionManager() = %v, %v, want the session's log", manager, err)
	}
	before := len(log.GetEntries())
	if _, err := log.AppendCustomEntry("probe", map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	if _, err := log.AppendSessionInfo("named"); err != nil {
		t.Fatal(err)
	}
	if got, want := manager.GetSessionId(), log.GetSessionId(); got == "" || got != want {
		t.Errorf("GetSessionId = %q, want %q", got, want)
	}
	if manager.GetCwd() != log.GetCwd() || manager.GetSessionDir() != log.GetSessionDir() || !reflect.DeepEqual(manager.GetSessionFile(), log.GetSessionFile()) {
		t.Errorf("cwd/dir/file = %q %q %v, want the log's", manager.GetCwd(), manager.GetSessionDir(), manager.GetSessionFile())
	}
	all := manager.GetEntries()
	if len(all) != before+2 || all[before].Base().Type != "custom" || all[before+1].Base().Type != "session_info" {
		t.Fatalf("GetEntries = %d entries, want %d ending with the custom and session_info entries appended after the context was made", len(all), before+2)
	}
	entries := all[before:]
	leaf, ok := manager.GetLeafEntry()
	if !ok || leaf.Base().ID != entries[1].Base().ID || manager.GetLeafID() == nil || *manager.GetLeafID() != entries[1].Base().ID {
		t.Errorf("leaf = %v %v, want %s", leaf, ok, entries[1].Base().ID)
	}
	if got, ok := manager.GetEntry(entries[0].Base().ID); !ok || got.Base().ID != entries[0].Base().ID {
		t.Errorf("GetEntry = %v, %v", got, ok)
	}
	if manager.GetSessionName() != "named" {
		t.Errorf("GetSessionName = %q, want named", manager.GetSessionName())
	}
	if branch := manager.GetBranch(); len(branch) != before+2 || branch[before].Base().ID != entries[0].Base().ID {
		t.Errorf("GetBranch = %v, want root to leaf", branch)
	}
	if tree := manager.GetTree(); len(tree) != 1 || tree[0].Entry.Base().ID != all[0].Base().ID {
		t.Errorf("GetTree = %v, want the one root %s", tree, all[0].Base().ID)
	}
	if header := manager.GetHeader(); header.ID != log.GetSessionId() {
		t.Errorf("GetHeader id = %q, want %q", header.ID, log.GetSessionId())
	}
	if projection := manager.BuildSessionProjection(); len(projection.Entries) != len(manager.BuildContextEntries()) || projection.ThinkingLevel != "off" {
		t.Errorf("BuildSessionProjection = %+v", projection)
	}
	if got, want := len(manager.BuildContextEntries()), len(log.BuildContextEntries()); got != want || got == 0 {
		t.Errorf("BuildContextEntries = %d entries, want the log's %d", got, want)
	}
}
