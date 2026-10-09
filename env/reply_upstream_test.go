package env

import (
	"os"
	"path/filepath"
	"testing"
)

// connection.ts Reply: a request resolves with the reply's JSON, its payload bytes, and the id of the daemon session that answered; a request pinned to that session id reaches the same live session, and a request pinned to another id is a lost connection.
// Pi: packages/env/src/connection.ts:66 (payload)
// packages/env/src/connection.ts:64-68: a Reply carries the JSON, the payload bytes and the daemon session that answered; RequestOptions.session (connection.ts:82-86) pins the session.
func TestRequestReplyCarriesJSONPayloadAndSession(t *testing.T) {
	file := filepath.Join(t.TempDir(), "data.txt")
	if err := os.WriteFile(file, []byte("PAYLOAD"), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	t.Cleanup(connection.Close)

	opened, err := connection.Request(background, "open", Json{"path": file, "mode": "read"}, RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if opened.Session == 0 || opened.JSON["handle"] == nil {
		t.Fatalf("open reply = %+v, want a handle and a session id", opened)
	}
	read, err := connection.Request(background, "pread", Json{"handle": opened.JSON["handle"], "offset": 0, "length": 100}, RequestOptions{Session: opened.Session})
	if err != nil {
		t.Fatal(err)
	}
	if string(read.Payload) != "PAYLOAD" || read.Session != opened.Session {
		t.Fatalf("pread reply = %+v, want payload PAYLOAD from session %d", read, opened.Session)
	}
	if _, err := connection.Request(background, "pread", Json{"handle": opened.JSON["handle"], "offset": 0, "length": 1}, RequestOptions{Session: opened.Session + 1}); !IsConnectionLost(err) {
		t.Fatalf("a request pinned to another session = %v, want a lost-connection error", err)
	}
}
