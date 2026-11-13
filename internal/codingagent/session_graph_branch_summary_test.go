package codingagent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// An extension's SessionManager.branchWithSummary(branchFromId, summary) leaves fromHook undefined, and Pi's entry then has no fromHook member; a caller that
// passes a boolean gets it written (session-manager.ts:1611-1630 stores `fromHook` as given).
func TestGraphBranchWithSummaryWithoutFromHookWritesNoMember(t *testing.T) {
	dir := t.TempDir()
	session, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range nullMemberLines[:2] {
		if err := session.AppendEntry(json.RawMessage(line)); err != nil {
			t.Fatal(err)
		}
	}
	manager := &GraphSessionManager{session}
	for _, tc := range []struct{ args, want string }{
		{`["00000002","s"]`, `"fromId":"00000002","summary":"s"}`},
		{`["00000002","s",null,false]`, `"summary":"s","fromHook":false}`},
		{`["00000002","s",null,true]`, `"summary":"s","fromHook":true}`},
	} {
		id, err := manager.Call(t.Context(), "branchWithSummary", json.RawMessage(tc.args))
		if err != nil {
			t.Fatalf("branchWithSummary%s: %v", tc.args, err)
		}
		entry, ok := session.GetEntry(id.(string))
		if !ok {
			t.Fatalf("branchWithSummary%s: no entry %v", tc.args, id)
		}
		if raw := string(entry.Raw()); !strings.HasSuffix(raw, tc.want) {
			t.Errorf("branchWithSummary%s wrote %s, want it to end in %s", tc.args, raw, tc.want)
		}
		if err := session.Branch("00000002"); err != nil {
			t.Fatal(err)
		}
	}
}
