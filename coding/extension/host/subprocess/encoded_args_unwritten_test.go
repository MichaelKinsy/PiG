package subprocess

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// D31: the host never writes the Args of a request after it has sent them (RequestPayload.encodedArgs). A fused extension reads the caller's bytes by reference, not a copy, and releasing the frame (its buffer goes back to the pool) and encoding other frames over that buffer leave the Args as they were. Under -race, a reader that goes over the Args while the host sends and releases frames reports any write to them.
func TestEncodedArgsAreNeverWrittenAfterTheSend(t *testing.T) {
	args := json.RawMessage(`{"text":"unchanged","list":[1,2,3]}`)
	snapshot := bytes.Clone(args)
	host, ext := newFusedConn()
	const rounds = 100
	type read struct{ shell, args []byte }
	reads := make(chan read, 2*rounds)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 2 * rounds {
			shell, got, err := ext.ReadFrameParts()
			if err != nil {
				t.Error(err)
				return
			}
			// Go over the bytes the host shares with this reader while the host goes on.
			if got != nil {
				_ = bytes.Equal(got, snapshot)
			}
			reads <- read{shell, got}
		}
	}()
	send := func(method string, args json.RawMessage) {
		t.Helper()
		request := RequestPayload{Method: method, Event: "turn_end", Args: args, encodedArgs: true}
		frame, err := encodeFrame(&Envelope{Type: MsgRequest, ID: "r1", Request: &request})
		if err != nil {
			t.Fatal(err)
		}
		defer frame.release()
		if err := host.WriteFrameParts(frame.parts()); err != nil {
			t.Fatal(err)
		}
	}
	for i := range rounds {
		send("event", args)
		// The next frame reuses the pooled buffer the last one gave back.
		send("tool_call", json.RawMessage(`{"other":`+strconv.Itoa(i)+`}`))
	}
	<-done
	close(reads)
	i := 0
	for r := range reads {
		if i%2 == 0 {
			if !bytes.Equal(r.args, snapshot) || &r.args[0] != &args[0] {
				t.Fatalf("round %d: the extension read %q (shared with the caller: %v), want the caller's bytes %q by reference", i/2, r.args, len(r.args) > 0 && &r.args[0] == &args[0], snapshot)
			}
			if !bytes.Contains(r.shell, []byte(`"method":"event"`)) {
				t.Fatalf("round %d: shell %q", i/2, r.shell)
			}
		}
		i++
	}
	if !bytes.Equal(args, snapshot) {
		t.Fatalf("the host wrote the caller's args: %q, want %q", args, snapshot)
	}
}
