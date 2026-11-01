package sdk

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"runtime"
	"testing"
)

// A host can dispatch to a listener while the extension loads, so events.dispatch requests arrive before ready. The handler goroutine for each must serve the request that started it, not whatever frame the receive loop reads next. A handler that shares the loop's envelope variable reads it after the loop has overwritten it with the following dispatch or the ready frame: it answers under another frame's ID with another frame's payload, and `go test -race` reports the read against the loop's write.
//
// One processor and one write that carries every frame make the order fixed. The read loop queues the frames as one burst, and the receive loop, which runs the handler goroutine only when it blocks, takes all of them before that goroutine first runs.
func TestPreReadyDispatchHandlersKeepTheirOwnRequest(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	heard := make(chan string, 2)
	ext := New("bus")
	if _, err := ext.Events().On("ch", func(_ Context, data any) error {
		encoded, _ := json.Marshal(data)
		heard <- string(encoded)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host, done := startBusExtension(t, ext)
	on, _ := readBusCall(t, host, "events.on", "null")
	for {
		if env := host.readEnvelope(t); env.Type == msgRegister {
			break
		}
	}
	payloads := map[string]string{"d1": `{"n":1}`, "d2": `{"n":2}`}
	var burst bytes.Buffer
	frames := []envelope{
		dispatchFrame(t, "d1", on.HandlerID, payloads["d1"]),
		dispatchFrame(t, "d2", on.HandlerID, payloads["d2"]),
		{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}},
	}
	for _, frame := range frames {
		data, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
		burst.Write(hdr[:])
		burst.Write(data)
	}
	if _, err := host.nc.Write(burst.Bytes()); err != nil {
		t.Fatal(err)
	}

	answered := map[string]bool{}
	for len(answered) < 2 {
		env := host.readEnvelope(t)
		if env.Type != msgResponse {
			continue
		}
		if _, ok := payloads[env.ID]; !ok || answered[env.ID] {
			t.Fatalf("response under ID %q, want one each for d1 and d2", env.ID)
		}
		if env.Response == nil || env.Response.Error != nil {
			t.Fatalf("response %s = %+v", env.ID, env.Response)
		}
		answered[env.ID] = true
	}
	got := map[string]bool{<-heard: true, <-heard: true}
	if !got[payloads["d1"]] || !got[payloads["d2"]] {
		t.Fatalf("the listener heard %v, want %s and %s", got, payloads["d1"], payloads["d2"])
	}
	stopBusExtension(t, host, done)
}

func dispatchFrame(t *testing.T, id, handlerID, payload string) envelope {
	t.Helper()
	args, err := json.Marshal(map[string]any{"handlerId": handlerID, "channel": "ch", "json": json.RawMessage(payload)})
	if err != nil {
		t.Fatal(err)
	}
	return envelope{Type: msgRequest, ID: id, Request: &requestMsg{Method: "events.dispatch", Args: args}}
}
