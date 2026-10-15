package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// Pi: types.ts:335 (ExtensionContext.sessionManager: ReadonlySessionManager) and session-manager.ts:245 (the Pick of 15 getters).
// A handler's ctx.sessionManager is the live session through the typed read-only interface: it answers the session's own identity,
// location and entries, not a copy taken at bind time.
func TestContextSessionManagerIsTheLiveSessionThroughTheReadOnlyInterface(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	runner := h.session.currentRunner()
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{SessionManager: h.session.inner}, nil)
	manager, err := runner.CreateCommandContext().SessionManager()
	if err != nil {
		t.Fatal(err)
	}
	if manager.GetSessionId() != h.session.inner.GetSessionId() || manager.GetCwd() != h.session.inner.GetCwd() || manager.GetSessionDir() != h.session.inner.GetSessionDir() {
		t.Fatalf("identity = %q %q %q, want the session's own", manager.GetSessionId(), manager.GetCwd(), manager.GetSessionDir())
	}
	before := len(manager.GetEntries())
	if err := h.session.inner.AppendEntry(map[string]any{"type": "custom", "id": "c1", "parentId": nil, "timestamp": "2026-01-01T00:00:00Z", "customType": "probe", "data": 1}); err != nil {
		t.Fatal(err)
	}
	entries := manager.GetEntries()
	if len(entries) != before+1 || entries[len(entries)-1].Base().ID != "c1" {
		t.Fatalf("entries after an append = %d (was %d), want the appended entry visible through the interface", len(entries), before)
	}
	if leaf := manager.GetLeafID(); leaf == nil || *leaf != "c1" {
		t.Fatalf("leaf = %v, want c1", leaf)
	}
	if got := manager.GetBranch(); !slices.ContainsFunc(got, func(e sessionentry.SessionEntry) bool { return e.Base().ID == "c1" }) {
		t.Fatalf("branch = %d entries, want it to include c1", len(got))
	}
}
