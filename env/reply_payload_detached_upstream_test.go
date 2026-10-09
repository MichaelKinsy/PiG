package env

import (
	"os"
	"path/filepath"
	"testing"
)

// A Reply's payload is "a plain Uint8Array copy ... not a view of the receive buffer" (packages/env/src/connection.ts:362-363, Reply.payload
// :66): a payload kept from one reply is unchanged by later replies and by edits to an earlier one, and a reply with no payload bytes carries
// an empty payload with the JSON and session still set (connection.ts:381).
// mutation-checked: reusing one frame buffer for every reply, dropping the payload or dropping the session id of a reply fails it
func TestReplyPayloadIsACopyOwnedByTheCaller(t *testing.T) {
	file := filepath.Join(t.TempDir(), "data.txt")
	if err := os.WriteFile(file, []byte("AAAABBBB"), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	t.Cleanup(connection.Close)
	opened, err := connection.Request(background, "open", Json{"path": file, "mode": "read"}, RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pread := func(offset, length int) Reply {
		t.Helper()
		reply, err := connection.Request(background, "pread", Json{"handle": opened.JSON["handle"], "offset": offset, "length": length}, RequestOptions{Session: opened.Session})
		if err != nil {
			t.Fatal(err)
		}
		return reply
	}
	first := pread(0, 4)
	second := pread(4, 4)
	if string(first.Payload) != "AAAA" || string(second.Payload) != "BBBB" {
		t.Fatalf("payloads = %q, %q; want AAAA, BBBB (a later reply must not overwrite an earlier payload)", first.Payload, second.Payload)
	}
	for index := range first.Payload {
		first.Payload[index] = 'Z'
	}
	if third := pread(0, 4); string(third.Payload) != "AAAA" || string(second.Payload) != "BBBB" {
		t.Fatalf("after editing the first payload: third = %q, second = %q; want AAAA, BBBB", third.Payload, second.Payload)
	}
	empty := pread(0, 0)
	if opened.Session == 0 || len(empty.Payload) != 0 || empty.Session != opened.Session || empty.JSON == nil {
		t.Fatalf("empty read reply = %+v, want an empty payload with its JSON and session %d", empty, opened.Session)
	}
}
