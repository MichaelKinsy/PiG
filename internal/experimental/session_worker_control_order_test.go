package experimental

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableadapter"
	"github.com/MichaelKinsy/PiG/internal/experimental/durabletest"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
	"github.com/MichaelKinsy/PiG/internal/lineadmission"
)

// submitOrderConversation records the order in which controller operations reach the durable conversation.
type submitOrderConversation struct {
	*durableadapter.Session
	mu    sync.Mutex
	order []string
}

func (c *submitOrderConversation) Submit(_ context.Context, content ai.UserContent, _ durable.WhenBusy) (durable.SubmissionId, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	text, _ := content.(ai.UserText)
	c.order = append(c.order, string(text))
	return durable.SubmissionId(len(c.order)), nil
}

// upstream: packages/coding-agent/src/experimental/session-worker.ts handleOperation reaches the service endpoint in its
// synchronous prefix, so operations reach the agent controller in the order the coordinator delivered them. The Go control
// loop must begin each call before it reads the next message.
func TestWorkerControlBeginsOperationsInMessageOrder(t *testing.T) {
	t.Parallel()
	const calls = 300
	faux := durabletest.OpenFauxConversation()
	t.Cleanup(func() { _ = faux.Close(context.Background()) })
	conversation := &submitOrderConversation{Session: faux.Conversation}
	workerServices, err := services.CreateSessionWorkerServices(services.SessionWorkerServicesOptions{
		Harness: faux.Harness, Conversation: conversation,
		Publish: func(context.Context, services.WorkerServiceScope, string, chord.ServiceProviderUpdate) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerServices.Dispose() })
	serverConnection := "server-1"
	lifecycle := NewWorkerLifecycle(WorkerLifecycleOptions{InitialServerConnectionID: &serverConnection, InitialDemandGraceMs: 60_000, OrphanDemandGraceMs: 60_000})
	t.Cleanup(lifecycle.Close)
	if err := lifecycle.SetDemand(serverConnection, "attachment-1", true); err != nil {
		t.Fatal(err)
	}
	local, remote := net.Pipe()
	t.Cleanup(func() { _ = local.Close(); _ = remote.Close() })
	go func() { _, _ = io.Copy(io.Discard, remote) }()
	control := &workerControlConnection{socket: local}
	requests := &workerActiveRequests{values: map[string]*workerActiveRequest{}}
	scope := map[string]string{"serverConnectionId": serverConnection, "attachmentId": "attachment-1"}
	for i := range calls {
		member := "steer"
		if i%2 == 0 {
			member = "followUp"
		}
		request, _ := json.Marshal(services.AgentPromptRequest{Message: strconv.Itoa(i)})
		raw, err := json.Marshal(map[string]any{"type": "message", "from": "server", "payload": map[string]any{
			"type": "operation", "requestId": "request-" + strconv.Itoa(i), "scope": scope,
			"call": map[string]any{"serviceId": services.AgentControllerID, "member": member, "args": []json.RawMessage{request}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err := handleWorkerCommand(raw, control, lifecycle, requests, workerServices, "token", "session", func() error { return nil }, func() {}); err != nil {
			t.Fatal(err)
		}
	}
	requests.work.Wait()
	conversation.mu.Lock()
	defer conversation.mu.Unlock()
	if len(conversation.order) != calls {
		t.Fatalf("%d operations reached the conversation, want %d", len(conversation.order), calls)
	}
	for i, text := range conversation.order {
		if text != strconv.Itoa(i) {
			t.Fatalf("position %d reached the conversation as %s; order %v", i, text, conversation.order[max(0, i-3):min(len(conversation.order), i+4)])
		}
	}
}

// The agent controller hands its ordering to the durable Session line. Every conversation operation it orders must report line
// admission through the adapter, the bound conversation and the Harness, or each operation would wait for the previous one to
// finish (Abort waits for idle).
func TestDurableConversationOperationsReportLineAdmission(t *testing.T) {
	t.Parallel()
	faux := durabletest.OpenFauxConversation()
	t.Cleanup(func() { _ = faux.Close(context.Background()) })
	for _, operation := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"submit", func(ctx context.Context) error {
			_, err := faux.Conversation.Submit(ctx, ai.UserText("hello"), durable.WhenBusyFollowUp)
			return err
		}},
		{"abort submission", func(ctx context.Context) error {
			_, err := faux.Harness.AbortSubmission(ctx, 1, faux.Conversation.ID())
			return err
		}},
		{"compact", func(ctx context.Context) error {
			_, err := faux.Conversation.Compact(ctx, nil)
			return err
		}},
		{"abort", func(ctx context.Context) error { return faux.Conversation.Abort(ctx) }},
	} {
		admitted := false
		err := operation.run(lineadmission.With(t.Context(), func() { admitted = true }))
		if err != nil {
			t.Fatalf("%s: %v", operation.name, err)
		}
		if !admitted {
			t.Fatalf("%s did not report line admission", operation.name)
		}
	}
}
