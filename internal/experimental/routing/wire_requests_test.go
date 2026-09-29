package routing_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

var errWireClosed = errors.New("Wire connection closed")

// tryNext is next for callers that expect the wire to close: it returns the same error as upstream's rejected waiter.
func (client *wireClient) tryNext(match func(protocol.ServerMessage) bool) (protocol.ServerMessage, error) {
	client.t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		client.mu.Lock()
		for _, message := range client.messages {
			if match(message) {
				client.mu.Unlock()
				return message, nil
			}
		}
		closed, changed := client.closed, client.changed
		client.mu.Unlock()
		if closed {
			return nil, errWireClosed
		}
		select {
		case <-changed:
		case <-deadline:
			return nil, errors.New("no matching server message")
		}
	}
}

// closeWire is the WireChannel close of conformance.test.ts: idempotent, and it reports the close to the server handler once.
func (client *wireClient) closeWire() {
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return
	}
	client.closed = true
	client.wakeLocked()
	client.mu.Unlock()
	client.handler.OnClose()
}

func (client *wireClient) nextID() string {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.requests++
	return fmt.Sprintf("request-%d", client.requests)
}

// serviceRequest builds a request whose call is the given ordered members.
func serviceRequest(id string, target protocol.RpcTarget, serviceID, member string, args ...any) protocol.RequestEnvelope {
	if args == nil {
		args = []any{}
	}
	return protocol.RequestEnvelope{
		Id: id, Target: target,
		Call: protocol.Object{{Key: "serviceId", Value: serviceID}, {Key: "member", Value: member}, {Key: "args", Value: args}},
	}
}

// requestServiceErr sends one request and waits for its response, or for the wire to close first.
func (client *wireClient) requestServiceErr(target protocol.RpcTarget, serviceID, member string, args ...any) (protocol.ResponseEnvelope, error) {
	client.t.Helper()
	id := client.nextID()
	client.sendMessage(serviceRequest(id, target, serviceID, member, args...))
	message, err := client.tryNext(func(message protocol.ServerMessage) bool {
		response, ok := message.(protocol.ResponseEnvelope)
		return ok && response.Id == id
	})
	if err != nil {
		return protocol.ResponseEnvelope{}, err
	}
	return message.(protocol.ResponseEnvelope), nil
}

func (client *wireClient) requestService(target protocol.RpcTarget, serviceID, member string, args ...any) protocol.ResponseEnvelope {
	client.t.Helper()
	response, err := client.requestServiceErr(target, serviceID, member, args...)
	if err != nil {
		client.t.Fatal(err)
	}
	return response
}

func (client *wireClient) attach(serverID, sessionID string) protocol.ResponseEnvelope {
	client.t.Helper()
	return client.requestService(protocol.ServerTarget{ServerId: serverID}, "pi.session-management", "attach", sessionID)
}

type attachResult struct {
	response protocol.ResponseEnvelope
	err      error
}

// attachAsync starts an attach off the test goroutine without calling t.Fatal there; awaitAttach reports its failure on the test goroutine.
func (client *wireClient) attachAsync(serverID, sessionID string) <-chan attachResult {
	result := make(chan attachResult, 1)
	go func() {
		response, err := client.requestServiceErr(protocol.ServerTarget{ServerId: serverID}, "pi.session-management", "attach", sessionID)
		result <- attachResult{response, err}
	}()
	return result
}

func awaitAttach(t *testing.T, result <-chan attachResult) protocol.ResponseEnvelope {
	t.Helper()
	settled := <-result
	if settled.err != nil {
		t.Fatal(settled.err)
	}
	return settled.response
}

// sessionTarget is upstream requestSessionService's routing: the recorded attachment for the Session, or a fixed missing attachment.
func (client *wireClient) sessionTarget(serverID, sessionID string) protocol.RpcTarget {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.attachment == nil || client.attachment.SessionId != sessionID {
		return protocol.SessionTarget{ServerId: serverID, SessionId: sessionID, AttachmentId: "missing-attachment"}
	}
	return protocol.SessionTarget{ServerId: serverID, SessionId: client.attachment.SessionId, AttachmentId: client.attachment.AttachmentId}
}

func (client *wireClient) requestSessionServiceErr(serverID, sessionID, member string, args ...any) (protocol.ResponseEnvelope, error) {
	client.t.Helper()
	return client.requestServiceErr(client.sessionTarget(serverID, sessionID), "test.session", member, args...)
}

func (client *wireClient) requestSessionService(serverID, sessionID, member string, args ...any) protocol.ResponseEnvelope {
	client.t.Helper()
	response, err := client.requestSessionServiceErr(serverID, sessionID, member, args...)
	if err != nil {
		client.t.Fatal(err)
	}
	return response
}

func (client *wireClient) latestAttachmentID(sessionID string) string {
	client.t.Helper()
	client.mu.Lock()
	defer client.mu.Unlock()
	for _, v := range slices.Backward(client.messages) {
		if envelope, ok := v.(protocol.AttachmentEnvelope); ok && envelope.Attachment != nil && envelope.Attachment.SessionId == sessionID {
			return envelope.Attachment.AttachmentId
		}
	}
	client.t.Fatalf("missing attachment for %s", sessionID)
	return ""
}

func expectOK(t *testing.T, response protocol.ResponseEnvelope) {
	t.Helper()
	if !response.Ok {
		t.Fatalf("response = %#v, want ok", response)
	}
}

func expectFailure(t *testing.T, response protocol.ResponseEnvelope, code string) {
	t.Helper()
	if response.Ok || response.Error == nil || response.Error.Code != code {
		t.Fatalf("response = %#v, want failure %s", response, code)
	}
}
