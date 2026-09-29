package routing_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

const testServerID = "00000000-0000-4000-8000-000000000001"

// testServerServices is upstream createTestServerServices (packages/server/src/testing/host.ts): it supports only the attach and detach members and releases nothing.
type testServerServices struct{}

func (testServerServices) AttachClient(_ context.Context, presentation routing.RoutedServerPresentation) (routing.RoutedServerServiceAttachment, error) {
	return testServerAttachment{presentation: presentation}, nil
}

type testServerAttachment struct {
	presentation routing.RoutedServerPresentation
}

func (attachment testServerAttachment) InvokeService(ctx context.Context, call chord.ServiceCall, _ chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	switch {
	case call.Instance == nil && call.ServiceId == "pi.session-management" && call.Member == "attach" && len(call.Args) == 1:
		var id string
		if err := json.Unmarshal(call.Args[0], &id); err != nil {
			break
		}
		return json.RawMessage("null"), attachment.presentation.AttachSession(ctx, id)
	case call.Instance == nil && call.ServiceId == "pi.session-management" && call.Member == "detach" && len(call.Args) == 0:
		return json.RawMessage("null"), attachment.presentation.DetachSession(ctx)
	}
	return nil, errors.New("Unsupported test server service " + call.ServiceId + "." + call.Member)
}

func (testServerAttachment) Release(context.Context) error { return nil }

// newTestServerHost is upstream TestServerHost without Session storage: the cases in this package never attach a Session.
func newTestServerHost() routing.ServerHost {
	return routing.ServerHost{ServerServices: testServerServices{}}
}

// unixTestClient is upstream ProtocolTestClient over connectUnixTestClient (packages/server/src/testing/client.ts).
type unixTestClient struct {
	conn     net.Conn
	mu       sync.Mutex
	messages []protocol.ServerMessage
	changed  chan struct{}
	closed   chan struct{}
}

func connectUnixTestClient(t *testing.T, path string) *unixTestClient {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	client := &unixTestClient{conn: conn, changed: make(chan struct{}, 1), closed: make(chan struct{})}
	decoder, err := protocol.NewServerMessageDecoder(protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(client.closed)
		buffer := make([]byte, 64*1024)
		for {
			n, err := conn.Read(buffer)
			if n > 0 {
				messages, decodeErr := decoder.Push(buffer[:n])
				client.mu.Lock()
				client.messages = append(client.messages, messages...)
				client.mu.Unlock()
				select {
				case client.changed <- struct{}{}:
				default:
				}
				if decodeErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(client.close)
	return client
}

func (client *unixTestClient) close() {
	_ = client.conn.Close()
	<-client.closed
}

func (client *unixTestClient) send(t *testing.T, message protocol.ClientMessage) {
	t.Helper()
	frame, err := protocol.EncodeClientMessage(message, protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.conn.Write(frame); err != nil {
		t.Fatal(err)
	}
}

// hello sends the current protocol hello and returns the first hello or hello_error reply.
func (client *unixTestClient) hello(t *testing.T) protocol.ServerMessage {
	t.Helper()
	client.send(t, protocol.ClientHello{Version: protocol.ProtocolVersion})
	deadline := time.After(30 * time.Second)
	for {
		client.mu.Lock()
		for _, message := range client.messages {
			switch message.(type) {
			case protocol.ServerHello, protocol.ServerHelloError:
				client.mu.Unlock()
				return message
			}
		}
		client.mu.Unlock()
		select {
		case <-client.changed:
		case <-client.closed:
			t.Fatal("wire connection closed before hello")
		case <-deadline:
			t.Fatal("no hello reply")
		}
	}
}

// pollUntil is upstream expect.poll: it re-evaluates a condition on a short interval until it holds or the test's bound expires.
func pollUntil(t *testing.T, label string, condition func() bool) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("condition never held: %s", label)
		}
	}
}

// goroutinesIn counts goroutines whose stack contains needle. It observes parked router goroutines that expose no other signal, the Go analogue of the microtask ordering upstream's single event loop provides.
func goroutinesIn(needle string) int {
	return goroutinesMatching(func(stack string) bool { return strings.Contains(stack, needle) })
}

func goroutinesMatching(match func(stack string) bool) int {
	buffer := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buffer, true)
		if n < len(buffer) {
			buffer = buffer[:n]
			break
		}
		buffer = make([]byte, 2*len(buffer))
	}
	count := 0
	for stack := range strings.SplitSeq(string(buffer), "\n\n") {
		if match(stack) {
			count++
		}
	}
	return count
}

// goroutinesBlockedIn counts goroutines that are blocked on a channel receive inside a function whose stack line contains needle.
func goroutinesBlockedIn(needle string) int {
	return goroutinesMatching(func(stack string) bool {
		header, _, _ := strings.Cut(stack, "\n")
		return strings.Contains(header, "[chan receive") && strings.Contains(stack, needle)
	})
}
