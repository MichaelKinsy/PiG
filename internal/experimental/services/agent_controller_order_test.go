package services

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/lineadmission"
)

type orderConversation struct {
	mu    sync.Mutex
	order []string
}

func (c *orderConversation) ID() durable.ConversationId { return 1 }
func (c *orderConversation) Submit(_ context.Context, content ai.UserContent, _ durable.WhenBusy) (durable.SubmissionId, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	text, _ := content.(ai.UserText)
	c.order = append(c.order, string(text))
	return durable.SubmissionId(len(c.order)), nil
}
func (c *orderConversation) Abort(context.Context) error { return nil }
func (c *orderConversation) Compact(context.Context, *string) (int64, error) {
	return 1, nil
}

// A TypeScript controller operation reaches the durable conversation in the order the presentation sent it: its synchronous prefix
// runs during invoke. A steer sent after a prompt must not overtake it (provider.ts:234; agent-controller.ts).
func TestAgentControllerOperationsReachTheConversationInCallOrder(t *testing.T) {
	const calls = 400
	conversation := &orderConversation{}
	provider, err := chord.NewRemoteServiceProvider(chord.SingletonService(AgentControllerDefinition))
	requireModelsOK(t, err)
	requireModelsOK(t, chord.Provide[AgentController](provider, AgentControllerDefinition, CreateAgentController(nil, conversation)))
	endpoint := chord.CreateRemoteServiceEndpoint(provider)
	t.Cleanup(endpoint.Dispose)
	invocations := make([]*chord.ServiceInvocation, calls)
	for i := range invocations {
		request, _ := json.Marshal(AgentPromptRequest{Message: strconv.Itoa(i)})
		member := "steer"
		if i%2 == 0 {
			member = "followUp"
		}
		invocations[i], err = chord.BeginEndpointInvoke(t.Context(), endpoint, chord.ServiceCall{ServiceId: AgentControllerID, Member: member, Args: []json.RawMessage{request}}, nil)
		requireModelsOK(t, err)
	}
	for _, invocation := range invocations {
		_, err := invocation.Wait(t.Context())
		requireModelsOK(t, err)
	}
	for i, text := range conversation.order {
		if text != strconv.Itoa(i) {
			t.Fatalf("position %d reached the conversation as %s; order %v", i, text, conversation.order[max(0, i-3):min(len(conversation.order), i+4)])
		}
	}
	if len(conversation.order) != calls {
		t.Fatalf("%d operations reached the conversation, want %d", len(conversation.order), calls)
	}
}

// lineConversation reports line admission as the durable Session does when a commit takes its position, then blocks Abort as
// Conversation.Abort does while it waits for the conversation to become idle.
type lineConversation struct {
	orderConversation
	idle chan struct{}
}

func (c *lineConversation) Submit(ctx context.Context, content ai.UserContent, whenBusy durable.WhenBusy) (durable.SubmissionId, error) {
	defer reportLineAdmission(ctx)
	return c.orderConversation.Submit(ctx, content, whenBusy)
}

func (c *lineConversation) Abort(ctx context.Context) error {
	c.mu.Lock()
	c.order = append(c.order, "abort")
	c.mu.Unlock()
	reportLineAdmission(ctx)
	<-c.idle
	return nil
}

// agent-controller-provider.ts abort is conversation.abort(context): its commit joins the Session line in the synchronous prefix
// and the Promise then waits for idle. A steer sent after it reaches the conversation behind the abort's commit without waiting
// for idle.
func TestAgentControllerOperationAfterAbortDoesNotWaitForIdle(t *testing.T) {
	conversation := &lineConversation{idle: make(chan struct{})}
	provider, err := chord.NewRemoteServiceProvider(chord.SingletonService(AgentControllerDefinition))
	requireModelsOK(t, err)
	requireModelsOK(t, chord.Provide[AgentController](provider, AgentControllerDefinition, CreateAgentController(nil, conversation)))
	endpoint := chord.CreateRemoteServiceEndpoint(provider)
	t.Cleanup(endpoint.Dispose)
	abort, err := chord.BeginEndpointInvoke(t.Context(), endpoint, chord.ServiceCall{ServiceId: AgentControllerID, Member: "abort", Args: []json.RawMessage{}}, nil)
	requireModelsOK(t, err)
	request, _ := json.Marshal(AgentPromptRequest{Message: "after"})
	steer, err := chord.BeginEndpointInvoke(t.Context(), endpoint, chord.ServiceCall{ServiceId: AgentControllerID, Member: "steer", Args: []json.RawMessage{request}}, nil)
	requireModelsOK(t, err)
	waitContext, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err = steer.Wait(waitContext)
	close(conversation.idle)
	if err != nil {
		t.Fatalf("steer waited for the abort to reach idle: %v", err)
	}
	_, err = abort.Wait(t.Context())
	requireModelsOK(t, err)
	conversation.mu.Lock()
	defer conversation.mu.Unlock()
	if !slices.Equal(conversation.order, []string{"abort", "after"}) {
		t.Fatalf("conversation order = %v, want [abort after]", conversation.order)
	}
}

// reportLineAdmission does what the durable Session line does for a commit that has taken its position.
func reportLineAdmission(ctx context.Context) {
	if admitted := lineadmission.From(ctx); admitted != nil {
		admitted()
	}
}
