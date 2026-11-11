package subprocess

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A frame holds the frame lock while it waits for the extension's answer to a callback, and disposing a screen (a shutdown or reload) takes that lock. An
// extension that never answers (a render stuck in a loop, with its heartbeat thread alive) must not hold the host's shutdown: disposing ends the screen's
// callbacks, tells the extension to stop them, and returns.
func TestDisposingAScreenEndsAFrameThatWaitsForAnExtensionThatNeverAnswers(t *testing.T) {
	hostEnd, peer := net.Pipe()
	conn := NewConn("wedged", hostEnd)
	ctx, cancel := context.WithCancel(context.Background())
	conn.Start(ctx)
	rendering := make(chan string, 1)
	cancelled := make(chan string, 1)
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		for {
			env, err := readEnvelope(peer)
			if err != nil {
				return
			}
			var reply *Envelope
			switch {
			case env.Type == MsgPing:
				reply = &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}}
			case env.Type == MsgCancel:
				select {
				case cancelled <- env.Cancel.RequestID:
				default:
				}
			case env.Type == MsgRequest && env.Request.Method == RequestTuiAltCallback:
				var callback struct {
					Kind string `json:"kind"`
				}
				_ = json.Unmarshal(env.Request.Args, &callback)
				if callback.Kind == "render" {
					select {
					case rendering <- env.ID:
					default:
					}
					continue
				}
				reply = &Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{}}
			}
			if reply != nil && writeEnvelope(peer, reply) != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = peer.Close()
		_ = hostEnd.Close()
		<-peerDone
		<-conn.done
	})

	b := NewUIBridge(func() {})
	for _, call := range []string{
		`{"op":"create","id":"s","terminal":{"columns":20,"rows":6}}`,
		`{"op":"addChild","id":"s","key":"doc","caps":{}}`,
		`{"op":"start","id":"s"}`,
	} {
		if _, err := b.handleTuiAlt(conn, "", json.RawMessage(call)); err != nil {
			t.Fatalf("%s: %v", call, err)
		}
	}
	var renderID string
	select {
	case renderID = <-rendering:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the screen never asked the extension to render its component")
	}

	returnsWithinBudget(t, "disposing a screen whose frame waits for an extension that never answers", func() { b.disposeTuiAlt(conn, false) })
	select {
	case id := <-cancelled:
		if id != renderID {
			t.Errorf("the extension was told to cancel %q, want the render request %q", id, renderID)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Error("the extension was not told to stop the render the frame was waiting for")
	}
}
