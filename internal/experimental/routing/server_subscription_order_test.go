package routing_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// publishingServices is a server service host whose attachment hands the test the request's update publisher.
type publishingServices struct {
	invoke func(context.Context, chord.ServiceCall, chord.ServiceUpdatePublisher) (json.RawMessage, error)
}

func (services publishingServices) AttachClient(context.Context, routing.RoutedServerPresentation) (routing.RoutedServerServiceAttachment, error) {
	return publishingAttachment(services), nil
}

type publishingAttachment struct {
	invoke func(context.Context, chord.ServiceCall, chord.ServiceUpdatePublisher) (json.RawMessage, error)
}

func (attachment publishingAttachment) InvokeService(ctx context.Context, call chord.ServiceCall, publish chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	return attachment.invoke(ctx, call, publish)
}
func (publishingAttachment) Release(context.Context) error { return nil }

// subscribingServer answers every subscribe call with subscriptionSnapshot after publishing updates for the new subscription, before the snapshot exists on the wire.
func subscribingServer(t *testing.T, onError func(error), updates ...chord.ServiceProviderUpdate) *wireClient {
	t.Helper()
	host := newTestServerHost()
	host.ServerServices = publishingServices{invoke: func(ctx context.Context, call chord.ServiceCall, publish chord.ServiceUpdatePublisher) (json.RawMessage, error) {
		var subscription string
		if err := json.Unmarshal(call.Args[0], &subscription); err != nil {
			return nil, err
		}
		for _, update := range updates {
			if err := publish(ctx, subscription, update); err != nil {
				return nil, err
			}
		}
		return json.RawMessage(subscriptionSnapshot), nil
	}}
	client := connect(t, createServer(t, host, func(options *routing.ServerOptions) { options.OnError = onError }))
	client.hello(protocol.ProtocolVersion)
	return client
}

func stateUpdate(member string, sequence int) chord.ServiceProviderUpdate {
	return chord.ServiceProviderUpdate{Type: chord.UpdateState, Member: member, Sequence: sequence, Ops: []chord.Op{{"s", []any{"revision"}, float64(sequence)}}}
}

func subscribeRequest(id, subscription string) protocol.RequestEnvelope {
	return serverCall(id, protocol.ServerTarget{ServerId: testServerID}, "$chord.service", "subscribe", subscription, "svc", "singleton")
}

// Pi: packages/server/src/server.ts:338-344 and :375-382. Updates the provider publishes for a subscription before its snapshot response are held, then sent in order after the response.
// mutation-checked: negating the condition `c.closedValue` at client.go:264 fails it.
func TestServerHoldsSubscriptionUpdatesUntilTheSnapshotResponse(t *testing.T) {
	t.Parallel()
	client := subscribingServer(t, func(error) {}, stateUpdate("state", 1), stateUpdate("state", 2))
	client.sendMessage(subscribeRequest("r1", "sub-1"))
	client.next(func(message protocol.ServerMessage) bool {
		update, ok := message.(protocol.ServiceEventEnvelope)
		return ok && toPlain(update.Update).(map[string]any)["sequence"] == float64(2)
	})
	var order []string
	for _, message := range client.Messages() {
		switch message := message.(type) {
		case protocol.ResponseEnvelope:
			if !message.Ok || message.Id != "r1" {
				t.Fatalf("response=%#v", message)
			}
			order = append(order, "response")
		case protocol.ServiceEventEnvelope:
			update := toPlain(message.Update).(map[string]any)
			if message.SubscriptionId != "sub-1" || update["member"] != "state" {
				t.Fatalf("update=%#v", message)
			}
			order = append(order, "update "+string(rune('0'+int(update["sequence"].(float64)))))
		}
	}
	if len(order) != 3 || order[0] != "response" || order[1] != "update 1" || order[2] != "update 2" {
		t.Fatalf("wire order=%v, want the response then updates 1 and 2", order)
	}
}

// Pi: server.ts:383-401. A failure after the response was sent cannot become a response: the server reports it and closes the connection.
// mutation-checked: negating the condition `c.closedValue` at client.go:264 fails it.
func TestServerClosesTheConnectionWhenAHeldUpdateFailsAfterTheResponse(t *testing.T) {
	t.Parallel()
	reported := make(chan error, 4)
	client := subscribingServer(t, func(err error) { reported <- err }, stateUpdate("missing", 1))
	client.sendMessage(subscribeRequest("r1", "sub-1"))
	if response := responseFor(client, "r1"); !response.Ok {
		t.Fatalf("response=%#v", response)
	}
	client.waitForClose()
	select {
	case err := <-reported:
		if err == nil {
			t.Fatal("reported a nil error")
		}
	case <-client.ctx.Done():
		t.Fatal("the failed update was not reported")
	}
	for _, message := range client.Messages() {
		if _, ok := message.(protocol.ServiceEventEnvelope); ok {
			t.Fatalf("an unencodable update reached the wire: %#v", message)
		}
	}
}
