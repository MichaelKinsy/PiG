package sdk

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// A host call returns once the SDK has applied every notify frame the read loop queued before the call's result (waitNotifications): Pi's getters read the live Session, so a read after an awaited call sees the state the host held when it answered. A notify frame that reaches the connection before ready is one of those frames, whoever takes it from the queue: the factory's pump before the register, or the wait for ready after it. It is applied to the replica and counted as applied. The pump took a state_update and dropped it, uncounted (since v0.5.2), so the first host call a handler made after ready waited for it for ever; the wait for ready failed the load on it.

// stateFrame is a state_update that names the session: a value the replica's fallback, "", differs from.
func stateFrame(t *testing.T, sessionName string) envelope {
	t.Helper()
	args, err := json.Marshal(map[string]any{"state": map[string]any{"session": map[string]any{"sessionId": "early", "sessionName": sessionName}}})
	if err != nil {
		t.Fatal(err)
	}
	return envelope{Type: msgNotify, Notify: &notifyMsg{Method: "state_update", Args: args}}
}

// probeExtension is an extension whose probe command makes a host call and then reads the replicated session name.
func probeExtension(name string) (*Extension, <-chan string) {
	read := make(chan string, 1)
	ext := New(name)
	ext.Command("probe", "call the host, then read the replica", func(ctx Context, _ string) error {
		ctx.Notify("probe", "info")
		read <- ctx.SessionName()
		return nil
	})
	return ext, read
}

// nextHostCall returns the next host call the extension sends. A run that ended instead fails the test with the run's error.
func nextHostCall(t *testing.T, host *mockHost, done <-chan error) envelope {
	t.Helper()
	for {
		var header [4]byte
		_, err := io.ReadFull(host.nc, header[:])
		var env envelope
		if err == nil {
			data := make([]byte, binary.BigEndian.Uint32(header[:]))
			if _, err = io.ReadFull(host.nc, data); err == nil {
				err = json.Unmarshal(data, &env)
			}
		}
		if err != nil {
			select {
			case runErr := <-done:
				t.Fatalf("the run ended before the handler's host call: %v", runErr)
			case <-time.After(5 * time.Second):
				t.Fatalf("read the handler's host call: %v", err)
			}
		}
		if env.Type == msgCall {
			return env
		}
	}
}

// requireProbeReturns runs the probe command after ready and requires that its host call returns and that it then reads want from the replica. early is the number of notify frames the host wrote before ready.
func requireProbeReturns(t *testing.T, host *mockHost, done <-chan error, ext *Extension, read <-chan string, early uint64, want string) {
	t.Helper()
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "probe", Request: &requestMsg{Method: "command", Tool: "probe"}})
	call := nextHostCall(t, host, done)
	// The loop read the request after the ready, and the ready after the early frames, so each of them was taken from the queue by now.
	ext.notifyMu.Lock()
	handled := ext.notifyHandled
	ext.notifyMu.Unlock()
	if handled != early {
		t.Fatalf("the extension counted %d of the %d notify frames it read before ready as applied: the handler's host call waits for the rest and never returns", handled, early)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{}})
	select {
	case got := <-read:
		if got != want {
			t.Fatalf("after its host call the handler read the session name %q, want %q from the state the host sent before ready", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler's host call did not return")
	}
}

// The host flushed its state ahead of the result of a call the factory made (a pi.events.on), before the extension registered: the factory's pump reads the connection then.
func TestStateBeforeRegisterIsAppliedAndCountedByTheFactoryPump(t *testing.T) {
	host := factoryHost(t)
	ext, read := probeExtension("state-before-register")
	result := make(chan error, 1)
	go func() {
		_, err := ext.Events().On("ch", func(Context, any) error { return nil })
		result <- err
	}()
	host.accept(t)
	_ = host.nc.SetDeadline(time.Now().Add(5 * time.Second))
	var call envelope
	for call = host.readEnvelope(t); call.Type != msgCall; call = host.readEnvelope(t) {
	}
	var on busWire
	if err := json.Unmarshal(call.Call.Args, &on); err != nil {
		t.Fatal(err)
	}
	host.writeEnvelope(t, stateFrame(t, "named before register"))
	// The pump takes the frames in order, so it took the state before it served this dispatch; the factory's call is still waiting.
	if msg := dispatchTo(t, host, "d1", on.HandlerID, "ch", "heard"); msg != "" {
		t.Fatalf("dispatch answered with error %q", msg)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{}})
	if err := awaitResult(t, result); err != nil {
		t.Fatalf("the factory's call = %v, want the host's result", err)
	}
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	if env := host.readEnvelope(t); env.Type != msgRegister {
		t.Fatalf("Run sent %s, want register", env.Type)
	}
	// The ready payload carries no state, so the replica holds what the early frame brought and nothing else.
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	requireProbeReturns(t, host, done, ext, read, 1, "named before register")
	stopBusExtension(t, host, done)
}

// A state the host sends between the register and the ready (a push to a restarting extension, whose connection the Host already holds) is read by the wait for ready: the load goes on and the state is applied.
func TestStateBetweenRegisterAndReadyIsAppliedAndTheLoadGoesOn(t *testing.T) {
	ext, read := probeExtension("state-before-ready")
	host, done := startBusExtension(t, ext)
	for host.readEnvelope(t).Type != msgRegister {
	}
	host.writeEnvelope(t, stateFrame(t, "named before ready"))
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	requireProbeReturns(t, host, done, ext, read, 1, "named before ready")
	stopBusExtension(t, host, done)
}

// The ready snapshot is newer than every frame read before it, so its state replaces theirs, the ordered MCP server list included (mcpServersAt).
func TestTheReadySnapshotReplacesTheStateOfAnEarlierFrame(t *testing.T) {
	ext, read := probeExtension("ready-after-state")
	host, done := startBusExtension(t, ext)
	for host.readEnvelope(t).Type != msgRegister {
	}
	early, err := json.Marshal(map[string]any{"state": map[string]any{
		"session":    map[string]any{"sessionId": "early", "sessionName": "named before ready"},
		"mcpServers": []map[string]any{{"name": "before-ready"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "state_update", Args: early}})
	atReady := json.RawMessage(`{"session":{"sessionId":"early","sessionName":"named at ready"},"mcpServers":[{"name":"at-ready"}]}`)
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80, State: atReady}})
	requireProbeReturns(t, host, done, ext, read, 1, "named at ready")
	ext.mu.RLock()
	servers := string(ext.mcpServersRaw)
	ext.mu.RUnlock()
	if servers != `[{"name":"at-ready"}]` {
		t.Fatalf("the replicated MCP server list = %s, want the ready snapshot's", servers)
	}
	stopBusExtension(t, host, done)
}
