package env

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// packages/env/src/connection.ts:64-69 Reply.payload carries the binary bytes of a response next to its JSON, and `session` names the
// daemon session whose handles it returned: an "open" reply yields a handle, and a "pread" on it replies with the file bytes as payload.
func TestReplyCarriesTheBinaryPayloadOfAPread(t *testing.T) {
	env, connection := remoteEnvironment(t)
	ctx := context.Background()
	content := []byte("payload \x00 bytes \xff end")
	path := filepath.Join(env.Cwd(), "data.bin")
	mustDo(os.WriteFile(path, content, 0o600))

	opened, err := connection.Request(ctx, "open", Json{"path": path, "mode": "read"}, RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	handle, _ := opened.JSON["handle"].(float64)
	reply, err := connection.Request(ctx, "pread", Json{"handle": handle, "offset": 0, "length": len(content) + 10}, RequestOptions{Session: opened.Session})
	if err != nil {
		t.Fatal(err)
	}
	if string(reply.Payload) != string(content) {
		t.Fatalf("Payload = %q, want %q", reply.Payload, content)
	}
	if reply.Session != opened.Session {
		t.Fatalf("Session = %d, want the opening session %d", reply.Session, opened.Session)
	}
	_, _ = connection.Request(ctx, "close", Json{"handle": handle}, RequestOptions{Session: opened.Session})
}
