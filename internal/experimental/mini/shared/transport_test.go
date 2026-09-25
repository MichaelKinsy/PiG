package shared

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for transport")
		var zero T
		return zero
	}
}

// mini/shared/transport.ts only dispatches LF-terminated, nonempty lines. Unlike modes/rpc/jsonl.ts, it discards the final unterminated line.
func TestJSONConnectionPartialFrames(t *testing.T) {
	in, out := io.Pipe()
	c := JsonConnection(in, discardCloser{io.Discard}, nil)
	t.Cleanup(func() { _ = c.Close(); c.Wait() })
	messages := make(chan json.RawMessage, 4)
	closed := make(chan struct{})
	c.OnClose(func() { close(closed) })
	c.OnMessage(func(m json.RawMessage) { messages <- m })
	for _, chunk := range []string{"\n{\"text\":\"", "é世", "界\u2028\u2029\"}\r\n{\"n\":", "2}\n{\"ignored\":true}"} {
		if _, err := io.WriteString(out, chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	receive(t, closed)
	if got := string(receive(t, messages)); got != "{\"text\":\"é世界\u2028\u2029\"}\r" {
		t.Fatalf("first frame: %q", got)
	}
	if got := string(receive(t, messages)); got != `{"n":2}` {
		t.Fatalf("second frame: %q", got)
	}
	if len(messages) != 0 {
		t.Fatal("unterminated frame was dispatched")
	}
	if err := c.Err(); err != nil {
		t.Fatal(err)
	}
}

type discardCloser struct{ io.Writer }

func (discardCloser) Close() error { return nil }

type bufferCloser struct {
	bytes.Buffer
	written chan struct{}
}

func (b *bufferCloser) Write(p []byte) (int, error) {
	n, err := b.Buffer.Write(p)
	close(b.written)
	return n, err
}
func (*bufferCloser) Close() error { return nil }

func TestJSONConnectionSendWireAndClose(t *testing.T) {
	in, out := io.Pipe()
	buf := bufferCloser{written: make(chan struct{})}
	var closes atomic.Int32
	c := JsonConnection(in, &buf, func() error { closes.Add(1); return out.Close() })
	c.OnMessage(func(json.RawMessage) {})
	// JSON.stringify preserves literal separators, HTML, and a literal backslash-u escape.
	if err := c.Send(struct {
		Text string `json:"text"`
	}{"<>&\u2028\u2029\\u2028"}); err != nil {
		t.Fatal(err)
	}
	receive(t, buf.written)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c.Wait()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Send(make(chan int)); err != nil {
		t.Fatalf("send after close: %v", err)
	}
	if got, want := buf.String(), "{\"text\":\"<>&\u2028\u2029\\\\u2028\"}\n"; got != want {
		t.Fatalf("wire %q, want %q", got, want)
	}
	if closes.Load() != 2 {
		t.Fatalf("cleanup ran %d times", closes.Load())
	}
}

func TestJSONConnectionMalformedAndLargeFrame(t *testing.T) {
	for _, text := range []string{`{"unfinished":`, strings.Repeat("x", 1024*1024)} {
		t.Run(text[:min(12, len(text))], func(t *testing.T) {
			in, out := io.Pipe()
			c := JsonConnection(in, discardCloser{io.Discard}, nil)
			t.Cleanup(func() { _ = c.Close(); c.Wait() })
			messages := make(chan json.RawMessage, 1)
			closed := make(chan struct{})
			c.OnClose(func() { close(closed) })
			c.OnMessage(func(m json.RawMessage) { messages <- m })
			wire := text + "\n"
			if len(text) > 1024 {
				b, err := json.Marshal(text)
				if err != nil {
					t.Fatal(err)
				}
				wire = string(b) + "\n"
			}
			if _, err := io.WriteString(out, wire); err != nil {
				t.Fatal(err)
			}
			if len(text) > 1024 {
				var got string
				if err := json.Unmarshal(receive(t, messages), &got); err != nil {
					t.Fatal(err)
				}
				if got != text {
					t.Fatal("large frame truncated")
				}
			} else {
				receive(t, closed)
				if c.Err() == nil {
					t.Fatal("malformed JSON silently ignored")
				}
			}
		})
	}
}

func TestSocketTransportRoundTrip(t *testing.T) {
	transport := SocketTransport(filepath.Join(t.TempDir(), "mini.sock"))
	accepted := make(chan *JSONConnection, 1)
	listener, err := transport.Listen(func(c *JSONConnection) { accepted <- c })
	if err != nil {
		t.Fatal(err)
	}
	client, err := transport.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	server := receive(t, accepted)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		client.Wait()
		server.Wait()
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	got := make(chan json.RawMessage, 1)
	server.OnMessage(func(m json.RawMessage) { got <- m })
	if err := client.Send(map[string]string{"hello": "world"}); err != nil {
		t.Fatal(err)
	}
	if string(receive(t, got)) != `{"hello":"world"}` {
		t.Fatal("socket payload changed")
	}
}

func TestJSONConnectionConcurrentSend(t *testing.T) {
	a, b := net.Pipe()
	c := JsonConnection(a, a, nil)
	other := JsonConnection(b, b, nil)
	t.Cleanup(func() { _ = c.Close(); _ = other.Close(); c.Wait(); other.Wait() })
	got := make(chan json.RawMessage, 100)
	other.OnMessage(func(m json.RawMessage) { got <- m })
	var wg sync.WaitGroup
	for i := range cap(got) {
		wg.Go(func() {
			if err := c.Send(i); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	seen := make(map[int]bool)
	for range cap(got) {
		var n int
		if err := json.Unmarshal(receive(t, got), &n); err != nil {
			t.Fatal(err)
		}
		seen[n] = true
	}
	if len(seen) != cap(got) {
		t.Fatal("interleaved or lost frames")
	}
}

func TestJSONConnectionUpstreamOracle(t *testing.T) {
	root, err := filepath.Abs("../../../../.upstream/current")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/transport-oracle.mjs", root)
	oracle, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("upstream probe: %v: %s", err, oracle)
	}
	var expected struct {
		Messages   []json.RawMessage `json:"messages"`
		Wire       string            `json:"wire"`
		Closes     int               `json:"closes"`
		Cleanups   int               `json:"cleanups"`
		LateCloses int               `json:"lateCloses"`
	}
	if err := json.Unmarshal(oracle, &expected); err != nil {
		t.Fatal(err)
	}
	in, out := io.Pipe()
	buf := bufferCloser{written: make(chan struct{})}
	cleanups, lateCloses := 0, 0
	c := JsonConnection(in, &buf, func() error { cleanups++; return nil })
	t.Cleanup(func() { _ = c.Close(); c.Wait() })
	messages := make(chan json.RawMessage, len(expected.Messages))
	closed := make(chan struct{})
	c.OnMessage(func(m json.RawMessage) { messages <- m })
	c.OnClose(func() { close(closed) })
	wire := []byte("\n{\"text\":\"é世界\u2028\u2029\"}\r\n{\"n\":2}\n{\"ignored\":true}")
	for _, b := range wire {
		if _, err := out.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Send(struct {
		Text string `json:"text"`
	}{"<>&\u2028\u2029\\u2028"}); err != nil {
		t.Fatal(err)
	}
	receive(t, buf.written)
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	receive(t, closed)
	c.Wait()
	c.OnClose(func() { lateCloses++ })
	for range 2 {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if cleanups != expected.Cleanups || lateCloses != expected.LateCloses {
		t.Fatalf("cleanup=%d late=%d != Pi %+v", cleanups, lateCloses, expected)
	}
	for _, want := range expected.Messages {
		var compact bytes.Buffer
		if err := json.Compact(&compact, receive(t, messages)); err != nil {
			t.Fatal(err)
		}
		if compact.String() != string(want) {
			t.Fatalf("Pig %s != Pi %s", compact.String(), want)
		}
	}
	if buf.String() != expected.Wire || expected.Closes != 1 || len(messages) != 0 {
		t.Fatalf("Pig wire %q != Pi %+v", buf.String(), expected)
	}
}

func TestProtocolWireShapes(t *testing.T) {
	if names := []string{Lane.Name, Models.Name, Worker.Name, Sessions.Name}; !reflect.DeepEqual(names, []string{"lane", "models", "worker", "sessions"}) {
		t.Fatal(names)
	}
	empty := ""
	for _, tt := range []struct {
		value any
		want  string
	}{
		{CommandResult{OK: true}, `{"ok":true}`},
		{CommandResult{Error: &empty}, `{"ok":false,"error":""}`},
		{ModelRef{Provider: "local", ModelID: "test"}, `{"provider":"local","modelId":"test"}`},
		{ModelsEvent{Type: "prompt", RequestID: &empty, Request: &AuthPromptRequest{Type: "text", Message: "value", Placeholder: &empty}}, `{"type":"prompt","requestId":"","request":{"type":"text","message":"value","placeholder":""}}`},
		{LaneEvent{SubscriptionID: "view", Event: json.RawMessage(`{"type":"run_end","tipId":null}`)}, `{"subscriptionId":"view","event":{"type":"run_end","tipId":null}}`},
	} {
		got, err := json.Marshal(tt.value)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tt.want {
			t.Fatalf("%s != %s", got, tt.want)
		}
	}
}
