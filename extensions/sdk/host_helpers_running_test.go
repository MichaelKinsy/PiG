package sdk

import "testing"

// A packed cell runs several extensions in one process. A helper called with no Context and no socket of its own uses a running extension's connection, and
// must keep working when the first one to start has stopped.
func TestHelperConnectionSurvivesTheFirstRunningExtensionStopping(t *testing.T) {
	first, second := &conn{}, &conn{}
	adoptHelperConn(first, "/s/e-0.sock", nil)
	adoptHelperConn(second, "/s/e-1.sock", nil)
	t.Cleanup(func() { releaseHelperConn(first); releaseHelperConn(second) })
	t.Setenv("PIG_EXT_SOCKET", "")
	if got, err := helperConnection(); err != nil || got != first {
		t.Fatalf("helperConnection = %p, %v; want the first extension's %p", got, err, first)
	}
	releaseHelperConn(first)
	if got, err := helperConnection(); err != nil || got != second {
		t.Fatalf("after the first stopped: helperConnection = %p, %v; want the second's %p", got, err, second)
	}
	releaseHelperConn(second)
	if _, err := helperConnection(); err == nil {
		t.Fatal("with no extension running and no socket, a helper has no connection to use")
	}
}

// The runner names a member's socket (PIG_EXT_SOCKET) while that member's factory runs. The host accepts a packed cell's members in order and serves a
// connection only after the members before it: a helper called by the first member's factory that rode the second member's registered connection waited for
// an answer the host would not send until the first member had connected (the conformance rust-packed hang).
func TestHelperNamedForASocketDoesNotUseAnotherMembersConnection(t *testing.T) {
	second := &conn{}
	adoptHelperConn(second, "/s/e-1.sock", nil)
	t.Cleanup(func() { releaseHelperConn(second) })
	t.Setenv("PIG_EXT_SOCKET", "/s/e-1.sock")
	if got, err := helperConnection(); err != nil || got != second {
		t.Fatalf("on the running member's own socket: helperConnection = %p, %v; want %p", got, err, second)
	}
	// Nothing listens on the first member's socket: a helper that dials it fails, where one that took the second member's connection would wait for the host.
	t.Setenv("PIG_EXT_SOCKET", t.TempDir()+"/e-0.sock")
	if got, err := helperConnection(); err == nil {
		t.Fatalf("a helper naming a socket no extension is running on used a connection (%p) instead of dialling it", got)
	}
	// A connection with no socket of its own (RunWithConn) answers whichever socket is named.
	fused := &conn{}
	adoptHelperConn(fused, "", nil)
	t.Cleanup(func() { releaseHelperConn(fused) })
	if got, err := helperConnection(); err != nil || got != fused {
		t.Fatalf("a connection with no socket: helperConnection = %p, %v; want %p", got, err, fused)
	}
}
