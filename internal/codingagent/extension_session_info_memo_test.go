package codingagent

import (
	"bytes"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The session info an extension holds follows every value it is built from (cwd, session directory, persistence, header): the memo answers an unchanged read from its encoding and rebuilds when any one changes, and each answer equals a direct encoding.
func TestExtensionSessionInfoMemoFollowsEveryInput(t *testing.T) {
	session := NewSession("memo", "/w")
	view := ExtensionSessionView{Session: session, CWD: "/w", SessionDir: "/sessions"}
	var memo ExtensionSessionInfoMemo
	read := func(step string) []byte {
		t.Helper()
		got, err := memo.Encoded(view)
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(ExtensionSessionInfo(view))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s:\n got %s\nwant %s", step, got, want)
		}
		return got
	}
	first := read("first read")
	if second := read("unchanged"); !bytes.Equal(first, second) {
		t.Fatalf("an unchanged read differs: %s vs %s", first, second)
	}
	changed := func(step string, previous []byte) []byte {
		t.Helper()
		got := read(step)
		if bytes.Equal(got, previous) {
			t.Fatalf("%s did not change the info: %s", step, got)
		}
		return got
	}
	// persistence
	session.SetPath("/sessions/memo.jsonl")
	withPath := changed("a session file path", first)
	// the session directory
	view.SessionDir = "/other-sessions"
	withDir := changed("a session directory", withPath)
	// the header, in place (a restored or branched session replaces it)
	session.header.ParentSession = "/sessions/parent.jsonl"
	withParent := changed("a parent session", withDir)
	session.header.ID = "memo-2"
	withID := changed("a session id", withParent)
	// the cwd
	view.CWD = "/elsewhere"
	withCWD := changed("a cwd", withID)
	// no session at all
	view.Session = nil
	changed("no session", withCWD)
}
