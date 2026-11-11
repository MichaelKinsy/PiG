package subprocess

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"sync"
	"testing"
	"time"
)

// A frame parseResponseFrame recognizes decodes to exactly the Envelope it reports, for the frames the SDKs write and for every single-byte corruption of them: the fast path never accepts a frame the full decode reads differently.
func TestParseResponseFrameAgreesWithTheFullDecode(t *testing.T) {
	frames := []string{
		`{"type":"response","id":"r7","response":{}}`,
		`{"type":"response","id":"r7","response":{"result":null}}`,
		`{"type":"response","id":"r12345","response":{"result":{"a":[1,2,{"b":"c}}"}],"d":"\u00e9"}}}`,
		`{"type":"response","id":"r7","response":{"result":"a very long result string that is longer than the sixty-four bytes of a frame"}}`,
		`{"type":"response","id":"r7","response":{"result":1,"error":null}}`,
		`{"type":"response","id":"r7","response":{"result":1},"x":1}`,
		`{"type":"response","id":"r7","response":{"result":1}, "x":{"y":1}}`,
		`{"type":"response","id":"r7","response":{"error":{"message":"m"}}}`,
		`{"type":"response","id":"","response":{}}`,
		`{"type":"response","id":"r\u0037","response":{}}`,
		`{"type":"response","id":"r7","response":{"result":"\ud800"}}`,
		`{"type":"response","id":"r7","response":{"result":1}}}`,
		`{"type":"response","id":"r7","response":{"result":}}`,
		`{"type":"response","id":"r7","response":{"result":1,}}`,
		`{"type":"call_result","id":"r7","response":{}}`,
	}
	for _, frame := range frames {
		data := []byte(frame)
		check := func(data []byte) {
			t.Helper()
			id, result, ok := parseResponseFrame(data)
			if !ok {
				return
			}
			var env Envelope
			if err := decodeEnvelope(data, &env); err != nil {
				t.Fatalf("%q: recognized, but the full decode fails: %v", data, err)
			}
			want := Envelope{Type: MsgResponse, ID: string(id), Response: &ResponsePayload{Result: result}}
			if len(result) == 0 {
				want.Response.Result = nil
			}
			if env.Type != want.Type || env.ID != want.ID || env.Response == nil || !bytes.Equal(env.Response.Result, want.Response.Result) || (env.Response.Result == nil) != (want.Response.Result == nil) || env.Response.Error != nil {
				t.Fatalf("%q: recognized as %+v, the full decode reads %+v (%+v)", data, want, env, env.Response)
			}
			rest := env
			rest.Type, rest.ID, rest.Response = "", "", nil
			if rest != (Envelope{}) {
				t.Fatalf("%q: recognized, but the full decode reads other members: %+v", data, rest)
			}
		}
		check(data)
		for i := range data {
			for _, b := range []byte{'"', '\\', '}', '{', ',', ' ', ':', 0xff, 'x', '0'} {
				mutated := bytes.Clone(data)
				mutated[i] = b
				check(mutated)
			}
		}
	}
	for _, frame := range frames[:4] {
		if _, _, ok := parseResponseFrame([]byte(frame)); !ok {
			t.Errorf("%q: the frame an SDK writes is not recognized", frame)
		}
	}
}

// Every request ID of a connection is its own "r" and number, whichever goroutine took it, across the chunks that spell them.
func TestRequestIDsAreUniqueAcrossChunks(t *testing.T) {
	c := NewConn("ids", nil)
	const workers, each = 8, 500
	ids := make([][]string, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for range each {
				ids[w] = append(ids[w], c.newRequestID())
			}
		})
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, list := range ids {
		for _, id := range list {
			if seen[id] {
				t.Fatalf("request ID %s taken twice", id)
			}
			seen[id] = true
			var n uint64
			if _, err := fmt.Sscanf(id, "r%d", &n); err != nil || n < 1 || n > workers*each || fmt.Sprintf("r%d", n) != id {
				t.Fatalf("request ID %q is not r and a number from 1 to %d", id, workers*each)
			}
		}
	}
	if len(seen) != workers*each {
		t.Fatalf("%d distinct IDs, want %d", len(seen), workers*each)
	}
}

// echoResponder answers each request frame, after a random delay, with a response whose result is the request's own id, so a request that receives another's response is caught.
func echoResponder(conn net.Conn, seed uint64) {
	rng := rand.New(rand.NewPCG(seed, 1))
	var write sync.Mutex
	var length [4]byte
	idKey := []byte(`"id":"`)
	for {
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return
		}
		frame := make([]byte, binary.BigEndian.Uint32(length[:]))
		if _, err := io.ReadFull(conn, frame); err != nil {
			return
		}
		start := bytes.Index(frame, idKey)
		if start < 0 {
			continue
		}
		start += len(idKey)
		id := frame[start : start+bytes.IndexByte(frame[start:], '"')]
		delay := time.Duration(rng.IntN(3)) * 100 * time.Microsecond
		go func() {
			time.Sleep(delay)
			reply := append([]byte{0, 0, 0, 0}, `{"type":"response","id":"`...)
			reply = append(reply, id...)
			reply = append(reply, `","response":{"result":"`...)
			reply = append(reply, id...)
			reply = append(reply, `"}}`...)
			binary.BigEndian.PutUint32(reply[:4], uint32(len(reply)-4))
			write.Lock()
			_, _ = conn.Write(reply)
			write.Unlock()
		}()
	}
}

// A request that ends before its response, or whose channel saw a second answer, never hands its channel's content to another request: every response a request receives is the response to its own id.
func TestResponseChannelsNeverCrossRequests(t *testing.T) {
	hostSide, extSide := net.Pipe()
	conn := NewConn("echo", hostSide)
	conn.Start(t.Context())
	go echoResponder(extSide, 1)
	t.Cleanup(func() {
		_ = conn.Close("test done")
		_ = extSide.Close()
	})
	const workers, each = 8, 300
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(w), 2))
			for range each {
				ctx := context.Background()
				cancel := func() {}
				if rng.IntN(3) == 0 {
					ctx, cancel = context.WithTimeout(ctx, time.Duration(rng.IntN(300))*time.Microsecond)
				}
				env := &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "event", Event: "message_update", Args: []byte(`{}`)}}
				resp, err := conn.Request(ctx, env)
				cancel()
				if err != nil {
					continue
				}
				want := fmt.Sprintf("%q", env.ID)
				if resp.ID != env.ID || resp.Response == nil || string(resp.Response.Result) != want {
					t.Errorf("request %s received %+v (%+v)", env.ID, resp, resp.Response)
					return
				}
				releaseResponse(resp)
			}
		})
	}
	wg.Wait()
}
