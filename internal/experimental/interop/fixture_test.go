//go:build !windows

package interop

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

const (
	logicalServerID = "00000000-0000-4000-8000-000000000001"
	otherServerID   = "ffffffff-ffff-4fff-bfff-ffffffffffff"
)

// socketDirectory keeps socket paths under the Unix path limit.
func socketDirectory(t testing.TB) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "pi-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

// eventLog records what a server's echo host observed. Both server fixtures emit the same events.
type eventLog struct {
	mu     sync.Mutex
	events []string
	notify chan struct{}
}

func newEventLog() *eventLog { return &eventLog{notify: make(chan struct{}, 1024)} }

func (log *eventLog) add(event any) {
	encoded, err := json.Marshal(event)
	if err != nil {
		panic(err)
	}
	log.addRaw(string(encoded))
}

// addRaw normalizes through a generic decode so key order and number spelling do not matter.
func (log *eventLog) addRaw(text string) {
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		panic(err)
	}
	normalized, _ := json.Marshal(value)
	log.mu.Lock()
	log.events = append(log.events, string(normalized))
	log.mu.Unlock()
	select {
	case log.notify <- struct{}{}:
	default:
	}
}

// snapshot returns the events sorted: concurrent requests on one connection may be observed in either order.
func (log *eventLog) snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	events := slices.Clone(log.events)
	slices.Sort(events)
	return events
}

// ordered returns the events in the order the host observed them.
func (log *eventLog) ordered() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.events)
}

// summarize reports a long string by its code point count, as testdata/node-server.mjs does.
func summarize(value any) any {
	switch typed := value.(type) {
	case string:
		if len(utf16.Encode([]rune(typed))) > 1000 {
			return fmt.Sprintf("<%d code points>", utf8.RuneCountInString(typed))
		}
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = summarize(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = summarize(item)
		}
		return out
	}
	return value
}

type counterState struct {
	Count int    `json:"count"`
	Label string `json:"label"`
}

// counter is the Go twin of testdata/node-server.mjs createCounterEndpoint: replicated state plus two mutating methods.
type counter interface {
	State() chord.ReplicatedStateOf[*counterState]
	Bump(ctx context.Context, n int) error
	Rename(ctx context.Context, label string) error
}

type counterImplementation struct {
	state *chord.MutableReplicatedState[*counterState]
}

func (c counterImplementation) State() chord.ReplicatedStateOf[*counterState] { return c.state }
func (c counterImplementation) Bump(ctx context.Context, n int) error {
	return c.state.Change(ctx, func(draft *counterState) error { draft.Count += n; return nil })
}
func (c counterImplementation) Rename(ctx context.Context, label string) error {
	return c.state.Change(ctx, func(draft *counterState) error { draft.Label = label; return nil })
}

var counterDefinition = chord.DefineService[counter]("interop.counter")

func createCounterEndpoint() (chord.RemoteServiceEndpoint, error) {
	state, err := chord.NewReplicatedState(&counterState{Label: "start"})
	if err != nil {
		return nil, err
	}
	provider, err := chord.NewRemoteServiceProvider(chord.SingletonService(counterDefinition))
	if err != nil {
		return nil, err
	}
	if err := chord.Provide[counter](provider, counterDefinition, counterImplementation{state}); err != nil {
		return nil, err
	}
	return chord.CreateRemoteServiceEndpoint(provider), nil
}

// echoHandle is the Go twin of testdata/node-server.mjs EchoHandle.
type echoHandle struct {
	id  string
	log *eventLog
}

func (h *echoHandle) AttachClient(context.Context) (routing.RoutedSessionAttachment, error) {
	endpoint, err := createCounterEndpoint()
	if err != nil {
		return nil, err
	}
	return &echoAttachment{handle: h, counter: endpoint}, nil
}
func (h *echoHandle) Terminated() <-chan struct{} { return nil }
func (h *echoHandle) TerminalError() error        { return nil }
func (h *echoHandle) Close(context.Context) error {
	h.log.add(map[string]any{"event": "close", "session": h.id})
	return nil
}

type echoAttachment struct {
	handle  *echoHandle
	counter chord.RemoteServiceEndpoint
}

func (a *echoAttachment) Release(context.Context) error {
	a.counter.Dispose()
	a.handle.log.add(map[string]any{"event": "release", "session": a.handle.id})
	return nil
}

// observe records the call; an initiating attachment does so in its synchronous admission prefix.
func (a *echoAttachment) observe(call chord.ServiceCall) ([]byte, error) {
	encoded, err := json.Marshal(call)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return nil, err
	}
	a.handle.log.add(map[string]any{"event": "call", "call": summarize(generic)})
	return encoded, nil
}

// BeginInvokeService is the attachment's admission boundary: it observes the call before returning, as the TypeScript
// echo host does in the synchronous prefix of invokeService.
func (a *echoAttachment) BeginInvokeService(ctx context.Context, call chord.ServiceCall, publish chord.ServiceUpdatePublisher) (*chord.ServiceInvocation, error) {
	encoded, err := a.observe(call)
	if err != nil {
		return nil, err
	}
	if a.isCounterCall(call) {
		return chord.BeginEndpointInvoke(ctx, a.counter, call, publish)
	}
	type outcome struct {
		result json.RawMessage
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := a.run(ctx, call, encoded)
		done <- outcome{result, err}
	}()
	return chord.NewServiceInvocation(func(waitContext context.Context) (json.RawMessage, error) {
		select {
		case result := <-done:
			return result.result, result.err
		case <-waitContext.Done():
			return nil, context.Cause(waitContext)
		}
	}), nil
}

func (a *echoAttachment) InvokeService(ctx context.Context, call chord.ServiceCall, publish chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	encoded, err := a.observe(call)
	if err != nil {
		return nil, err
	}
	if a.isCounterCall(call) {
		return a.counter.Invoke(ctx, call, publish)
	}
	return a.run(ctx, call, encoded)
}

func (a *echoAttachment) isCounterCall(call chord.ServiceCall) bool {
	return call.ServiceId == "interop.counter" || strings.HasPrefix(call.ServiceId, "$chord.")
}

func (a *echoAttachment) run(ctx context.Context, call chord.ServiceCall, encoded []byte) (json.RawMessage, error) {
	switch call.Member {
	case "echo":
		return json.Marshal(map[string]any{"echo": json.RawMessage(encoded)})
	case "undefined":
		return nil, nil
	case "null":
		return json.RawMessage("null"), nil
	case "fail":
		return nil, &chord.RemoteServiceError{Code: chord.ErrServiceMemberNotFound, Message: "no such member"}
	case "secret":
		return nil, errors.New("secret detail")
	case "big":
		return json.Marshal(map[string]any{"data": strings.Repeat("x", 1<<20) + "é"})
	case "hang":
		<-ctx.Done()
		a.handle.log.add(map[string]any{"event": "abort", "member": call.Member})
		return nil, errors.New("aborted")
	}
	return nil, &chord.RemoteServiceError{Code: chord.ErrServiceMemberNotFound, Message: "unknown member " + call.Member}
}

// serverFixture is a running server under test and the events its host observed.
type serverFixture struct {
	path string
	log  *eventLog
	stop func()
}

// startGoServer runs the Go routing server on a Unix socket with the echo host.
func startGoServer(t *testing.T, handshakeTimeoutMs *float64) *serverFixture {
	t.Helper()
	return launchGoServer(t, filepath.Join(socketDirectory(t), "s.sock"), logicalServerID, handshakeTimeoutMs)
}

func launchGoServer(t *testing.T, path, serverID string, handshakeTimeoutMs *float64) *serverFixture {
	t.Helper()
	log := newEventLog()
	services := routingtest.CreateTestServerServices()
	sessions := map[string]bool{"session-1": true, "session-2": true}
	server, err := routing.CreateUnixServer(routing.ServerHost{
		ServerServices: services,
		ResolveSession: func(_ context.Context, id string) (routing.SessionMetadata, error) {
			if !sessions[id] {
				return nil, routing.NewSessionNotFoundError("Unknown session: " + id)
			}
			return routing.BasicSessionMetadata{ID: id}, nil
		},
		OpenSession: func(_ context.Context, metadata routing.SessionMetadata) (routing.RoutedSessionHandle, error) {
			log.add(map[string]any{"event": "open", "session": metadata.SessionID()})
			return &echoHandle{id: metadata.SessionID(), log: log}, nil
		},
	}, routing.UnixServerOptions{
		Path: path, ServerId: serverID, HandshakeTimeoutMs: handshakeTimeoutMs,
		OnError: func(err error) { log.add(map[string]any{"event": "error", "message": err.Error()}) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Start(); err != nil {
		t.Fatal(err)
	}
	log.add(map[string]any{"event": "ready"})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			if err := server.Close(); err != nil {
				t.Errorf("close Go server: %v", err)
			}
			log.add(map[string]any{"event": "closed"})
		})
	}
	t.Cleanup(stop)
	return &serverFixture{path: path, log: log, stop: stop}
}

// startNodeServer runs the pinned packages/server on a Unix socket with the same echo host (testdata/node-server.mjs).
func startNodeServer(t *testing.T, handshakeTimeoutMs *float64) *serverFixture {
	t.Helper()
	return launchNodeServer(t, filepath.Join(socketDirectory(t), "s.sock"), logicalServerID, handshakeTimeoutMs)
}

func launchNodeServer(t *testing.T, path, serverID string, handshakeTimeoutMs *float64) *serverFixture {
	t.Helper()
	handshake := ""
	if handshakeTimeoutMs != nil {
		handshake = fmt.Sprint(*handshakeTimeoutMs)
	}
	args := []string{path, handshake, serverID}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := nodeScript(ctx, t, "node-server.mjs", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	log := newEventLog()
	ready := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(nil, 1<<24)
		for scanner.Scan() {
			log.addRaw(scanner.Text())
			switch scanner.Text() {
			case `{"event":"ready"}`:
				close(ready)
			case `{"event":"closed"}`:
				close(closed)
			}
		}
		_, _ = io.Copy(io.Discard, stdout)
	}()
	select {
	case <-ready:
	case <-time.After(30 * time.Second):
		cancel()
		_ = cmd.Wait()
		t.Fatalf("Node server did not start: %s", stderr.String())
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = stdin.Close()
			select {
			case <-closed:
			case <-time.After(30 * time.Second):
				t.Errorf("Node server did not close: %s", stderr.String())
			}
			cancel()
			_ = cmd.Wait()
		})
	}
	t.Cleanup(stop)
	return &serverFixture{path: path, log: log, stop: stop}
}
