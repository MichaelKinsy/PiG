package codingagent

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// scanSessionName is getSessionName as session-manager.ts computes it: the entries walked backwards to the first session_info that parses.
func scanSessionName(s *Session) string {
	for _, entry := range slices.Backward(s.GetEntries()) {
		if entry.Base().Type != "session_info" {
			continue
		}
		if name, ok := sessionInfoName(entry.Raw()); ok {
			return name
		}
	}
	return ""
}

// GetSessionName is kept as entries are appended and loaded instead of scanning the log, because every extension event's state carries it. It must equal the backward scan after every append, after a reload of the file, and in a branched session.
func TestGetSessionNameEqualsTheBackwardScan(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("name-scan", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(mkUserMsg("hello")); err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewPCG(7, 11))
	names := []string{"alpha", "  beta  ", "", "gamma\nline", "   "}
	for step := range 200 {
		switch random.IntN(4) {
		case 0:
			if _, err := sess.AppendSessionInfo(names[random.IntN(len(names))]); err != nil {
				t.Fatal(err)
			}
		case 1:
			// A session_info whose name is not a string does not parse, and the scan skips it.
			id, err := generateEntryID()
			if err != nil {
				t.Fatal(err)
			}
			if err := sess.AppendEntry(map[string]any{"type": "session_info", "id": id, "parentId": sess.GetLeafID(), "timestamp": "2026-05-02T00:00:00Z", "name": step}); err != nil {
				t.Fatal(err)
			}
		case 2:
			if _, err := sess.AppendMessage(mkAssistantMsg("answer")); err != nil {
				t.Fatal(err)
			}
		case 3:
			entries := sess.GetEntries()
			if err := sess.Branch(entries[random.IntN(len(entries))].Base().ID); err != nil {
				t.Fatal(err)
			}
		}
		if got, want := sess.GetSessionName(), scanSessionName(sess); got != want {
			t.Fatalf("step %d: GetSessionName = %q, the backward scan = %q", step, got, want)
		}
	}
	// End on a named entry followed by one that does not parse, so the reload has a name to restore.
	if _, err := sess.AppendSessionInfo("final"); err != nil {
		t.Fatal(err)
	}
	id, err := generateEntryID()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(map[string]any{"type": "session_info", "id": id, "parentId": sess.GetLeafID(), "timestamp": "2026-05-02T00:00:00Z", "name": 1}); err != nil {
		t.Fatal(err)
	}
	if got := sess.GetSessionName(); got != "final" {
		t.Fatalf("GetSessionName = %q, want final", got)
	}
	loaded, err := sm.Load(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := loaded.GetSessionName(), scanSessionName(loaded); got != want || got != sess.GetSessionName() {
		t.Fatalf("reloaded GetSessionName = %q, scan %q, before the reload %q", got, want, sess.GetSessionName())
	}
	// A branch from the first message holds no session_info, so the branched session has no name.
	if _, err := sess.CreateBranchedSession(sess.GetEntries()[0].Base().ID); err != nil {
		t.Fatal(err)
	}
	if got, want := sess.GetSessionName(), scanSessionName(sess); got != want || got != "" {
		t.Fatalf("branched session GetSessionName = %q, scan %q", got, want)
	}
}
