package subprocess

import (
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// The ready payload is a connection's first state, and the Host writes no state to a connection before it. A runtime reads the frames that precede its ready while its factory still loads: the Go SDK's factory pump took a state_update there and dropped it, and the first host call a handler made afterwards waited for that frame for ever (pig stayed at "Reloading ..." after /reload). The Host wrote the frame since b3b5ae8b6f: a call it answers before the register handshake (loadingBusCall, here the factory's pi.events.on) went through replyToCall, which flushes the state to an extension that is behind a host state change, and a connection that was never pushed a state is behind every change.

// earlyCallRig loads one in-process runtime whose factory subscribes on the event bus before it registers.
type earlyCallRig struct {
	host   *Host
	bridge *UIBridge
	prompt atomic.Value // string
	// register lets the runtime send its register frame once it has read the result of the factory's call.
	register chan struct{}
	// frames are the frames the runtime read, in wire order, without the pings.
	frames chan *Envelope
	// changeInSnapshot makes the next read of the system prompt return the prompt it held and report a change to a new one: a change that lands while the Host builds a snapshot.
	changeInSnapshot atomic.Pointer[string]
}

func newEarlyCallRig(t *testing.T) *earlyCallRig {
	t.Helper()
	rig := &earlyCallRig{register: make(chan struct{}), frames: make(chan *Envelope, 16)}
	rig.host = NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { rig.host.Shutdown("test done") })
	rig.bridge = NewUIBridge(func() {})
	rig.prompt.Store("the prompt at the start")
	rig.bridge.SetHostAction("getSystemPrompt", func() string {
		held := rig.prompt.Load().(string)
		if next := rig.changeInSnapshot.Swap(nil); next != nil {
			rig.prompt.Store(*next)
			rig.bridge.HostStateChanged()
		}
		return held
	})
	rig.host.SetUIBridge(rig.bridge)
	return rig
}

// load starts the load and returns the channel its result arrives on.
func (r *earlyCallRig) load(t *testing.T) <-chan error {
	t.Helper()
	loaded := make(chan error, 1)
	go func() {
		_, err := r.host.LoadInProcess(t.Context(), ExtConfig{Name: "early-call", Enabled: true}, func(conn net.Conn) error {
			// The native SDKs subscribe by value, which binds the process's realm at the call.
			subscribe := &CallPayload{Method: callEventsOn, Args: json.RawMessage(`{"channel":"early","handlerId":"h1","value":true}`)}
			if err := writeEnvelopeTo(conn, &Envelope{Type: MsgCall, ID: "factory-call", Call: subscribe}); err != nil {
				return err
			}
			for {
				env, err := readEnvelopeFrom(conn)
				if err != nil {
					return nil
				}
				if env.Type == MsgPing && env.Ping != nil {
					_ = writeEnvelopeTo(conn, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
					continue
				}
				r.frames <- env
				if env.Type == MsgCallResult && env.ID == "factory-call" {
					<-r.register
					if err := writeEnvelopeTo(conn, &Envelope{Type: MsgRegister, Register: &RegisterPayload{Name: "early-call"}}); err != nil {
						return err
					}
				}
			}
		})
		loaded <- err
	}()
	return loaded
}

// next returns the next frame the runtime read.
func (r *earlyCallRig) next(t *testing.T) *Envelope {
	t.Helper()
	select {
	case env := <-r.frames:
		return env
	case <-time.After(5 * time.Second):
		t.Fatal("the runtime read no frame")
		return nil
	}
}

// factoryCallResult returns the result of the factory's call, which must be the first frame the runtime reads.
func (r *earlyCallRig) factoryCallResult(t *testing.T) {
	t.Helper()
	env := r.next(t)
	if env.Type == MsgNotify && env.Notify != nil {
		t.Fatalf("the Host wrote a %s notify to a connection before its ready, ahead of the result of the factory's call", env.Notify.Method)
	}
	if env.Type != MsgCallResult || env.CallResult == nil || env.CallResult.Error != nil {
		t.Fatalf("the first frame the runtime read = %+v (call result %+v), want the result of the factory's call", env, env.CallResult)
	}
}

// ready lets the runtime register and returns the state of its ready payload, which must be the next frame it reads.
func (r *earlyCallRig) ready(t *testing.T, loaded <-chan error) *StatePayload {
	t.Helper()
	close(r.register)
	env := r.next(t)
	if env.Type == MsgNotify && env.Notify != nil {
		t.Fatalf("the Host wrote a %s notify to a connection before its ready, after its register", env.Notify.Method)
	}
	if env.Type != MsgReady || env.Ready == nil || env.Ready.State == nil {
		t.Fatalf("the frame after the register = %+v, want a ready payload with the state", env)
	}
	select {
	case err := <-loaded:
		if err != nil {
			t.Fatalf("load: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the load did not end after the ready")
	}
	return env.Ready.State
}

// expectNoState requires that the Host pushes the runtime no state once every queued push has run.
func (r *earlyCallRig) expectNoState(t *testing.T, why string) {
	t.Helper()
	r.host.drainTasks.Wait()
	for {
		select {
		case env := <-r.frames:
			if env.Type == MsgNotify && env.Notify != nil && env.Notify.Method == "state_update" {
				t.Fatalf("%s: the Host pushed a state_update of %d bytes after the ready", why, len(env.Notify.Args))
			}
		case <-time.After(150 * time.Millisecond):
			return
		}
	}
}

// A host state change before the load leaves the new connection behind it. The reply to the factory's call carries no state: the ready payload does, once.
func TestAFactoryCallAfterAHostStateChangeIsAnsweredWithoutAStatePush(t *testing.T) {
	rig := newEarlyCallRig(t)
	rig.prompt.Store("changed before the load")
	rig.bridge.HostStateChanged()
	loaded := rig.load(t)
	rig.factoryCallResult(t)
	if got := rig.ready(t, loaded).SystemPrompt; got != "changed before the load" {
		t.Fatalf("the ready payload carries the prompt %q, want the host's", got)
	}
	rig.expectNoState(t, "the ready payload held every change")
}

// A change between the factory's call and the register is in the ready payload, whose snapshot the Host takes when the runtime registers.
func TestAHostStateChangeAfterAFactoryCallIsInTheReadyPayload(t *testing.T) {
	rig := newEarlyCallRig(t)
	loaded := rig.load(t)
	rig.factoryCallResult(t)
	rig.prompt.Store("changed after the factory's call")
	rig.bridge.HostStateChanged()
	if got := rig.ready(t, loaded).SystemPrompt; got != "changed after the factory's call" {
		t.Fatalf("the ready payload carries the prompt %q, want the host's", got)
	}
}

// A change that lands after the ready snapshot read the value and before the ready is sent is in no frame yet: the Host pushes it right after the ready, without waiting for the next request or call.
func TestAHostStateChangeTheReadySnapshotMissedIsPushedAfterTheReady(t *testing.T) {
	rig := newEarlyCallRig(t)
	loaded := rig.load(t)
	rig.factoryCallResult(t)
	missed := "changed while the ready snapshot was taken"
	rig.changeInSnapshot.Store(&missed)
	if got := rig.ready(t, loaded).SystemPrompt; got != "the prompt at the start" {
		t.Fatalf("the ready payload carries the prompt %q, want the one its snapshot read before the change", got)
	}
	env := rig.next(t)
	if env.Type != MsgNotify || env.Notify == nil || env.Notify.Method != "state_update" {
		t.Fatalf("the frame after the ready = %+v, want the state the snapshot missed", env)
	}
	if got := string(decodePushedState(t, env)["systemPrompt"]); got != `"`+missed+`"` {
		t.Fatalf("the push after the ready carries the prompt %s, want the change the snapshot missed", got)
	}
}
