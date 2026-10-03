package routingtest_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

const serverID = "00000000-0000-4000-8000-000000000001"

func boundedContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// await waits for signal within the test's bound.
func await(t *testing.T, ctx context.Context, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("%s never happened", label)
	}
}

func mustNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func expectErrorText(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// packages/server/src/testing/host.ts:5-18: a Deferred settles once, with the first resolved value.
func TestDeferredKeepsTheFirstResolvedValue(t *testing.T) {
	deferred := routingtest.NewDeferred[string]()
	if _, ok := deferred.Value(); ok {
		t.Fatal("an unresolved Deferred reported a value")
	}
	deferred.Resolve("first")
	deferred.Resolve("second")
	await(t, boundedContext(t), deferred.Promise(), "resolution")
	if value, ok := deferred.Value(); !ok || value != "first" {
		t.Fatalf("Value = %q, %v; want first, true", value, ok)
	}
}

// packages/server/src/testing/host.ts:63-79: a service call is recorded before its scripted failure, a scripted result is used once, and the default result is {"ok":true}.
func TestTestHarnessScriptsServiceCalls(t *testing.T) {
	harness := routingtest.NewTestHarness(routing.BasicSessionMetadata{ID: "session-1"})
	first := chord.ServiceCall{ServiceId: "test.session", Member: "first", Args: []json.RawMessage{}}
	second := chord.ServiceCall{ServiceId: "test.session", Member: "second", Args: []json.RawMessage{}}
	failure := errors.New("scripted")
	harness.SetNextServiceError(failure)
	if _, err := harness.InvokeService(first); !errors.Is(err, failure) {
		t.Fatalf("scripted failure = %v", err)
	}
	if harness.NextServiceError() != nil {
		t.Fatal("the scripted failure was not consumed")
	}
	harness.SetNextServiceResult(nil)
	result, err := harness.InvokeService(second)
	mustNoError(t, err)
	if result != nil {
		t.Fatalf("omitted result = %s, want nil", result)
	}
	result, err = harness.InvokeService(second)
	mustNoError(t, err)
	if string(result) != `{"ok":true}` {
		t.Fatalf("default result = %s", result)
	}
	calls := harness.ServiceCalls()
	if got := []string{calls[0].Member, calls[1].Member, calls[2].Member}; !slices.Equal(got, []string{"first", "second", "second"}) {
		t.Fatalf("recorded calls = %v", got)
	}
}

// packages/server/src/testing/host.ts:63-79: a gated call is recorded and waits at the gate until it is released.
func TestTestHarnessGatesTheNextServiceCall(t *testing.T) {
	ctx := boundedContext(t)
	harness := routingtest.NewTestHarness(routing.BasicSessionMetadata{ID: "session-1"})
	gate := harness.GateNextServiceCall()
	done := make(chan error, 1)
	go func() {
		_, err := harness.InvokeService(chord.ServiceCall{ServiceId: "test.session", Member: "run", Args: []json.RawMessage{}})
		done <- err
	}()
	await(t, ctx, gate.Entered.Promise(), "gate entry")
	if len(harness.ServiceCalls()) != 1 {
		t.Fatal("the gated call was not recorded before the gate")
	}
	select {
	case <-done:
		t.Fatal("the gated call finished before its release")
	default:
	}
	gate.Release.Resolve(struct{}{})
	mustNoError(t, <-done)
}

// packages/server/src/testing/host.ts:45-61: a failed release counts the attempt and keeps the attachment; a successful release is idempotent.
func TestTestHarnessAttachmentRelease(t *testing.T) {
	ctx := boundedContext(t)
	harness := routingtest.NewTestHarness(routing.BasicSessionMetadata{ID: "session-1"})
	attachment, err := harness.AttachClient(ctx)
	mustNoError(t, err)
	failure := errors.New("release failed")
	harness.SetFailAttachmentRelease(failure)
	if err := attachment.Release(ctx); !errors.Is(err, failure) {
		t.Fatalf("failed release = %v", err)
	}
	if harness.AttachedClients() != 1 || harness.AttachmentReleaseCount() != 1 {
		t.Fatalf("after a failed release: attached %d, releases %d", harness.AttachedClients(), harness.AttachmentReleaseCount())
	}
	harness.SetFailAttachmentRelease(nil)
	mustNoError(t, attachment.Release(ctx))
	mustNoError(t, attachment.Release(ctx))
	if harness.AttachedClients() != 0 || harness.AttachmentReleaseCount() != 2 {
		t.Fatalf("after release: attached %d, releases %d", harness.AttachedClients(), harness.AttachmentReleaseCount())
	}
}

// packages/server/src/testing/host.ts:81-100: a scripted close failure is returned once and leaves the harness running; the next close resolves Closed and terminates without an error, and a later Terminate does not replace that result.
func TestTestHarnessCloseAndTermination(t *testing.T) {
	ctx := boundedContext(t)
	harness := routingtest.NewTestHarness(routing.BasicSessionMetadata{ID: "session-1"})
	failure := errors.New("close failed")
	harness.SetFailClose(failure)
	if err := harness.Close(ctx); !errors.Is(err, failure) {
		t.Fatalf("scripted close = %v", err)
	}
	select {
	case <-harness.Terminated():
		t.Fatal("a failed close terminated the harness")
	case <-harness.Closed().Promise():
		t.Fatal("a failed close resolved Closed")
	default:
	}
	mustNoError(t, harness.Close(ctx))
	await(t, ctx, harness.Closed().Promise(), "Closed")
	await(t, ctx, harness.Terminated(), "termination")
	harness.Terminate(errors.New("late"))
	if harness.TerminalError() != nil || harness.CloseCount() != 2 {
		t.Fatalf("terminal error %v, closes %d", harness.TerminalError(), harness.CloseCount())
	}

	crashed := routingtest.NewTestHarness(routing.BasicSessionMetadata{ID: "session-2"})
	crash := errors.New("worker crashed")
	crashed.Terminate(crash)
	await(t, ctx, crashed.Terminated(), "termination")
	if !errors.Is(crashed.TerminalError(), crash) {
		t.Fatalf("terminal error = %v", crashed.TerminalError())
	}
	if _, ok := crashed.Closed().Value(); ok {
		t.Fatal("termination resolved Closed")
	}
}

type recordingPresentation struct {
	mu       sync.Mutex
	attached []string
	detached int
}

func (presentation *recordingPresentation) AttachSession(_ context.Context, id string) error {
	presentation.mu.Lock()
	defer presentation.mu.Unlock()
	presentation.attached = append(presentation.attached, id)
	return nil
}

func (presentation *recordingPresentation) DetachSession(context.Context) error {
	presentation.mu.Lock()
	defer presentation.mu.Unlock()
	presentation.detached++
	return nil
}

func (*recordingPresentation) PrepareSessionRemoval(context.Context, string) error { return nil }

// packages/server/src/testing/host.ts:115-145: the server services accept only attach with one string argument and detach with none, on the uninstanced pi.session-management service.
func TestCreateTestServerServicesRoutesOnlyAttachAndDetach(t *testing.T) {
	ctx := boundedContext(t)
	presentation := &recordingPresentation{}
	attachment, err := routingtest.CreateTestServerServices().AttachClient(ctx, presentation)
	mustNoError(t, err)
	call := func(member string, args ...string) chord.ServiceCall {
		encoded := []json.RawMessage{}
		for _, arg := range args {
			encoded = append(encoded, json.RawMessage(arg))
		}
		return chord.ServiceCall{ServiceId: "pi.session-management", Member: member, Args: encoded}
	}
	result, err := attachment.InvokeService(ctx, call("attach", `"session-1"`), nil)
	mustNoError(t, err)
	if string(result) != "null" {
		t.Fatalf("attach result = %s, want null", result)
	}
	result, err = attachment.InvokeService(ctx, call("detach"), nil)
	mustNoError(t, err)
	if string(result) != "null" {
		t.Fatalf("detach result = %s, want null", result)
	}
	_, err = attachment.InvokeService(ctx, call("attach", "null"), nil)
	expectErrorText(t, err, "Unsupported test server service pi.session-management.attach")
	_, err = attachment.InvokeService(ctx, call("detach", `"session-1"`), nil)
	expectErrorText(t, err, "Unsupported test server service pi.session-management.detach")
	instanced := call("attach", `"session-1"`)
	instanced.Instance = &chord.ServiceInstanceAddress{}
	_, err = attachment.InvokeService(ctx, instanced, nil)
	expectErrorText(t, err, "Unsupported test server service pi.session-management.attach")
	_, err = attachment.InvokeService(ctx, chord.ServiceCall{ServiceId: "pi.session-directory", Member: "list", Args: []json.RawMessage{}}, nil)
	expectErrorText(t, err, "Unsupported test server service pi.session-directory.list")
	mustNoError(t, attachment.Release(ctx))
	if !slices.Equal(presentation.attached, []string{"session-1"}) || presentation.detached != 1 {
		t.Fatalf("presentation saw attach %v, detach %d", presentation.attached, presentation.detached)
	}
}

// packages/server/src/testing/host.ts:147-204: seeded Sessions resolve and open; an unseeded Session, a scripted open failure, and a missing harness fail with upstream's messages.
func TestTestServerHostOpensSeededSessions(t *testing.T) {
	ctx := boundedContext(t)
	host := routingtest.NewTestServerHost()
	_, err := host.ResolveSession(ctx, "session-1")
	var notFound *routing.SessionNotFoundError
	if !errors.As(err, &notFound) || err.Error() != "Unknown session: session-1" {
		t.Fatalf("unknown Session = %v", err)
	}
	if metadata := host.Seed(nil); metadata.SessionID() != "session-1" {
		t.Fatalf("default seed = %q", metadata.SessionID())
	}
	host.Seed(new("session-2"))
	if len(host.Sessions()) != 2 {
		t.Fatalf("sessions = %v", host.Sessions())
	}
	metadata, err := host.ResolveSession(ctx, "session-2")
	mustNoError(t, err)

	if _, err := host.OpenSession(ctx, routing.BasicSessionMetadata{ID: "missing"}); !errors.As(err, &notFound) || err.Error() != "Unknown session: missing" {
		t.Fatalf("unseeded open = %v", err)
	}
	failure := errors.New("open failed")
	host.SetNextOpenSessionError(failure)
	if _, err := host.OpenSession(ctx, metadata); !errors.Is(err, failure) {
		t.Fatalf("scripted open = %v", err)
	}
	_, err = host.LatestHarness("session-2")
	expectErrorText(t, err, "No harness for session-2")

	closeFailure := errors.New("close failed")
	host.SetNextHarnessCloseError(closeFailure)
	opened, err := host.OpenSession(ctx, metadata)
	mustNoError(t, err)
	latest, err := host.LatestHarness("session-2")
	mustNoError(t, err)
	if opened != routing.RoutedSessionHandle(latest) || latest.Metadata() != metadata || !errors.Is(latest.FailClose(), closeFailure) {
		t.Fatalf("opened harness %#v, latest %#v", opened, latest)
	}
	if host.NextHarnessCloseError() != nil || host.OpenSessionCount() != 3 || len(host.Harnesses()["session-2"]) != 1 {
		t.Fatalf("close error %v, opens %d, harnesses %v", host.NextHarnessCloseError(), host.OpenSessionCount(), host.Harnesses())
	}
}

// packages/server/src/testing/host.ts:162-169 and 193-197: a gated open is counted when it reaches the gate and returns after its release.
func TestTestServerHostGatesTheNextOpen(t *testing.T) {
	ctx := boundedContext(t)
	host := routingtest.NewTestServerHost()
	metadata := host.Seed(nil)
	gate := host.GateNextOpenSession()
	done := make(chan error, 1)
	go func() {
		_, err := host.OpenSession(ctx, metadata)
		done <- err
	}()
	await(t, ctx, gate.Entered.Promise(), "gate entry")
	if host.OpenSessionCount() != 1 || len(host.Harnesses()) != 0 {
		t.Fatalf("at the gate: opens %d, harnesses %d", host.OpenSessionCount(), len(host.Harnesses()))
	}
	gate.Release.Resolve(struct{}{})
	mustNoError(t, <-done)
	if len(host.Harnesses()["session-1"]) != 1 {
		t.Fatal("the released open created no harness")
	}
}

// fakeChannel records the frames a ProtocolTestClient sends.
type fakeChannel struct {
	mu     sync.Mutex
	chunks [][]byte
	closed int
	fail   error
}

func (channel *fakeChannel) Send(chunk []byte) error {
	channel.mu.Lock()
	defer channel.mu.Unlock()
	if channel.fail != nil {
		return channel.fail
	}
	channel.chunks = append(channel.chunks, chunk)
	return nil
}

func (channel *fakeChannel) SendFragmented(chunk []byte, splitAt int) error {
	channel.mu.Lock()
	defer channel.mu.Unlock()
	channel.chunks = append(channel.chunks, chunk[:splitAt], chunk[splitAt:])
	return nil
}

func (channel *fakeChannel) Close() error {
	channel.mu.Lock()
	defer channel.mu.Unlock()
	channel.closed++
	return nil
}

// sent decodes every client message sent so far.
func (channel *fakeChannel) sent(t *testing.T) []protocol.ClientMessage {
	t.Helper()
	decoder, err := protocol.NewClientMessageDecoder(protocol.FrameDecoderOptions{})
	mustNoError(t, err)
	channel.mu.Lock()
	defer channel.mu.Unlock()
	var messages []protocol.ClientMessage
	for _, chunk := range channel.chunks {
		decoded, err := decoder.Push(chunk)
		mustNoError(t, err)
		messages = append(messages, decoded...)
	}
	return messages
}

func serverFrame(t *testing.T, message protocol.ServerMessage) []byte {
	t.Helper()
	frame, err := protocol.EncodeServerMessage(message, protocol.FrameDecoderOptions{})
	mustNoError(t, err)
	return frame
}

func responseTo(id string) func(protocol.ServerMessage) bool {
	return func(message protocol.ServerMessage) bool {
		response, ok := message.(protocol.ResponseEnvelope)
		return ok && response.Id == id
	}
}

// packages/server/src/testing/client.ts:96-109 and 139-149: waits match logged and later messages, a failure rejects only the pending waiters, and a closed client rejects a wait with no logged match.
func TestProtocolTestClientWaitsForMessages(t *testing.T) {
	ctx := boundedContext(t)
	client := routingtest.NewProtocolTestClient(&fakeChannel{})
	client.Receive(append(serverFrame(t, protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: serverID}), serverFrame(t, protocol.ResponseEnvelope{Id: "a", Ok: true, HasResult: true, Result: true})...))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := client.NextFrom(canceled, 1, func(message protocol.ServerMessage) bool { _, ok := message.(protocol.ServerHello); return ok }); !errors.Is(err, context.Canceled) {
		t.Fatalf("NextFrom past the only hello = %v, want a pending wait ended by its context", err)
	}
	if routingtest.HasPendingWaiter(client) {
		t.Fatal("an ended wait stayed registered")
	}
	if message, err := client.NextFrom(ctx, -1, responseTo("a")); err != nil || message.(protocol.ResponseEnvelope).Id != "a" {
		t.Fatalf("NextFrom(-1) = %#v, %v", message, err)
	}

	pending := make(chan error, 1)
	go func() {
		_, err := client.Next(ctx, responseTo("b"))
		pending <- err
	}()
	waitForWaiter(t, client)
	failure := errors.New("socket failed")
	client.Fail(failure)
	if err := <-pending; !errors.Is(err, failure) {
		t.Fatalf("failed waiter = %v", err)
	}
	later := make(chan protocol.ServerMessage, 1)
	go func() {
		message, _ := client.Next(ctx, responseTo("b"))
		later <- message
	}()
	waitForWaiter(t, client)
	client.Receive(serverFrame(t, protocol.ResponseEnvelope{Id: "b", Ok: true}))
	if message := <-later; message == nil {
		t.Fatal("a wait after Fail did not receive its message")
	}

	go func() {
		_, err := client.Next(ctx, responseTo("c"))
		pending <- err
	}()
	waitForWaiter(t, client)
	client.MarkClosed()
	expectErrorText(t, <-pending, "Wire connection closed")
	mustNoError(t, client.WaitForClose(ctx))
	_, err := client.Next(ctx, responseTo("c"))
	expectErrorText(t, err, "Wire client is closed")
	if _, err := client.Next(ctx, responseTo("a")); err != nil {
		t.Fatalf("a closed client rejected a logged match: %v", err)
	}
	if !client.Closed() || len(client.Messages()) != 3 {
		t.Fatalf("closed %v, messages %d", client.Closed(), len(client.Messages()))
	}
}

// waitForWaiter returns once a goroutine's wait is registered, so the message or failure that follows reaches a pending wait rather than the log.
func waitForWaiter(t *testing.T, client *routingtest.ProtocolTestClient) {
	t.Helper()
	if !waiterRegistered(t.Context(), client) {
		t.Fatal("the wait never registered")
	}
}

// waiterRegistered polls until a wait is registered on client and reports false when ctx ends first or the 30-second bound passes. It never calls t, so a responder goroutine may use it.
func waiterRegistered(ctx context.Context, client *routingtest.ProtocolTestClient) bool {
	deadline := time.Now().Add(30 * time.Second)
	for !routingtest.HasPendingWaiter(client) {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	return true
}

// packages/server/src/testing/client.ts:115-137: a decode failure fails the pending waiters without closing the client.
func TestProtocolTestClientFailsWaitersOnUndecodableFrames(t *testing.T) {
	ctx := boundedContext(t)
	client := routingtest.NewProtocolTestClient(&fakeChannel{})
	pending := make(chan error, 1)
	go func() {
		_, err := client.Next(ctx, responseTo("a"))
		pending <- err
	}()
	waitForWaiter(t, client)
	frame, err := protocol.EncodeFrame([]byte{0xff})
	mustNoError(t, err)
	client.Receive(frame)
	var invalid *protocol.ProtocolValidationError
	if err := <-pending; !errors.As(err, &invalid) {
		t.Fatalf("decode failure = %v", err)
	}
	if client.Closed() {
		t.Fatal("a decode failure closed the client")
	}
}

// packages/server/src/testing/client.ts:51-82: request IDs advance only when omitted, attach sends one string argument, and Session requests target the current attachment or "missing-attachment".
func TestProtocolTestClientRequests(t *testing.T) {
	ctx := boundedContext(t)
	channel := &fakeChannel{}
	client := routingtest.NewProtocolTestClient(channel)
	// The frame is encoded on the test goroutine: a responder goroutine must not call t.Fatal.
	respond := func(id string) {
		frame := serverFrame(t, protocol.ResponseEnvelope{Id: id, Ok: true})
		go func() {
			if waiterRegistered(ctx, client) {
				client.Receive(frame)
			}
		}()
	}
	call := chord.ServiceCall{ServiceId: "test.session", Member: "run", Args: []json.RawMessage{}}

	respond("request-1")
	_, err := client.Attach(ctx, serverID, "session-1")
	mustNoError(t, err)
	respond("explicit")
	_, err = client.RequestSessionService(ctx, serverID, "session-1", call, new("explicit"))
	mustNoError(t, err)
	client.Receive(serverFrame(t, protocol.AttachmentEnvelope{Attachment: &protocol.SessionTarget{ServerId: serverID, SessionId: "session-1", AttachmentId: "attachment-1"}}))
	respond("request-2")
	_, err = client.RequestSessionService(ctx, serverID, "session-1", call, nil)
	mustNoError(t, err)
	respond("request-3")
	_, err = client.RequestSessionService(ctx, serverID, "session-2", call, nil)
	mustNoError(t, err)
	client.Receive(serverFrame(t, protocol.AttachmentEnvelope{}))
	respond("request-4")
	_, err = client.RequestSessionService(ctx, serverID, "session-1", call, nil)
	mustNoError(t, err)

	type sentRequest struct {
		id     string
		target protocol.RpcTarget
	}
	var got []sentRequest
	for _, message := range channel.sent(t) {
		request := message.(protocol.RequestEnvelope)
		got = append(got, sentRequest{request.Id, request.Target})
	}
	want := []sentRequest{
		{"request-1", protocol.ServerTarget{ServerId: serverID}},
		{"explicit", protocol.SessionTarget{ServerId: serverID, SessionId: "session-1", AttachmentId: "missing-attachment"}},
		{"request-2", protocol.SessionTarget{ServerId: serverID, SessionId: "session-1", AttachmentId: "attachment-1"}},
		{"request-3", protocol.SessionTarget{ServerId: serverID, SessionId: "session-2", AttachmentId: "missing-attachment"}},
		{"request-4", protocol.SessionTarget{ServerId: serverID, SessionId: "session-1", AttachmentId: "missing-attachment"}},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("requests = %#v\nwant %#v", got, want)
	}
	attach := channel.sent(t)[0].(protocol.RequestEnvelope)
	encoded, err := protocol.ToJSON(attach.Call)
	mustNoError(t, err)
	if string(encoded) != `{"serviceId":"pi.session-management","member":"attach","args":["session-1"]}` {
		t.Fatalf("attach call = %s", encoded)
	}
}

// packages/server/src/testing/client.ts:45-49 and 84-94: hello defaults to the current protocol version, a send failure fails the request, and fragmented sends split the encoded frame.
func TestProtocolTestClientSends(t *testing.T) {
	ctx := boundedContext(t)
	channel := &fakeChannel{}
	client := routingtest.NewProtocolTestClient(channel)
	helloError := serverFrame(t, protocol.ServerHelloError{Error: protocol.ProtocolError{Code: "version", Message: "unsupported"}})
	go func() {
		if waiterRegistered(ctx, client) {
			client.Receive(helloError)
		}
	}()
	reply, err := client.Hello(ctx, nil)
	mustNoError(t, err)
	if _, ok := reply.(protocol.ServerHelloError); !ok {
		t.Fatalf("hello reply = %#v", reply)
	}
	mustNoError(t, client.SendFragmentedMessage(protocol.ClientHello{Version: 1}, 3))
	if hello := channel.sent(t); len(hello) != 2 || hello[0] != (protocol.ClientHello{Version: protocol.ProtocolVersion}) || hello[1] != (protocol.ClientHello{Version: 1}) {
		t.Fatalf("sent = %#v", hello)
	}
	if size := len(channel.chunks[1]); size != 3 {
		t.Fatalf("first fragment = %d bytes, want 3", size)
	}
	failure := errors.New("send failed")
	channel.fail = failure
	if _, err := client.RequestService(ctx, protocol.ServerTarget{ServerId: serverID}, chord.ServiceCall{ServiceId: "s", Member: "m", Args: []json.RawMessage{}}, nil); !errors.Is(err, failure) {
		t.Fatalf("request with a failed send = %v", err)
	}
	if routingtest.HasPendingWaiter(client) {
		t.Fatal("a failed send left its response waiter registered")
	}
	mustNoError(t, client.Close())
	if channel.closed != 1 {
		t.Fatalf("channel closes = %d", channel.closed)
	}
}

// packages/server/src/testing/server.ts:16-28: the test server defaults its identity and host, and an explicit identity reaches server validation.
func TestCreateTestServer(t *testing.T) {
	created, err := routingtest.CreateTestServer(routingtest.TestServerOptions{Listeners: []routing.ServerListener{}})
	mustNoError(t, err)
	t.Cleanup(func() { _ = created.Server.Close() })
	if created.Server.ServerId != "00000000-0000-4000-8000-000000000001" {
		t.Fatalf("default server ID = %q", created.Server.ServerId)
	}
	if _, err := created.Host.ResolveSession(boundedContext(t), "session-1"); err == nil || err.Error() != "Unknown session: session-1" {
		t.Fatalf("default host resolve = %v", err)
	}
	host := routingtest.NewTestServerHost().ServerHost()
	explicit, err := routingtest.CreateTestServer(routingtest.TestServerOptions{Listeners: []routing.ServerListener{}, Host: &host, ServerId: new("00000000-0000-4000-8000-000000000002")})
	mustNoError(t, err)
	t.Cleanup(func() { _ = explicit.Server.Close() })
	if explicit.Server.ServerId != "00000000-0000-4000-8000-000000000002" {
		t.Fatalf("explicit server ID = %q", explicit.Server.ServerId)
	}
	_, err = routingtest.CreateTestServer(routingtest.TestServerOptions{Listeners: []routing.ServerListener{}, ServerId: new("")})
	expectErrorText(t, err, "serverId must be a canonical lowercase UUIDv4")
	_, err = routingtest.CreateTestServer(routingtest.TestServerOptions{})
	expectErrorText(t, err, "Server listeners must be an array")
}
