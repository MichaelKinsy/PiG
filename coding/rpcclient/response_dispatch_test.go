package rpcclient

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// rpc-client.ts handleLine: a response whose id has a pending request resolves it and is removed (pendingRequests.delete(id)); anything else with that shape, such as a second
// response carrying the same id, is emitted to the listeners as an event, and a response with no id is an event too. The reader must not block on a request already answered.
func TestRpcClientAnswersAPendingRequestOnceAndEmitsLaterResponsesWithItsIdAsEvents(t *testing.T) {
	client := NewRpcClient(RpcClientOptions{})
	deliver := func(c *RpcClient, line string) { c.handleLine([]byte(line)) }
	request := pendingRequest{resolve: make(chan RpcResponse, 1), reject: make(chan error, 1)}
	client.pendingRequests["req_1"] = request
	var events []string
	client.OnEvent(func(event JsonAgentSessionEvent) { events = append(events, event.Type) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		deliver(client, `{"type":"response","id":"req_1","command":"get_state","success":true}`)
		deliver(client, `{"type":"response","id":"req_1","command":"get_state","success":true}`)
		deliver(client, `{"type":"response","command":"get_state","success":true}`)
	}()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the reader blocked on a response whose request was already answered")
	}
	select {
	case response := <-request.resolve:
		if response.ID != "req_1" || response.Command != "get_state" || !response.Success {
			t.Errorf("resolved response = %+v", response)
		}
	default:
		t.Fatal("the pending request was not resolved")
	}
	if len(client.pendingRequests) != 0 {
		t.Errorf("%d requests still pending after their response", len(client.pendingRequests))
	}
	if len(events) != 2 || events[0] != "response" || events[1] != "response" {
		t.Errorf("listeners saw %v, want the repeated and the id-less response as two events", events)
	}
}
