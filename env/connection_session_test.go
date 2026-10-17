package env

import (
	"errors"
	"testing"
)

// packages/env/src/connection.ts:185-188 Connection.session connects if needed and rejects with the connection's failure; a command that
// cannot start is a spawn_error that counts as a lost connection (connection.ts:262, 108-114).
func TestConnectionSessionReportsAStartFailure(t *testing.T) {
	t.Parallel()
	connection := NewConnection(ConnectionOptions{Command: []string{"/nonexistent/pi-env-daemon"}})
	t.Cleanup(connection.Close)
	id, err := connection.Session(background)
	if err == nil {
		t.Fatalf("Session over a daemon that cannot start = %d, nil; want the start failure", id)
	}
	if !IsConnectionLost(err) {
		t.Fatalf("Session error %v is not a lost connection", err)
	}
	var remote *RemoteError
	_ = errors.As(err, &remote)
	if remote == nil || remote.Fields["code"] != "spawn_error" {
		t.Fatalf("Session error %v: want code spawn_error", err)
	}
}

// connection.ts:190-195 request with options.session throws lost("pi-env connection lost") when the session is another one or no longer live;
// the handle of a dead daemon session must not be sent to its replacement.
func TestRequestOptionsSessionFailsWithTheLostMessageForAStaleOrDeadSession(t *testing.T) {
	t.Parallel()
	connection := daemonConnection(t)
	session, err := connection.Session(background)
	if err != nil {
		t.Fatal(err)
	}
	want := "pi-env connection lost"
	_, err = connection.Request(background, "hello", Json{}, RequestOptions{Session: session + 1})
	var remote *RemoteError
	_ = errors.As(err, &remote)
	if remote == nil || remote.Error() != want {
		t.Fatalf("a request for another session: %v, want %q", err, want)
	}
	connection.mu.Lock()
	current := connection.session
	connection.mu.Unlock()
	connection.teardown(current, lost("pi-env connection lost"))
	_, err = connection.Request(background, "hello", Json{}, RequestOptions{Session: session})
	remote = nil
	_ = errors.As(err, &remote)
	if remote == nil || remote.Error() != want || !IsConnectionLost(err) {
		t.Fatalf("a request for a dead session: %v, want a lost connection %q", err, want)
	}
	replacement, err := connection.Session(background)
	if err != nil || replacement == session {
		t.Fatalf("after the loss the next request starts a new session: id %d (old %d), err %v", replacement, session, err)
	}
}
