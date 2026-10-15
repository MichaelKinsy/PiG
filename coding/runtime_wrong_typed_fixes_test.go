package coding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Pi's in-memory fork applies the replacement before it disposes the outgoing session, so a failing close still leaves the replacement hook to run and surfaces the close error.
func TestInMemoryForkRunsTheReplacementHookWhenTheSupersededCloseFails(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{memory: true})
	runtimePrompt(t, h.runtime, "hello")
	userID := h.runtime.Session().UserMessagesForForking()[0].EntryID
	closeErr := errors.New("close failed")
	outgoing := h.runtime.Session()
	outgoing.closeSupersededErr = func() error { return closeErr }
	hooks := 0
	h.runtime.SetRebindSession(func(context.Context, *Session) error { hooks++; return nil })
	_, err := h.runtime.Fork(t.Context(), userID, nil)
	if !errors.Is(err, closeErr) {
		t.Fatalf("fork error = %v, want the close error", err)
	}
	if hooks != 1 {
		t.Fatalf("replacement hook ran %d times, want once", hooks)
	}
	if h.runtime.Session() == outgoing {
		t.Fatal("fork did not apply the replacement")
	}
}

// agent-session.ts:4168 throws the replay's TypeError after navigateTree moved the leaf.
func TestNavigateTreeOntoAWrongTypedSystemBranchMovesTheLeafThenFails(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	log := `{"type":"session","version":3,"id":"nav-wrong","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + strconv.Quote(h.runtime.CWD()) + `}` + "\n" +
		`{"type":"message","id":"m-1","parentId":null,"timestamp":"2026-01-01T00:01:00.000Z","message":{"role":"user","content":"root","timestamp":1}}` + "\n" +
		`{"type":"message","id":"m-2","parentId":"m-1","timestamp":"2026-01-01T00:01:01.000Z","message":{"role":"system","content":5,"timestamp":2}}` + "\n" +
		`{"type":"message","id":"m-3","parentId":"m-2","timestamp":"2026-01-01T00:01:02.000Z","message":{"role":"user","content":"bad branch","timestamp":3}}` + "\n" +
		`{"type":"message","id":"m-4","parentId":"m-1","timestamp":"2026-01-01T00:01:03.000Z","message":{"role":"user","content":"good branch","timestamp":4}}` + "\n"
	path := filepath.Join(t.TempDir(), "nav-wrong.jsonl")
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runtime.SwitchSession(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	session := h.runtime.Session()
	_, err := session.NavigateTree(t.Context(), "m-3", NavigateTreeOptions{})
	if err == nil || err.Error() != "content.filter is not a function" {
		t.Fatalf("navigate error = %v, want content.filter is not a function", err)
	}
	leaf := session.inner.GetLeafID()
	if leaf == nil || *leaf != "m-2" {
		t.Fatalf("leaf after the failed navigation = %v, want m-2 (moved before the replay throws)", leaf)
	}
}
