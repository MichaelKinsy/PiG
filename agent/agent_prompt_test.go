package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func promptAgent(t *testing.T) (*Agent, *scriptedProvider) {
	t.Helper()
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream { return doneStream(textMessage("done")) }}
	return mustNewAgent(AgentOptions{Model: scriptedModel(provider)}), provider
}

// upstream: packages/agent/src/agent.ts:372 prompt(input: string, images?: ImageContent[]): Promise<void>; the user message is the text followed by the images.
func TestAgentPrompt_RunsAUserMessageOfTextAndImages(t *testing.T) {
	a, provider := promptAgent(t)
	image := ai.ImageContent{Data: "aGk=", MimeType: "image/png"}
	if err := a.Prompt(context.Background(), "look", image); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	messages := a.State().Messages()
	if len(messages) != 2 || messages[0].User == nil || provider.calls() != 1 {
		t.Fatalf("transcript = %+v, provider calls %d", messages, provider.calls())
	}
	blocks, ok := messages[0].User.Content.(ai.UserContentBlocks)
	if !ok || len(blocks) != 2 {
		t.Fatalf("user content = %#v, want text then image", messages[0].User.Content)
	}
	if text, isText := blocks[0].(ai.TextContent); !isText || text.Text != "look" {
		t.Fatalf("first block = %#v", blocks[0])
	}
	if got, isImage := blocks[1].(ai.ImageContent); !isImage || got.MimeType != "image/png" {
		t.Fatalf("second block = %#v", blocks[1])
	}
}

// upstream: agent.ts:371 prompt(message: AgentMessage | AgentMessage[]).
func TestAgentPromptMessages_RunsTheGivenMessages(t *testing.T) {
	a, provider := promptAgent(t)
	message := AgentMessage{User: &UserMessage{Role: "user", Content: ai.UserText("hello"), Timestamp: 1}}
	if err := a.PromptMessages(context.Background(), []AgentMessage{message}); err != nil {
		t.Fatalf("PromptMessages: %v", err)
	}
	if messages := a.State().Messages(); len(messages) != 2 || messages[0].User == nil || provider.calls() != 1 {
		t.Fatalf("transcript = %+v, provider calls %d", messages, provider.calls())
	}
}

// upstream: agent.ts:386 continue(): Promise<void>: an empty transcript throws "No messages to continue from"; a transcript ending in a user message continues.
func TestAgentContinue_RejectsAnEmptyTranscriptAndContinuesFromAUserMessage(t *testing.T) {
	a, provider := promptAgent(t)
	if err := a.Continue(context.Background()); !errors.Is(err, ErrNoMessagesToContinue) {
		t.Fatalf("Continue on an empty transcript = %v, want %v", err, ErrNoMessagesToContinue)
	}
	a.SetMessages([]AgentMessage{{User: &UserMessage{Role: "user", Content: ai.UserText("hi"), Timestamp: 1}}})
	if err := a.Continue(context.Background()); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if got := a.State().Messages(); len(got) != 2 || got[1].Assistant == nil || provider.calls() != 1 {
		t.Fatalf("transcript = %+v, provider calls %d", got, provider.calls())
	}
}

// upstream: agent.ts:372 images is optional: without images the user message is the text alone.
func TestAgentPrompt_WithoutImagesSendsTheTextAlone(t *testing.T) {
	a, _ := promptAgent(t)
	if err := a.Prompt(context.Background(), "plain"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	content := a.State().Messages()[0].User.Content
	var texts int
	switch c := content.(type) {
	case ai.UserContentBlocks:
		for _, block := range c {
			if text, ok := block.(ai.TextContent); ok && text.Text == "plain" {
				texts++
			} else {
				t.Fatalf("unexpected block %#v without images", block)
			}
		}
	default:
		t.Fatalf("user content = %#v", content)
	}
	if texts != 1 {
		t.Fatalf("text blocks = %d, want 1", texts)
	}
}

// upstream: agent.test.ts:232 `await agent.prompt([...])`: prompt(message[]) runs every given message as one user turn batch.
func TestAgentPromptMessages_RunsAnArrayOfMessagesInOrder(t *testing.T) {
	a, provider := promptAgent(t)
	first := userMessage("first")
	second := userMessage("second")
	if err := a.PromptMessages(context.Background(), []AgentMessage{first, second}); err != nil {
		t.Fatalf("PromptMessages: %v", err)
	}
	messages := a.State().Messages()
	if len(messages) != 3 || messages[0].User == nil || messages[1].User == nil || messages[2].Assistant == nil || provider.calls() != 1 {
		t.Fatalf("roles %v, provider calls %d, want both messages then one answer", roles(messages), provider.calls())
	}
	if text := messages[0].User.Content.(ai.UserContentBlocks)[0].(ai.TextContent).Text; text != "first" {
		t.Fatalf("first message = %q, want first", text)
	}
}

// upstream: agent.test.ts:785 "continue() should process queued follow-up messages after an assistant turn": the Pi-shaped continue
// resolves to nothing and the follow-up is in the transcript with its answer.
func TestAgentContinue_ProcessesAQueuedFollowUpAfterAnAssistantTurn(t *testing.T) {
	a := mustNewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("Processed")})})
	a.SetMessages([]AgentMessage{userMessage("Initial"), assistantText("Initial response")})
	a.FollowUp(userMessage("Queued follow-up"))
	if err := a.Continue(context.Background()); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	messages := a.State().Messages()
	if len(messages) != 4 || messages[2].User == nil || messages[3].Assistant == nil {
		t.Fatalf("roles %v, want the follow-up answered", roles(messages))
	}
}

// upstream: agent.ts:386 continue from an assistant tail without queued input throws "Cannot continue from message role: assistant".
func TestAgentContinue_RejectsAnAssistantTailWithoutQueuedInput(t *testing.T) {
	a := mustNewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("unused")})})
	a.SetMessages([]AgentMessage{userMessage("Initial"), assistantText("Initial response")})
	if err := a.Continue(context.Background()); err == nil || err.Error() != "Cannot continue from message role: assistant" {
		t.Fatalf("Continue = %v, want Cannot continue from message role: assistant", err)
	}
}

// upstream: agent.ts:371-379 prompt([]) normalizes to an empty batch and still runs runAgentLoop (agent-loop.ts:102-125), so the provider answers from the current transcript.
func TestAgentPromptMessages_EmptyBatchStillRunsATurn(t *testing.T) {
	a, provider := promptAgent(t)
	if err := a.PromptMessages(context.Background(), []AgentMessage{}); err != nil {
		t.Fatalf("PromptMessages([]): %v", err)
	}
	if messages := a.State().Messages(); len(messages) != 1 || messages[0].Assistant == nil || provider.calls() != 1 {
		t.Fatalf("transcript = %+v, provider calls %d, want one assistant turn", messages, provider.calls())
	}
}

// upstream: agent.test.ts:709 and :749: prompt() and continue() reject while a run is active, with Pi's messages, also for prompt([]).
func TestAgentPromptAndContinue_RejectWhileStreaming(t *testing.T) {
	a, stop := busyAgent(t)
	defer stop()
	const promptBusy = "Agent is already processing a prompt. Use steer() or followUp() to queue messages, or wait for completion."
	if err := a.Prompt(context.Background(), "Second message"); !errors.Is(err, ErrAlreadyProcessingPrompt) || err.Error() != promptBusy {
		t.Fatalf("Prompt while streaming = %v", err)
	}
	if err := a.PromptMessages(context.Background(), []AgentMessage{}); !errors.Is(err, ErrAlreadyProcessingPrompt) || err.Error() != promptBusy {
		t.Fatalf("PromptMessages([]) while streaming = %v", err)
	}
	if err := a.Continue(context.Background()); err == nil || err.Error() != "Agent is already processing. Wait for completion before continuing." {
		t.Fatalf("Continue while streaming = %v", err)
	}
}
