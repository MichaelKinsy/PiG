package durableagent

// pi: packages/coding-agent/src/experimental/durable/subagent.ts

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

func subagentRegistry(t *testing.T, extra ...*durable.Extension) harness.Registry {
	t.Helper()
	registry := harness.CreateRegistry()
	for _, extension := range append([]*durable.Extension{Subagent}, extra...) {
		if err := registry.Install(extension); err != nil {
			t.Fatal(err)
		}
	}
	return registry
}

func toolNames(t *testing.T, conversation harness.Conversation) []string {
	t.Helper()
	agent, err := conversation.Agent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range agent.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func lastToolResult(t *testing.T, conversation harness.Conversation) ai.ToolResultMessage {
	t.Helper()
	var result ai.ToolResultMessage
	for _, entry := range entriesOf(t, conversation) {
		if len(entry.Model) > 0 {
			if message, ok := entry.Model[0].(ai.ToolResultMessage); ok {
				result = message
			}
		}
	}
	if result.ToolCallID == "" {
		t.Fatal("the conversation has no tool result")
	}
	return result
}

func textOf(message ai.ToolResultMessage) string {
	var text strings.Builder
	for _, content := range message.Content {
		if block, ok := content.(ai.TextContent); ok {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

// subagent.ts:25-54: a call runs its task in a child conversation owned by the call's task, returns the child's answer with the child's id in the details, and leaves the child behind; the child cannot delegate further.
func TestSubagentRunsTheTaskInAChildConversation(t *testing.T) {
	agent := openFauxAgent(t, storage.NewMemoryStorage(), subagentRegistry(t), t.TempDir(),
		callStep("subagent", map[string]any{"task": "find the answer"}, "call-1"),
		answerStep("the answer is 42"),
		answerStep("all done"),
	)
	settled := prompt(t, agent.root, "delegate")
	if settled.Status != durable.SubmissionDone {
		t.Fatalf("status = %s", settled.Status)
	}
	result := lastToolResult(t, agent.root)
	if got := textOf(result); got != "the answer is 42" {
		t.Fatalf("tool result = %q, want the child's answer", got)
	}
	details, ok := result.Details.(map[string]any)
	if !ok || len(details) != 1 {
		t.Fatalf("details = %#v", result.Details)
	}
	childID := durable.ConversationId(details["conversationId"].(float64))
	if childID == durable.ROOT_CONVERSATION_ID {
		t.Fatal("the details name the root conversation")
	}
	child, err := agent.harness.Conversation(context.Background(), childID)
	if err != nil || child == nil {
		t.Fatalf("the child conversation %d outlives the call: %v %v", childID, child, err)
	}
	// The child holds the task as its user message and the answer as its assistant message.
	var kinds []string
	for _, entry := range slices.Backward(entriesOf(t, child)) {
		kinds = append(kinds, entry.Kind)
	}
	if want := []string{"pi.user", "pi.assistant"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("child entries = %v, want %v", kinds, want)
	}
	// The call's task owns the child: it is found by owner.
	owned, err := durable.Commit(context.Background(), agent.harness, func(tx durable.Tx) ([]durable.ConversationRecord, error) {
		page, err := tx.ScanConversations(durable.ConversationQuery{}, 10, nil)
		return page.Items, err
	})
	if err != nil {
		t.Fatal(err)
	}
	var ownedByTask int
	for _, record := range owned {
		if record.Id == childID && record.Owner != nil && record.Owner.TaskId != 0 {
			ownedByTask++
		}
	}
	if ownedByTask != 1 {
		t.Fatalf("conversations = %+v; want the child owned by a task", owned)
	}
	if !slices.Contains(toolNames(t, agent.root), "subagent") || slices.Contains(toolNames(t, child), "subagent") {
		t.Fatalf("root tools %v, child tools %v; only the root may delegate", toolNames(t, agent.root), toolNames(t, child))
	}
}

// subagent.ts:36-47: a rerun after a crash finds the child it created and the submission it made, so the task runs once and the answer is the same.
func TestSubagentRerunFindsTheChildAndItsSubmission(t *testing.T) {
	var subagent *durable.ToolRegistration
	for _, tool := range Subagent.Tools {
		subagent = tool
	}
	rerun := new(durable.Extension{Name: "rerun", Tools: []*durable.ToolRegistration{new(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "twice", Description: "Runs the subagent tool twice in one call", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			args := map[string]any{"task": "count to three"}
			first, err := subagent.Execute(ctx, args, api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			second, err := subagent.Execute(ctx, args, api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if !reflect.DeepEqual(first, second) {
				t.Errorf("the rerun returned %+v, the first run %+v", second, first)
			}
			return second, nil
		},
	})}})
	agent := openFauxAgent(t, storage.NewMemoryStorage(), subagentRegistry(t, rerun), t.TempDir(),
		callStep("twice", map[string]any{}, "call-1"),
		answerStep("one two three"),
		answerStep("finished"),
	)
	if settled := prompt(t, agent.root, "go"); settled.Status != durable.SubmissionDone {
		t.Fatalf("status = %s", settled.Status)
	}
	// Three provider requests only: the call, the child's one answer, and the final answer. A second child or a second submission would need a fourth.
	if remaining := agent.pending(); remaining != 0 {
		t.Fatalf("%d faux responses unused", remaining)
	}
	children, err := durable.Commit(context.Background(), agent.harness, func(tx durable.Tx) (int, error) {
		page, err := tx.ScanConversations(durable.ConversationQuery{}, 10, nil)
		return len(page.Items), err
	})
	if err != nil || children != 2 {
		t.Fatalf("conversations = %d, %v; want the root and one child", children, err)
	}
}

// subagent.ts:36 replay "safe": a crash in the middle of a call does not lose it. The Harness that reopens the Session reruns the call, which finds the child and the submission of the first run, and the child's answer reaches the parent.
func TestSubagentCallInterruptedByAClosedHarnessResumesAfterReopen(t *testing.T) {
	database := filepath.Join(t.TempDir(), "session.sqlite")
	open := func(steps ...ai.FauxResponseStep) *fauxAgent {
		store, err := sqlitenode.OpenNodeSqliteStorage(database, sqlitenode.NodeSqliteStorageOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return openFauxAgent(t, store, subagentRegistry(t), t.TempDir(), steps...)
	}
	reached := make(chan struct{})
	blocked := ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		close(reached)
		<-options.Signal.Done()
		return ai.FauxResponse{}.AssistantMessage(), context.Cause(options.Signal)
	})
	first := open(callStep("subagent", map[string]any{"task": "find the answer"}, "call-1"), blocked)
	submission, err := first.root.Submit(context.Background(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("delegate")})
	if err != nil {
		t.Fatal(err)
	}
	<-reached
	// Close writes no outcome: the interrupted turn resumes with the next open.
	if err := first.harness.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	second := open(answerStep("recovered answer"), answerStep("all done"))
	second.harness.Resume()
	reopened, err := second.harness.Submission(context.Background(), submission.Id())
	if err != nil || reopened == nil {
		t.Fatalf("submission = %v, %v", reopened, err)
	}
	settled, err := reopened.Wait(context.Background())
	if err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("settled = %+v, %v", settled, err)
	}
	if got := textOf(lastToolResult(t, second.root)); got != "recovered answer" {
		t.Fatalf("tool result = %q, want the recovered child's answer", got)
	}
}
