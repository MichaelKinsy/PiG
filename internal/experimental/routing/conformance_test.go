package routing_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

const otherServerID = "00000000-0000-4000-8000-000000000002"

func createServer(t *testing.T, host routing.ServerHost, options ...func(*routing.ServerOptions)) *routing.Server {
	t.Helper()
	serverOptions := routing.ServerOptions{Listeners: []routing.ServerListener{}, ServerId: testServerID}
	for _, option := range options {
		option(&serverOptions)
	}
	server, err := routing.NewServer(host, serverOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func toPlain(value any) any {
	switch typed := value.(type) {
	case protocol.Object:
		plain := make(map[string]any, len(typed))
		for _, property := range typed {
			plain[property.Key.(string)] = toPlain(property.Value)
		}
		return plain
	case []any:
		plain := make([]any, len(typed))
		for i, item := range typed {
			plain[i] = toPlain(item)
		}
		return plain
	}
	return value
}

func callJSON(t *testing.T, calls []chord.ServiceCall) []string {
	t.Helper()
	encoded := make([]string, len(calls))
	for i, call := range calls {
		data, err := json.Marshal(call)
		if err != nil {
			t.Fatal(err)
		}
		encoded[i] = string(data)
	}
	return encoded
}

func sessionCallJSON(member string, args ...string) string {
	quoted := "["
	for i, arg := range args {
		if i > 0 {
			quoted += ","
		}
		data, _ := json.Marshal(arg)
		quoted += string(data)
	}
	return `{"serviceId":"test.session","member":"` + member + `","args":` + quoted + `]}`
}

func nextAttachment(client *wireClient, present bool) protocol.AttachmentEnvelope {
	client.t.Helper()
	return client.next(func(message protocol.ServerMessage) bool {
		envelope, ok := message.(protocol.AttachmentEnvelope)
		return ok && (envelope.Attachment != nil) == present
	}).(protocol.AttachmentEnvelope)
}

// countingServerServices is the opaque server service host of conformance.test.ts:140-160.
type countingServerServices struct {
	mu       sync.Mutex
	released int
}

func (services *countingServerServices) AttachClient(_ context.Context, presentation routing.RoutedServerPresentation) (routing.RoutedServerServiceAttachment, error) {
	return &countingServerAttachment{services: services, presentation: presentation}, nil
}

func (services *countingServerServices) releases() int {
	services.mu.Lock()
	defer services.mu.Unlock()
	return services.released
}

type countingServerAttachment struct {
	services     *countingServerServices
	presentation routing.RoutedServerPresentation
}

func (attachment *countingServerAttachment) InvokeService(ctx context.Context, call chord.ServiceCall, _ chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	if call.ServiceId != "pi.session-management" {
		return nil, errors.New("Unexpected service")
	}
	if call.Member == "attach" && len(call.Args) > 0 {
		var id string
		if err := json.Unmarshal(call.Args[0], &id); err == nil {
			return json.RawMessage("null"), attachment.presentation.AttachSession(ctx, id)
		}
	}
	if call.Member == "detach" {
		return json.RawMessage("null"), attachment.presentation.DetachSession(ctx)
	}
	return nil, errors.New("Unexpected service member")
}

func (attachment *countingServerAttachment) Release(context.Context) error {
	attachment.services.mu.Lock()
	defer attachment.services.mu.Unlock()
	attachment.services.released++
	return nil
}

func TestSessionProtocol(t *testing.T) {
	// upstream: packages/server/test/conformance.test.ts:81 "handshake identifies the logical server without listing sessions"
	t.Run("handshake identifies the logical server without listing sessions", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		client := connect(t, createServer(t, host.ServerHost()))
		reply, ok := client.hello(protocol.ProtocolVersion).(protocol.ServerHello)
		if !ok || reply.ServerId != testServerID {
			t.Fatalf("hello = %#v", reply)
		}
		if len(host.Harnesses()) != 0 {
			t.Fatal("the handshake opened a Session")
		}
	})

	// upstream: packages/server/test/conformance.test.ts:90 "rejects a semantically invalid service call after envelope decoding"
	t.Run("rejects a semantically invalid service call after envelope decoding", func(t *testing.T) {
		client := connect(t, createServer(t, routingtest.NewTestServerHost().ServerHost()))
		client.hello(protocol.ProtocolVersion)
		client.sendMessage(protocol.RequestEnvelope{Id: "invalid-call", Target: protocol.ServerTarget{ServerId: testServerID}, Call: protocol.Object{{Key: "arbitrary", Value: true}}})
		response := client.next(func(message protocol.ServerMessage) bool {
			response, ok := message.(protocol.ResponseEnvelope)
			return ok && response.Id == "invalid-call"
		}).(protocol.ResponseEnvelope)
		expectFailure(t, response, "invalid_request")
	})

	// upstream: packages/server/test/conformance.test.ts:104 "attach passes concrete repository metadata to the Harness host"
	t.Run("attach passes concrete repository metadata to the Harness host", func(t *testing.T) {
		metadata := backendMetadata{BasicSessionMetadata: routing.BasicSessionMetadata{ID: "session-1"}, Path: "/sessions/session-1.jsonl", ModifiedAt: 2}
		var mu sync.Mutex
		var received routing.SessionMetadata
		host := routing.ServerHost{
			ServerServices: routingtest.CreateTestServerServices(),
			ResolveSession: func(context.Context, string) (routing.SessionMetadata, error) { return metadata, nil },
			OpenSession: func(_ context.Context, candidate routing.SessionMetadata) (routing.RoutedSessionHandle, error) {
				mu.Lock()
				received = candidate
				mu.Unlock()
				return &bareHandle{}, nil
			},
		}
		client := connect(t, createServer(t, host))
		client.hello(protocol.ProtocolVersion)
		expectOK(t, client.attach(testServerID, "session-1"))
		mu.Lock()
		defer mu.Unlock()
		if received != routing.SessionMetadata(metadata) {
			t.Fatalf("OpenSession received %#v, want the resolved metadata %#v", received, metadata)
		}
	})

	// upstream: packages/server/test/conformance.test.ts:130 "routes opaque server services and publishes attachment changes out of band"
	t.Run("routes opaque server services and publishes attachment changes out of band", func(t *testing.T) {
		backing := routingtest.NewTestServerHost()
		backing.Seed(new("session-1"))
		services := &countingServerServices{}
		host := routing.ServerHost{ServerServices: services, ResolveSession: backing.ResolveSession, OpenSession: backing.OpenSession}
		client := connect(t, createServer(t, host))
		client.hello(protocol.ProtocolVersion)
		expectOK(t, client.attach(testServerID, "session-1"))
		if attached := nextAttachment(client, true).Attachment; attached.SessionId != "session-1" || attached.AttachmentId == "" {
			t.Fatalf("attachment = %#v", attached)
		}
		if latestHarness(t, backing, "session-1").AttachedClients() != 1 {
			t.Fatal("the Session did not receive its attachment")
		}
		expectOK(t, client.requestService(protocol.ServerTarget{ServerId: testServerID}, "pi.session-management", "detach"))
		if nextAttachment(client, false).Attachment != nil {
			t.Fatal("detach did not publish a null attachment")
		}
		// conformance.test.ts:177 asserts the release synchronously once the detach response and its attachment envelope arrive, not by polling.
		if got := latestHarness(t, backing, "session-1").AttachedClients(); got != 0 {
			t.Fatalf("attached clients after detach = %d, want 0", got)
		}
		client.closeWire()
		pollUntil(t, "server service release count == 1", func() bool { return services.releases() == 1 })
	})

	// upstream: packages/server/test/conformance.test.ts:179 "permits multiple client attachments per Session"
	t.Run("permits multiple client attachments per Session", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		server := createServer(t, host.ServerHost())
		first, second := connect(t, server), connect(t, server)
		first.hello(protocol.ProtocolVersion)
		second.hello(protocol.ProtocolVersion)

		expectOK(t, first.attach(testServerID, "session-1"))
		expectOK(t, first.attach(testServerID, "session-1"))
		if got := latestHarness(t, host, "session-1").AttachedClients(); got != 1 {
			t.Fatalf("attached clients after a repeated attach = %d, want 1", got)
		}
		expectOK(t, second.attach(testServerID, "session-1"))
		if got := len(host.Harnesses()["session-1"]); got != 1 {
			t.Fatalf("harnesses = %d, want 1", got)
		}
		if got := latestHarness(t, host, "session-1").AttachedClients(); got != 2 {
			t.Fatalf("attached clients = %d, want 2", got)
		}
		first.closeWire()
		waitAttachedClients(t, latestHarness(t, host, "session-1"), 1)
		expectOK(t, second.attach(testServerID, "session-1"))
	})

	// upstream: packages/server/test/conformance.test.ts:199 "clears connection ownership when attachment release fails"
	t.Run("clears connection ownership when attachment release fails", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		var mu sync.Mutex
		var reported []error
		server := createServer(t, host.ServerHost(), func(options *routing.ServerOptions) {
			options.OnError = func(err error) {
				mu.Lock()
				reported = append(reported, err)
				mu.Unlock()
			}
		})
		first, second := connect(t, server), connect(t, server)
		first.hello(protocol.ProtocolVersion)
		second.hello(protocol.ProtocolVersion)
		first.attach(testServerID, "session-1")
		harness := latestHarness(t, host, "session-1")
		releaseError := errors.New("release failed")
		harness.SetFailAttachmentRelease(releaseError)

		first.closeWire()
		pollUntil(t, "attachment release attempted once", func() bool { return harness.AttachmentReleaseCount() == 1 })
		pollUntil(t, "release failure reported", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return slices.ContainsFunc(reported, func(err error) bool { return errors.Is(err, releaseError) })
		})
		harness.SetFailAttachmentRelease(nil)
		expectOK(t, second.attach(testServerID, "session-1"))
	})

	// upstream: packages/server/test/conformance.test.ts:220 "requires the requesting client to hold the targeted Session attachment"
	t.Run("requires the requesting client to hold the targeted Session attachment", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		host.Seed(new("session-2"))
		server := createServer(t, host.ServerHost())
		attached, unattached := connect(t, server), connect(t, server)
		attached.hello(protocol.ProtocolVersion)
		unattached.hello(protocol.ProtocolVersion)

		expectFailure(t, unattached.requestSessionService(testServerID, "session-1", "run"), "session_not_attached")
		attached.attach(testServerID, "session-1")
		expectFailure(t, attached.requestSessionService(testServerID, "session-2", "run"), "session_not_attached")
		response := attached.requestSessionService(testServerID, "session-1", "run", "Hello")
		expectOK(t, response)
		if !reflect.DeepEqual(toPlain(response.Result), map[string]any{"ok": true}) {
			t.Fatalf("result = %#v", response.Result)
		}
		if got, want := callJSON(t, latestHarness(t, host, "session-1").ServiceCalls()), []string{sessionCallJSON("run", "Hello")}; !slices.Equal(got, want) {
			t.Fatalf("service calls = %v, want %v", got, want)
		}
	})

	// upstream: packages/server/test/conformance.test.ts:243 "rejects a stale attachment route after switching Sessions"
	t.Run("rejects a stale attachment route after switching Sessions", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		host.Seed(new("session-2"))
		client := connect(t, createServer(t, host.ServerHost()))
		client.hello(protocol.ProtocolVersion)
		client.attach(testServerID, "session-1")
		firstAttachmentID := client.latestAttachmentID("session-1")
		client.attach(testServerID, "session-2")

		stale := client.requestService(protocol.SessionTarget{ServerId: testServerID, SessionId: "session-1", AttachmentId: firstAttachmentID}, "test.session", "run", "stale")
		expectFailure(t, stale, "session_not_attached")
		if calls := latestHarness(t, host, "session-1").ServiceCalls(); len(calls) != 0 {
			t.Fatalf("stale route reached the Session: %v", calls)
		}
	})

	// upstream: packages/server/test/conformance.test.ts:261 "preserves opaque service results and bounds adapter defects"
	t.Run("preserves opaque service results and bounds adapter defects", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		client := connect(t, createServer(t, host.ServerHost()))
		client.hello(protocol.ProtocolVersion)
		client.attach(testServerID, "session-1")
		harness := latestHarness(t, host, "session-1")
		harness.SetNextServiceResult(json.RawMessage(`{"accepted":false,"reason":"closed"}`))
		response := client.requestSessionService(testServerID, "session-1", "run")
		expectOK(t, response)
		if !reflect.DeepEqual(toPlain(response.Result), map[string]any{"accepted": false, "reason": "closed"}) {
			t.Fatalf("result = %#v", response.Result)
		}

		harness.SetNextServiceError(errors.New("private adapter detail"))
		failure := client.requestSessionService(testServerID, "session-1", "run")
		expectFailure(t, failure, "internal_error")
		if failure.Error.Message != "Internal server error" {
			t.Fatalf("adapter defect crossed the wire as %q", failure.Error.Message)
		}
	})

	// upstream: packages/server/test/conformance.test.ts:281 "admits concurrent service calls to the attached Session"
	t.Run("admits concurrent service calls to the attached Session", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		client := connect(t, createServer(t, host.ServerHost()))
		client.hello(protocol.ProtocolVersion)
		client.attach(testServerID, "session-1")
		harness := latestHarness(t, host, "session-1")
		gate := gateNextServiceCall(t, harness)
		first := make(chan protocol.ResponseEnvelope, 1)
		go func() {
			response, err := client.requestSessionServiceErr(testServerID, "session-1", "run", "first")
			if err != nil {
				t.Error(err)
			}
			first <- response
		}()
		<-gate.Entered.Promise()
		expectOK(t, client.requestSessionService(testServerID, "session-1", "run", "second"))
		if got, want := callJSON(t, harness.ServiceCalls()), []string{sessionCallJSON("run", "first"), sessionCallJSON("run", "second")}; !slices.Equal(got, want) {
			t.Fatalf("service calls = %v, want %v", got, want)
		}
		openGate(gate)
		expectOK(t, <-first)
	})

	// upstream: packages/server/test/conformance.test.ts:299 "keeps attachment demand until an accepted service call settles after disconnect"
	t.Run("keeps attachment demand until an accepted service call settles after disconnect", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		client := connect(t, createServer(t, host.ServerHost()))
		client.hello(protocol.ProtocolVersion)
		client.attach(testServerID, "session-1")
		harness := latestHarness(t, host, "session-1")
		gate := gateNextServiceCall(t, harness)
		calling := make(chan error, 1)
		go func() {
			_, err := client.requestSessionServiceErr(testServerID, "session-1", "run")
			calling <- err
		}()
		<-gate.Entered.Promise()

		client.closeWire()
		// The disconnect cleanup parks in the attachment release until the accepted call settles; wait for that state before asserting the demand is still held.
		pollUntil(t, "disconnect cleanup waiting on the accepted call", func() bool { return goroutinesIn(").releaseAttachment(") > 0 })
		if got := harness.AttachedClients(); got != 1 {
			t.Fatalf("attached clients while the call is in flight = %d, want 1", got)
		}
		openGate(gate)
		if err := <-calling; err == nil || !regexp.MustCompile("(?i)closed").MatchString(err.Error()) {
			t.Fatalf("disconnected call = %v, want a closed-wire failure", err)
		}
		waitAttachedClients(t, harness, 0)
	})

	// upstream: packages/server/test/conformance.test.ts:318 "rejects requests addressed to another server before repository access"
	t.Run("rejects requests addressed to another server before repository access", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		client := connect(t, createServer(t, host.ServerHost()))
		client.hello(protocol.ProtocolVersion)
		expectFailure(t, client.attach(otherServerID, "session-1"), "wrong_server")
		if len(host.Harnesses()) != 0 {
			t.Fatal("a request for another server opened a Session")
		}
	})

	// upstream: packages/server/test/conformance.test.ts:331 "reports an unknown session without creating a Harness"
	t.Run("reports an unknown session without creating a Harness", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		client := connect(t, createServer(t, host.ServerHost()))
		client.hello(protocol.ProtocolVersion)
		expectFailure(t, client.attach(testServerID, "missing"), "session_not_found")
		if len(host.Harnesses()) != 0 {
			t.Fatal("an unknown Session opened a Harness")
		}
	})

	// upstream: packages/server/test/conformance.test.ts:343 "rejects an ambiguous session ID without creating a Harness"
	t.Run("rejects an ambiguous session ID without creating a Harness", func(t *testing.T) {
		host := routing.ServerHost{
			ServerServices: routingtest.CreateTestServerServices(),
			ResolveSession: func(context.Context, string) (routing.SessionMetadata, error) {
				return nil, routing.NewSessionAmbiguousError()
			},
			OpenSession: func(context.Context, routing.SessionMetadata) (routing.RoutedSessionHandle, error) {
				return nil, errors.New("must not create a Harness for an ambiguous session")
			},
		}
		client := connect(t, createServer(t, host))
		client.hello(protocol.ProtocolVersion)
		expectFailure(t, client.attach(testServerID, "duplicate"), "session_ambiguous")
	})

	// upstream: packages/server/test/conformance.test.ts:363 "invalidates a terminated Harness handle and allows a later attach"
	t.Run("invalidates a terminated Harness handle and allows a later attach", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		client := connect(t, createServer(t, host.ServerHost()))
		client.hello(protocol.ProtocolVersion)
		client.attach(testServerID, "session-1")
		first := latestHarness(t, host, "session-1")

		first.Terminate(errors.New("worker crashed"))
		<-first.Terminated()
		waitAttachedClients(t, first, 0)
		if got := first.AttachmentReleaseCount(); got != 1 {
			t.Fatalf("attachment releases = %d, want 1", got)
		}
		expectOK(t, client.attach(testServerID, "session-1"))
		if got := len(host.Harnesses()["session-1"]); got != 2 {
			t.Fatalf("harnesses = %d, want 2", got)
		}
	})

	// upstream: packages/server/test/conformance.test.ts:380 "connection loss releases its attachment, while server shutdown closes the Harness"
	t.Run("connection loss releases its attachment, while server shutdown closes the Harness", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		server := createServer(t, host.ServerHost())
		client := connect(t, server)
		client.hello(protocol.ProtocolVersion)
		client.attach(testServerID, "session-1")
		harness := latestHarness(t, host, "session-1")

		client.closeWire()
		waitAttachedClients(t, harness, 0)
		if got := harness.CloseCount(); got != 0 {
			t.Fatalf("Harness closes after connection loss = %d, want 0", got)
		}
		if err := server.Close(); err != nil {
			t.Fatal(err)
		}
		if got := harness.CloseCount(); got != 1 {
			t.Fatalf("Harness closes after shutdown = %d, want 1", got)
		}
	})
}

// bareHandle is the minimal Harness handle of conformance.test.ts:117-123 and 410-423.
type bareHandle struct {
	terminated chan struct{}
	attach     func(context.Context) (routing.RoutedSessionAttachment, error)
}

func (handle *bareHandle) AttachClient(ctx context.Context) (routing.RoutedSessionAttachment, error) {
	if handle.attach != nil {
		return handle.attach(ctx)
	}
	return bareAttachment{}, nil
}
func (handle *bareHandle) Terminated() <-chan struct{} { return handle.terminated }
func (*bareHandle) TerminalError() error               { return errors.New("worker crashed") }
func (*bareHandle) Close(context.Context) error        { return nil }

type bareAttachment struct{ release func() }

func (bareAttachment) InvokeService(context.Context, chord.ServiceCall, chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	return nil, nil
}
func (attachment bareAttachment) Release(context.Context) error {
	if attachment.release != nil {
		attachment.release()
	}
	return nil
}

// routerIsDraining reports that the Session router's close has snapshotted its in-flight acquisitions and is waiting on them. Upstream's event loop orders that snapshot ahead of the gate release; Go schedules the two independently.
func routerIsDraining() bool { return goroutinesBlockedIn("SessionRouter[...]).closeInternal(") > 0 }

func TestRoutedSessionAcquisitionFailures(t *testing.T) {
	// upstream: packages/server/test/conformance.test.ts:398 "releases a lease acquired concurrently with Harness termination"
	t.Run("releases a lease acquired concurrently with Harness termination", func(t *testing.T) {
		metadata := routing.BasicSessionMetadata{ID: "session-1"}
		acquiring, continueAcquiring := make(chan struct{}), make(chan struct{})
		terminated := make(chan struct{})
		var mu sync.Mutex
		releaseCount := 0
		host := routing.ServerHost{
			ServerServices: routingtest.CreateTestServerServices(),
			ResolveSession: func(context.Context, string) (routing.SessionMetadata, error) { return metadata, nil },
			OpenSession: func(context.Context, routing.SessionMetadata) (routing.RoutedSessionHandle, error) {
				return &bareHandle{terminated: terminated, attach: func(context.Context) (routing.RoutedSessionAttachment, error) {
					close(acquiring)
					<-continueAcquiring
					return bareAttachment{release: func() { mu.Lock(); releaseCount++; mu.Unlock() }}, nil
				}}, nil
			},
		}
		client := connect(t, createServer(t, host))
		client.hello(protocol.ProtocolVersion)
		result := client.attachAsync(testServerID, "session-1")
		<-acquiring

		// The router watches the handle's termination on one goroutine; the lease acquired after that watcher has invalidated the Session must be released.
		pollUntil(t, "termination watcher running", func() bool { return goroutinesIn(").open.func1(") == 1 })
		close(terminated)
		pollUntil(t, "termination watcher finished", func() bool { return goroutinesIn(").open.func1(") == 0 })
		close(continueAcquiring)
		expectFailure(t, awaitAttach(t, result), "server_draining")
		mu.Lock()
		defer mu.Unlock()
		if releaseCount != 1 {
			t.Fatalf("lease releases = %d, want 1", releaseCount)
		}
	})

	// upstream: packages/server/test/conformance.test.ts:433 "shares a Harness creation failure, releases the Session, and allows a later retry"
	t.Run("shares a Harness creation failure, releases the Session, and allows a later retry", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		host.SetNextOpenSessionError(errors.New("Harness creation failed"))
		server := createServer(t, host.ServerHost())
		first, second := connect(t, server), connect(t, server)
		first.hello(protocol.ProtocolVersion)
		second.hello(protocol.ProtocolVersion)
		gate := gateNextOpenSession(t, host)

		firstAttach := first.attachAsync(testServerID, "session-1")
		<-gate.Entered.Promise()
		secondAttach := second.attachAsync(testServerID, "session-1")
		// Both attaches must be parked on the one pending open before it fails; upstream's single event loop orders the second request's join ahead of the gate release.
		pollUntil(t, "both attaches waiting on one acquisition", func() bool { return goroutinesIn(").acquire(") == 2 })
		openGate(gate)

		expectFailure(t, awaitAttach(t, firstAttach), "internal_error")
		expectFailure(t, awaitAttach(t, secondAttach), "internal_error")
		if got := host.OpenSessionCount(); got != 1 {
			t.Fatalf("OpenSession calls = %d, want 1", got)
		}
		expectOK(t, first.attach(testServerID, "session-1"))
		if got := host.OpenSessionCount(); got != 2 {
			t.Fatalf("OpenSession calls after retry = %d, want 2", got)
		}
		if got := len(host.Harnesses()["session-1"]); got != 1 {
			t.Fatalf("harnesses = %d, want 1", got)
		}
	})

	// upstream: packages/server/test/conformance.test.ts:459 "closes a Harness acquired while server shutdown is in progress"
	t.Run("closes a Harness acquired while server shutdown is in progress", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		server := createServer(t, host.ServerHost())
		client := connect(t, server)
		client.hello(protocol.ProtocolVersion)
		gate := gateNextOpenSession(t, host)
		attach := make(chan error, 1)
		go func() {
			_, err := client.requestServiceErr(protocol.ServerTarget{ServerId: testServerID}, "pi.session-management", "attach", "session-1")
			attach <- err
		}()
		<-gate.Entered.Promise()
		closing := make(chan error, 1)
		go func() { closing <- server.Close() }()
		pollUntil(t, "router draining in-flight acquisitions", routerIsDraining)
		openGate(gate)

		if err := <-closing; err != nil {
			t.Fatalf("Close = %v", err)
		}
		if err := <-attach; err == nil || !regexp.MustCompile("(?i)closed").MatchString(err.Error()) {
			t.Fatalf("attach = %v, want a closed-wire failure", err)
		}
		if got := latestHarness(t, host, "session-1").CloseCount(); got != 1 {
			t.Fatalf("Harness closes = %d, want 1", got)
		}
	})

	// upstream: packages/server/test/conformance.test.ts:476 "fails shutdown when an in-flight acquisition cannot release its Harness"
	t.Run("fails shutdown when an in-flight acquisition cannot release its Harness", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(new("session-1"))
		cleanupError := errors.New("close failed")
		host.SetNextHarnessCloseError(cleanupError)
		server := createServer(t, host.ServerHost())
		client := connect(t, server)
		client.hello(protocol.ProtocolVersion)
		gate := gateNextOpenSession(t, host)
		attach := make(chan error, 1)
		go func() {
			_, err := client.requestServiceErr(protocol.ServerTarget{ServerId: testServerID}, "pi.session-management", "attach", "session-1")
			attach <- err
		}()
		<-gate.Entered.Promise()

		closing := make(chan error, 1)
		go func() { closing <- server.Close() }()
		pollUntil(t, "router draining in-flight acquisitions", routerIsDraining)
		openGate(gate)

		pattern := regexp.MustCompile("Failed to close routed Sessions")
		if err := <-closing; err == nil || !pattern.MatchString(err.Error()) {
			t.Fatalf("Close = %v, want a routed Session close failure", err)
		}
		<-server.Closed()
		if err := server.ClosedError(); err == nil || !pattern.MatchString(err.Error()) {
			t.Fatalf("ClosedError = %v, want a routed Session close failure", err)
		}
		if err := <-attach; err == nil || !regexp.MustCompile("(?i)closed").MatchString(err.Error()) {
			t.Fatalf("attach = %v, want a closed-wire failure", err)
		}
		harness := latestHarness(t, host, "session-1")
		if got := harness.CloseCount(); got != 1 {
			t.Fatalf("Harness closes = %d, want 1", got)
		}
		// The failed close left the Session open; release it as upstream does.
		if err := harness.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

// backendMetadata is conformance.test.ts:105 BackendMetadata: a SessionMetadata with the application's own members.
type backendMetadata struct {
	routing.BasicSessionMetadata
	Path       string
	ModifiedAt int
}
