package routing_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// scriptedServices is a server service host whose attachment runs the test's function.
type scriptedServices struct {
	invoke func(context.Context, chord.ServiceCall) (json.RawMessage, error)
}

func (services scriptedServices) AttachClient(context.Context, routing.RoutedServerPresentation) (routing.RoutedServerServiceAttachment, error) {
	return scriptedAttachment(services), nil
}

type scriptedAttachment struct {
	invoke func(context.Context, chord.ServiceCall) (json.RawMessage, error)
}

func (attachment scriptedAttachment) InvokeService(ctx context.Context, call chord.ServiceCall, _ chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	return attachment.invoke(ctx, call)
}
func (scriptedAttachment) Release(context.Context) error { return nil }

func scriptedServer(t *testing.T, invoke func(context.Context, chord.ServiceCall) (json.RawMessage, error)) *wireClient {
	t.Helper()
	host := newTestServerHost()
	host.ServerServices = scriptedServices{invoke: invoke}
	client := connect(t, createServer(t, host))
	client.hello(protocol.ProtocolVersion)
	return client
}

func serverCall(id string, target protocol.RpcTarget, serviceID, member string, args ...any) protocol.RequestEnvelope {
	return protocol.RequestEnvelope{Id: id, Target: target, Call: protocol.Object{{Key: "serviceId", Value: serviceID}, {Key: "member", Value: member}, {Key: "args", Value: append([]any{}, args...)}}}
}

func responseFor(client *wireClient, id string) protocol.ResponseEnvelope {
	client.t.Helper()
	return client.next(func(message protocol.ServerMessage) bool {
		response, ok := message.(protocol.ResponseEnvelope)
		return ok && response.Id == id
	}).(protocol.ResponseEnvelope)
}

const subscriptionSnapshot = `{"serviceId":"svc","mode":"singleton","instances":[{"members":[{"name":"state","kind":"state","sequence":0,"ops":[["r",{"revision":0}]]}]}]}`

// Pi: packages/server/src/server.ts:248-282 handleCancel and the aborted request's response: a cancel for this server and the request's own target aborts it; the response is cancelled / "RPC request cancelled".
// mutation-checked: negating the condition `c.closedValue` at client.go:264 fails it.
func TestServerCancelAbortsOnlyTheMatchingRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		client := scriptedServer(t, func(ctx context.Context, call chord.ServiceCall) (json.RawMessage, error) {
			<-ctx.Done()
			return nil, context.Cause(ctx)
		})
		target := protocol.ServerTarget{ServerId: testServerID}
		client.sendMessage(serverCall("slow", target, "svc", "wait"))
		client.sendMessage(serverCall("other", target, "svc", "wait"))
		synctest.Wait()
		// A cancel addressed to another server, to the same id through another target, or to an unknown id changes nothing.
		client.sendMessage(protocol.CancelEnvelope{Id: "slow", Target: protocol.ServerTarget{ServerId: otherServerID}})
		client.sendMessage(protocol.CancelEnvelope{Id: "slow", Target: protocol.SessionTarget{ServerId: testServerID, SessionId: "s", AttachmentId: "a"}})
		client.sendMessage(protocol.CancelEnvelope{Id: "unknown", Target: target})
		synctest.Wait()
		for _, message := range client.Messages() {
			if response, ok := message.(protocol.ResponseEnvelope); ok {
				t.Fatalf("a non-matching cancel produced %#v", response)
			}
		}
		client.sendMessage(protocol.CancelEnvelope{Id: "slow", Target: target})
		synctest.Wait()
		var responses []protocol.ResponseEnvelope
		for _, message := range client.Messages() {
			if response, ok := message.(protocol.ResponseEnvelope); ok {
				responses = append(responses, response)
			}
		}
		if len(responses) != 1 || responses[0].Id != "slow" || responses[0].Ok || responses[0].Error == nil || responses[0].Error.Code != "cancelled" || responses[0].Error.Message != "RPC request cancelled" {
			t.Fatalf("responses=%#v, want only the cancelled slow request", responses)
		}
	})
}

// Pi: server.ts:393-396 a disconnect aborts every active request with "Client disconnected".
// mutation-checked: negating the condition `c.closedValue` at client.go:264 fails it.
func TestServerDisconnectCancelsActiveRequests(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	cause := make(chan string, 1)
	client := scriptedServer(t, func(ctx context.Context, call chord.ServiceCall) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		cause <- context.Cause(ctx).Error()
		return nil, context.Cause(ctx)
	})
	client.sendMessage(serverCall("slow", protocol.ServerTarget{ServerId: testServerID}, "svc", "wait"))
	<-started
	client.closeWire()
	if got := <-cause; got != "Client disconnected" {
		t.Fatalf("cancellation cause=%q", got)
	}
}

// Pi: server.ts:285-345 handleRequest subscription control: a repeated subscription id is a validation error, a subscribe without a snapshot is one too.
// mutation-checked: negating the condition `c.closedValue` at client.go:264 fails it.
func TestServerSubscriptionControlFailures(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	returnSnapshot := true
	client := scriptedServer(t, func(_ context.Context, call chord.ServiceCall) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		if returnSnapshot {
			return json.RawMessage(subscriptionSnapshot), nil
		}
		return nil, nil
	})
	target := protocol.ServerTarget{ServerId: testServerID}
	subscribe := func(id, subscription string) protocol.RequestEnvelope {
		return serverCall(id, target, "$chord.service", "subscribe", subscription, "svc", "singleton")
	}
	client.sendMessage(subscribe("r1", "sub-1"))
	if response := responseFor(client, "r1"); !response.Ok {
		t.Fatalf("first subscribe=%#v", response)
	}
	client.sendMessage(subscribe("r2", "sub-1"))
	response := responseFor(client, "r2")
	if response.Ok || response.Error == nil || response.Error.Code != "invalid_request" || response.Error.Message != "Duplicate service subscription sub-1" {
		t.Fatalf("duplicate subscribe=%#v", response)
	}
	// Unsubscribing frees the id.
	client.sendMessage(serverCall("r3", target, "$chord.service", "unsubscribe", "sub-1"))
	if response := responseFor(client, "r3"); !response.Ok {
		t.Fatalf("unsubscribe=%#v", response)
	}
	client.sendMessage(subscribe("r4", "sub-1"))
	if response := responseFor(client, "r4"); !response.Ok {
		t.Fatalf("resubscribe after unsubscribe=%#v", response)
	}
	mu.Lock()
	returnSnapshot = false
	mu.Unlock()
	client.sendMessage(subscribe("r5", "sub-2"))
	response = responseFor(client, "r5")
	if response.Ok || response.Error == nil || response.Error.Code != "invalid_request" || response.Error.Message != "Service subscription did not return a snapshot" {
		t.Fatalf("subscribe without snapshot=%#v", response)
	}
}
