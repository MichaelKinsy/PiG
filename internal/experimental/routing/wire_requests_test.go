package routing_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// closeWire is the WireChannel close of conformance.test.ts: idempotent, and it reports the close to the server handler once.
func (client *wireClient) closeWire() {
	_ = client.Close()
}

// serviceCall builds a call whose arguments are the JSON encodings of args.
func serviceCall(serviceID, member string, args ...any) (chord.ServiceCall, error) {
	encoded := make([]json.RawMessage, len(args))
	for i, arg := range args {
		data, err := json.Marshal(arg)
		if err != nil {
			return chord.ServiceCall{}, err
		}
		encoded[i] = data
	}
	return chord.ServiceCall{ServiceId: serviceID, Member: member, Args: encoded}, nil
}

// requestServiceErr sends one request and waits for its response, or for the wire to close first.
func (client *wireClient) requestServiceErr(target protocol.RpcTarget, serviceID, member string, args ...any) (protocol.ResponseEnvelope, error) {
	call, err := serviceCall(serviceID, member, args...)
	if err != nil {
		return protocol.ResponseEnvelope{}, err
	}
	return client.RequestService(client.ctx, target, call, nil)
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
	response, err := client.Attach(client.ctx, serverID, sessionID)
	if err != nil {
		client.t.Fatal(err)
	}
	return response
}

type attachResult struct {
	response protocol.ResponseEnvelope
	err      error
}

// attachAsync starts an attach off the test goroutine without calling t.Fatal there; awaitAttach reports its failure on the test goroutine.
func (client *wireClient) attachAsync(serverID, sessionID string) <-chan attachResult {
	result := make(chan attachResult, 1)
	go func() {
		response, err := client.Attach(client.ctx, serverID, sessionID)
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

// requestSessionServiceErr is upstream requestSessionService for a test.session call: it targets the client's current attachment for the Session.
func (client *wireClient) requestSessionServiceErr(serverID, sessionID, member string, args ...any) (protocol.ResponseEnvelope, error) {
	call, err := serviceCall("test.session", member, args...)
	if err != nil {
		return protocol.ResponseEnvelope{}, err
	}
	return client.RequestSessionService(client.ctx, serverID, sessionID, call, nil)
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
	for _, v := range slices.Backward(client.Messages()) {
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
