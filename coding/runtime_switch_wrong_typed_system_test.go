package coding

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's switchSession builds the destination's Session after it tears the current one down, and the AgentSession constructor restores the loadout the transcript declares (agent-session.ts:526, _restoreToolsFromTranscript). That reads the toolsAdded and toolsRemoved of every system message on the branch, which throws a TypeError for a member the replay cannot read, so the switch fails with the TypeError and the runtime keeps the outgoing Session object. A content it cannot read does not fail the restore.
func TestSwitchSessionRejectsAWrongTypedSystemReplayLikePi(t *testing.T) {
	cases := []struct {
		name, system, want string
	}{
		{"numeric toolsAdded", `{"role":"system","content":"","toolsAdded":5,"timestamp":25}`, "(message.toolsAdded ?? []) is not iterable"},
		{"object toolsRemoved", `{"role":"system","content":"","toolsRemoved":{"a":1},"timestamp":25}`, "(message.toolsRemoved ?? []) is not iterable"},
		{"null tool", `{"role":"system","content":"","toolsAdded":[null],"timestamp":25}`, "Cannot read properties of null (reading 'name')"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRuntimeTestHarness(t, runtimeTestOptions{})
			original := h.runtime.Session()
			log := `{"type":"session","version":3,"id":"wrong-typed","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + strconv.Quote(h.runtime.CWD()) + `}` + "\n" +
				`{"type":"message","id":"m-1","parentId":null,"timestamp":"2026-01-01T00:01:00.000Z","message":` + tc.system + `}` + "\n" +
				`{"type":"message","id":"m-2","parentId":"m-1","timestamp":"2026-01-01T00:01:01.000Z","message":{"role":"user","content":"earlier","timestamp":26}}` + "\n"
			path := filepath.Join(t.TempDir(), "wrong-typed.jsonl")
			if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := h.runtime.SwitchSession(t.Context(), path)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("switch = %+v, %v, want error %q", result, err, tc.want)
			}
			if h.runtime.Session() != original {
				t.Fatal("a failed switch replaced the runtime's Session")
			}
			// Pi's dispose leaves the retained Session able to run a prompt (rpc scenario 59).
			runtimePrompt(t, h.runtime, "after the failed switch")
		})
	}
}

// A replacement with the outgoing Session's ID (a switch to the current file) owns the ID's provider resources. Pi disposes the outgoing Session before it builds the replacement, so the outgoing Session's later Close must not release them again.
func TestSwitchToTheCurrentFileReleasesTheSessionResourcesOnce(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	runtimePrompt(t, h.runtime, "hello")
	released := map[string]int{}
	unregister := ai.RegisterSessionResourceCleanup(func(id string) { released[id]++ })
	defer unregister()
	id := h.runtime.Session().ID()
	if _, err := h.runtime.SwitchSession(t.Context(), h.runtime.Session().Path()); err != nil {
		t.Fatal(err)
	}
	if got := h.runtime.Session().ID(); got != id {
		t.Fatalf("switch to the current file changed the session ID: %q -> %q", id, got)
	}
	if released[id] != 1 {
		t.Fatalf("session resources released %d times, want once (the outgoing dispose)", released[id])
	}
}
