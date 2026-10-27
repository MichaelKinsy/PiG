package sdk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func mcpArgs(t *testing.T, values ...any) []json.RawMessage {
	t.Helper()
	out := make([]json.RawMessage, len(values))
	for i, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = encoded
	}
	return out
}

type mcpTestTransport struct {
	McpTransportEvents
	version  string
	aborted  chan struct{}
	blockOn  string
	startErr error
}

func (t *mcpTestTransport) Start() error { return t.startErr }
func (t *mcpTestTransport) Send(ctx context.Context, message json.RawMessage) error {
	if t.blockOn == string(message) {
		<-ctx.Done()
		close(t.aborted)
		return ctx.Err()
	}
	return nil
}
func (t *mcpTestTransport) Close() error                { t.EmitClose(); return nil }
func (t *mcpTestTransport) SetProtocolVersion(v string) { t.version = v }

// The shim queues what the transport emits in order and answers one poll with everything queued, and only the first close.
func TestMcpTransportShimReceiveOrder(t *testing.T) {
	t.Parallel()
	c := Context{ext: &Extension{}}
	transport := &mcpTestTransport{}
	shim := newMcpTransportShim(c, transport)
	transport.EmitMessage(json.RawMessage(`{"n":1}`))
	transport.EmitError(errors.New("hiccup"))
	transport.EmitMessage(json.RawMessage(`{"n":2}`))
	transport.EmitClose()
	transport.EmitMessage(json.RawMessage(`{"n":3}`))
	answer, err := shim.serve(mcpArgs(t, "receive"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(answer)
	const want = `{"events":[{"type":"message","message":{"n":1}},{"type":"error","error":"hiccup"},{"type":"message","message":{"n":2}},{"type":"close"}]}`
	if string(encoded) != want {
		t.Fatalf("receive = %s\nwant      %s", encoded, want)
	}
}

// A poll with nothing queued waits for the next event; close wakes it and drops the transport's callback.
func TestMcpTransportShimPollWaitsAndCloseDropsCallback(t *testing.T) {
	t.Parallel()
	c := Context{ext: &Extension{}}
	transport := &mcpTestTransport{}
	shim := newMcpTransportShim(c, transport)
	answered := make(chan any, 1)
	go func() {
		answer, _ := shim.serve(mcpArgs(t, "receive"))
		answered <- answer
	}()
	select {
	case <-answered:
		t.Fatal("receive answered with nothing queued")
	case <-time.After(50 * time.Millisecond):
	}
	transport.EmitMessage(json.RawMessage(`{"n":1}`))
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Fatal("receive did not wake for a message")
	}
	if c.ext.graphCallbacks.get(shim.id) == nil {
		t.Fatal("the transport's callback is not registered")
	}
	if _, err := shim.serve(mcpArgs(t, "close")); err != nil {
		t.Fatal(err)
	}
	if c.ext.graphCallbacks.get(shim.id) != nil {
		t.Fatal("the transport's callback outlived its close")
	}
}

// An ordered send reports placed before it returns; a transport without the hook has it called just before Send; the abort op ends the send's context.
func TestMcpTransportShimSend(t *testing.T) {
	t.Parallel()
	c := Context{ext: &Extension{}}
	transport := &mcpTestTransport{aborted: make(chan struct{}), blockOn: `{"slow":true}`}
	shim := newMcpTransportShim(c, transport)
	if _, err := shim.serve(mcpArgs(t, "send", 1, json.RawMessage(`{"fast":true}`), true)); err != nil {
		t.Fatal(err)
	}
	failed := make(chan error, 1)
	go func() {
		_, err := shim.serve(mcpArgs(t, "send", 2, json.RawMessage(`{"slow":true}`), true))
		failed <- err
	}()
	for {
		shim.mu.Lock()
		held := len(shim.aborts) == 1
		shim.mu.Unlock()
		if held {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := shim.serve(mcpArgs(t, "abort", 2)); err != nil {
		t.Fatal(err)
	}
	if err := <-failed; err == nil {
		t.Fatal("an aborted send returned no error")
	}
	<-transport.aborted
	events, _ := shim.serve(mcpArgs(t, "receive"))
	encoded, _ := json.Marshal(events)
	if string(encoded) != `{"events":[{"type":"placed","seq":1},{"type":"placed","seq":2}]}` {
		t.Fatalf("events = %s", encoded)
	}
	if _, err := shim.serve(mcpArgs(t, "setProtocolVersion", "2025-06-18")); err != nil || transport.version != "2025-06-18" {
		t.Fatalf("setProtocolVersion = %v, version %q", err, transport.version)
	}
}

type mcpTestServerStore struct {
	held bool
}

func (s *mcpTestServerStore) Load() (McpOAuthState, error) { return nil, nil }
func (s *mcpTestServerStore) Save(McpOAuthState) error     { return nil }
func (s *mcpTestServerStore) WithRefreshLock(fn func() error) error {
	s.held = true
	defer func() { s.held = false }()
	return fn()
}

type mcpTestCredentials struct{ store *mcpTestServerStore }

func (c mcpTestCredentials) ForServer(string, string) (McpOAuthServerStore, error) {
	return c.store, nil
}
func (mcpTestCredentials) Tokens(string, string) (*McpOAuthTokens, error) { return nil, nil }
func (mcpTestCredentials) Remove(string, string) (bool, error)            { return false, nil }

// The refresh lock is held by the caller's store for as long as the host holds it: lock answers once inside, unlock returns the function's outcome.
func TestMcpCredentialsShimRefreshLock(t *testing.T) {
	t.Parallel()
	store := &mcpTestServerStore{}
	shim := &mcpCredentialsShim{store: mcpTestCredentials{store}}
	made, err := shim.serve(mcpArgs(t, "forServer", "demo", "https://x.example"))
	if err != nil {
		t.Fatal(err)
	}
	storeID := made.(map[string]any)["store"].(string)
	locked, err := shim.serve(mcpArgs(t, "lock", storeID))
	if err != nil {
		t.Fatal(err)
	}
	if !store.held {
		t.Fatal("lock answered outside the store's withRefreshLock")
	}
	lockID := locked.(map[string]any)["lock"].(string)
	_, err = shim.serve(mcpArgs(t, "unlock", storeID, lockID, "refresh failed", true))
	if err == nil || err.Error() != "refresh failed" {
		t.Fatalf("unlock = %v, want the host function's error", err)
	}
	if store.held {
		t.Fatal("the store still holds the lock after unlock")
	}
	if _, err := shim.serve(mcpArgs(t, "unlock", storeID, lockID, "", false)); err == nil {
		t.Fatal("a second unlock of the same lock succeeded")
	}
}

// updateConfig's and loadConfig's errors are the callbacks' errors, and their arguments decode into Pi's shapes.
func TestMcpFunctionOptionsDecodeArguments(t *testing.T) {
	t.Parallel()
	c := Context{ext: &Extension{}}
	var gotEntry McpServerEntry
	var gotPatch McpServerConfigPatch
	var gotCtx McpLoadConfigContext
	wire := map[string]any{}
	(&mcpFunctionOptions{
		loadConfig: func(ctx McpLoadConfigContext) (LoadedMcpConfig, error) {
			gotCtx = ctx
			return LoadedMcpConfig{}, errors.New("bad config")
		},
		updateConfig: func(entry McpServerEntry, patch McpServerConfigPatch) error {
			gotEntry, gotPatch = entry, patch
			return errors.New("read-only")
		},
	}).register(c, wire)
	if _, err := c.ext.graphCallbacks.get(wire["loadConfig"].(string))(mcpArgs(t, map[string]any{"cwd": "/p", "isProjectTrusted": true})); err == nil || err.Error() != "bad config" {
		t.Fatalf("loadConfig error = %v", err)
	}
	if gotCtx != (McpLoadConfigContext{Cwd: "/p", IsProjectTrusted: true}) {
		t.Fatalf("loadConfig context = %+v", gotCtx)
	}
	_, err := c.ext.graphCallbacks.get(wire["updateConfig"].(string))(mcpArgs(t, map[string]any{"name": "demo", "config": map[string]any{"command": "srv"}, "source": "/s", "scope": "global", "override": "/o"}, map[string]any{"enabled": false, "exposure": "direct"}))
	if err == nil || err.Error() != "read-only" {
		t.Fatalf("updateConfig error = %v", err)
	}
	if gotEntry.Name != "demo" || gotEntry.Config.Command != "srv" || gotEntry.Source != "/s" || gotEntry.Scope != "global" || gotEntry.Override != "/o" || gotPatch.Enabled == nil || *gotPatch.Enabled || gotPatch.Exposure != McpExposureDirect {
		t.Fatalf("updateConfig args = %+v, %+v", gotEntry, gotPatch)
	}
}

// BenchmarkMcpTransportShimSend is what the shim adds in front of the caller's Send and the event queue behind it, without the socket: one ordered send (the placed event) and one message through a poll.
func BenchmarkMcpTransportShimSend(b *testing.B) {
	c := Context{ext: &Extension{}}
	transport := &mcpTestTransport{}
	shim := newMcpTransportShim(c, transport)
	send := mcpArgs(&testing.T{}, "send", 1, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), true)
	receive := mcpArgs(&testing.T{}, "receive")
	reply := json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := shim.serve(send); err != nil {
			b.Fatal(err)
		}
		transport.EmitMessage(reply)
		if _, err := shim.serve(receive); err != nil {
			b.Fatal(err)
		}
	}
}
