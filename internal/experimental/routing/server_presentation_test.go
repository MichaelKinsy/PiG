package routing_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

// removalServices is a server service host whose attachment forwards `remove` to the presentation's PrepareSessionRemoval, as packages/coding-agent/src/experimental/services/server.ts:99 does before it deletes a Session's durable metadata.
type removalServices struct{}

func (removalServices) AttachClient(_ context.Context, presentation routing.RoutedServerPresentation) (routing.RoutedServerServiceAttachment, error) {
	return removalAttachment{presentation: presentation}, nil
}

type removalAttachment struct {
	presentation routing.RoutedServerPresentation
}

func (attachment removalAttachment) InvokeService(ctx context.Context, call chord.ServiceCall, _ chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	var id string
	if len(call.Args) != 1 || json.Unmarshal(call.Args[0], &id) != nil {
		return nil, errors.New("one string argument expected")
	}
	switch call.Member {
	case "attach":
		return json.RawMessage("null"), attachment.presentation.AttachSession(ctx, id)
	case "remove":
		return json.RawMessage("null"), attachment.presentation.PrepareSessionRemoval(ctx, id)
	}
	return nil, errors.New("unsupported member " + call.Member)
}

func (removalAttachment) Release(context.Context) error { return nil }

// upstream: packages/server/src/server.ts:278 (`prepareSessionRemoval: (sessionId, context) => this.sessions.removeSession(sessionId, context)`) and session-router.ts:72-89 removeSession: it releases every attachment of the hosted Session, closes its handle, and forgets it, so a later attach opens a fresh Harness; an unhosted Session ID is a no-op.
// mutation-checked: the mutant "PrepareSessionRemoval does not remove" fails it.
func TestServerPresentationPrepareSessionRemovalReleasesAndClosesTheHostedSession(t *testing.T) {
	t.Parallel()
	backing := routingtest.NewTestServerHost()
	backing.Seed(new("session-1"))
	host := routing.ServerHost{ServerServices: removalServices{}, ResolveSession: backing.ResolveSession, OpenSession: backing.OpenSession}
	client := connect(t, createServer(t, host))
	client.hello(protocol.ProtocolVersion)
	target := protocol.ServerTarget{ServerId: testServerID}

	expectOK(t, client.requestService(target, "pi.session-management", "attach", "session-1"))
	first := latestHarness(t, backing, "session-1")
	waitAttachedClients(t, first, 1)

	expectOK(t, client.requestService(target, "pi.session-management", "remove", "no-such-session"))
	if first.CloseCount() != 0 || first.AttachedClients() != 1 {
		t.Fatalf("removing an unhosted Session touched the hosted one: closes %d, attached %d", first.CloseCount(), first.AttachedClients())
	}

	expectOK(t, client.requestService(target, "pi.session-management", "remove", "session-1"))
	if first.CloseCount() != 1 || first.AttachmentReleaseCount() != 1 || first.AttachedClients() != 0 {
		t.Fatalf("removal: closes %d, releases %d, attached %d; want 1, 1, 0", first.CloseCount(), first.AttachmentReleaseCount(), first.AttachedClients())
	}

	expectOK(t, client.requestService(target, "pi.session-management", "attach", "session-1"))
	if backing.OpenSessionCount() != 2 {
		t.Fatalf("a later attach reused the removed Session's handle: %d opens, want 2", backing.OpenSessionCount())
	}
}

// plainHandle is a Session handle that never signals termination, so only removeSession can make the router forget it.
type plainHandle struct{ closes *int }

func (plainHandle) AttachClient(context.Context) (routing.RoutedSessionAttachment, error) {
	return removalAttachment{}, nil
}
func (plainHandle) Terminated() <-chan struct{} { return nil }
func (plainHandle) TerminalError() error        { return nil }
func (handle plainHandle) Close(context.Context) error {
	*handle.closes++
	return nil
}

// upstream: session-router.ts:72-89 removeSession deletes the hosted Session after closing it (`if (this.hostedSessions.get(sessionId) === hosted) this.hostedSessions.delete(sessionId)`), so the next attach opens a new handle even when the closed handle never reported termination.
// mutation-checked: the mutant "PrepareSessionRemoval does not remove" fails it.
func TestServerPresentationPrepareSessionRemovalForgetsAHandleThatNeverTerminates(t *testing.T) {
	t.Parallel()
	opens, closes := 0, 0
	host := routing.ServerHost{
		ServerServices: removalServices{},
		ResolveSession: func(_ context.Context, id string) (routing.SessionMetadata, error) {
			return routing.BasicSessionMetadata{ID: id}, nil
		},
		OpenSession: func(context.Context, routing.SessionMetadata) (routing.RoutedSessionHandle, error) {
			opens++
			return plainHandle{closes: &closes}, nil
		},
	}
	client := connect(t, createServer(t, host))
	client.hello(protocol.ProtocolVersion)
	target := protocol.ServerTarget{ServerId: testServerID}
	expectOK(t, client.requestService(target, "pi.session-management", "attach", "session-1"))
	expectOK(t, client.requestService(target, "pi.session-management", "remove", "session-1"))
	expectOK(t, client.requestService(target, "pi.session-management", "attach", "session-1"))
	if opens != 2 || closes != 1 {
		t.Fatalf("opens %d, closes %d; want 2 and 1", opens, closes)
	}
}

// capturingServices keeps the presentation the Server hands each server service endpoint, so a test can call its methods directly.
type capturingServices struct {
	mu           sync.Mutex
	presentation routing.RoutedServerPresentation
}

func (services *capturingServices) AttachClient(_ context.Context, presentation routing.RoutedServerPresentation) (routing.RoutedServerServiceAttachment, error) {
	services.mu.Lock()
	services.presentation = presentation
	services.mu.Unlock()
	return removalAttachment{presentation: presentation}, nil
}

func (services *capturingServices) captured() routing.RoutedServerPresentation {
	services.mu.Lock()
	defer services.mu.Unlock()
	return services.presentation
}

// upstream: packages/server/src/server.ts:270-280 (the presentation passed to serverServices.attachClient: attachSession and detachSession run the router's attachClient and detachClient for the connection, prepareSessionRemoval its removeSession) and types.ts RoutedServerPresentation.
// mutation-checked: the mutants "AttachSession does not attach" (fails at the harness lookup), "DetachSession does not detach" (the attachment count stays 1) and "PrepareSessionRemoval does not remove" fail it.
func TestServerPresentationMethodsDriveTheConnectionsAttachment(t *testing.T) {
	t.Parallel()
	ctx := waitContext(t)
	backing := routingtest.NewTestServerHost()
	backing.Seed(new("session-1"))
	services := &capturingServices{}
	host := routing.ServerHost{ServerServices: services, ResolveSession: backing.ResolveSession, OpenSession: backing.OpenSession}
	client := connect(t, createServer(t, host))
	client.hello(protocol.ProtocolVersion)
	presentation := services.captured()
	if presentation == nil {
		t.Fatal("the handshake gave the server services no presentation")
	}

	if err := presentation.AttachSession(ctx, "session-1"); err != nil {
		t.Fatal(err)
	}
	harness := latestHarness(t, backing, "session-1")
	waitAttachedClients(t, harness, 1)
	if attached := nextAttachment(client, true).Attachment; attached.SessionId != "session-1" {
		t.Fatalf("attachment = %#v", attached)
	}

	if err := presentation.DetachSession(ctx); err != nil {
		t.Fatal(err)
	}
	waitAttachedClients(t, harness, 0)
	if nextAttachment(client, false).Attachment != nil {
		t.Fatal("detach did not publish a null attachment")
	}
	if err := presentation.DetachSession(ctx); err != nil { // detaching a client without an attachment is a no-op
		t.Fatal(err)
	}

	if err := presentation.AttachSession(ctx, "session-1"); err != nil {
		t.Fatal(err)
	}
	waitAttachedClients(t, harness, 1)
	if err := presentation.PrepareSessionRemoval(ctx, "session-1"); err != nil {
		t.Fatal(err)
	}
	if harness.CloseCount() != 1 || harness.AttachedClients() != 0 {
		t.Fatalf("removal: closes %d, attached %d; want 1 and 0", harness.CloseCount(), harness.AttachedClients())
	}

	var unknown *routing.SessionNotFoundError
	if err := presentation.AttachSession(ctx, "no-such-session"); !errors.As(err, &unknown) {
		t.Fatalf("attaching an unknown Session = %v, want a SessionNotFoundError", err)
	}
}
