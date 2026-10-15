package agent

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/src/agent.ts:371-382 prompt(input: string, images?: ImageContent[]), with normalizePromptInput (agent.ts): the user message is one text block followed by the images, the run settles before the call returns, and prompt resolves to nothing: the messages are in the transcript.
func TestAgentPromptBuildsATextThenImagesUserMessageAndSettles(t *testing.T) {
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream { return doneStream(textMessage("seen")) }}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider)})
	images := []ai.ImageContent{{Data: "AAAA", MimeType: "image/png"}, {Data: "BBBB", MimeType: "image/jpeg"}}
	if err := a.Prompt(t.Context(), "look", images...); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	messages := a.Messages()
	roles := []string{}
	for _, m := range messages {
		roles = append(roles, m.Role())
	}
	if !slices.Equal(roles, []string{"user", "assistant"}) {
		t.Fatalf("transcript roles after Prompt = %v, want the run settled with user, assistant", roles)
	}
	content := messages[0].User.Content
	blocks, ok := content.(ai.UserContentBlocks)
	if !ok || len(blocks) != 3 {
		t.Fatalf("user content = %#v, want text then two images", content)
	}
	if text, ok := blocks[0].(ai.TextContent); !ok || text.Text != "look" {
		t.Fatalf("first block = %#v, want the text", blocks[0])
	}
	if first, ok := blocks[1].(ai.ImageContent); !ok || first.Data != "AAAA" {
		t.Fatalf("second block = %#v, want the first image", blocks[1])
	}
	if second, ok := blocks[2].(ai.ImageContent); !ok || second.Data != "BBBB" {
		t.Fatalf("third block = %#v, want the second image", blocks[2])
	}
}

// upstream: agent.ts:371 prompt(message: AgentMessage | AgentMessage[]): the messages seed the turn as they are, in order, and a single message is a batch of one (agent.test.ts:232 and :260 prompt with message arrays).
func TestAgentPromptMessagesSeedsTheTurnInOrder(t *testing.T) {
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream { return doneStream(textMessage("ok")) }}
	user := func(text string) AgentMessage {
		return AgentMessage{User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}}
	}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider)})
	if err := a.PromptMessages(t.Context(), []AgentMessage{user("one"), user("two")}); err != nil {
		t.Fatalf("PromptMessages batch: %v", err)
	}
	texts := func(a *Agent) []string {
		var out []string
		for _, m := range a.Messages() {
			if m.User != nil {
				out = append(out, m.User.Content.(ai.UserContentBlocks)[0].(ai.TextContent).Text)
			}
		}
		return out
	}
	if got := texts(a); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("batch transcript users = %v, want [one two] in order", got)
	}
	single := mustNewAgent(AgentOptions{Model: scriptedModel(provider)})
	if err := single.PromptMessages(t.Context(), []AgentMessage{user("only")}); err != nil {
		t.Fatalf("PromptMessages single: %v", err)
	}
	if got := texts(single); !slices.Equal(got, []string{"only"}) {
		t.Fatalf("single transcript users = %v, want [only]", got)
	}
}

// upstream: agent.test.ts:709 "should throw when prompt() called while streaming", through both overloads: prompt rejects with the same error whether it gets a string or messages.
func TestAgentPromptRejectsWhileAnotherRunIsActive(t *testing.T) {
	a, stop := busyAgent(t)
	defer stop()
	const want = "Agent is already processing a prompt. Use steer() or followUp() to queue messages, or wait for completion."
	if err := a.Prompt(context.Background(), "Second message"); !errors.Is(err, ErrAlreadyProcessingPrompt) || err.Error() != want {
		t.Fatalf("Prompt while streaming = %v, want %q", err, want)
	}
	second := AgentMessage{User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "Second message"}}}}
	if err := a.PromptMessages(context.Background(), []AgentMessage{second}); !errors.Is(err, ErrAlreadyProcessingPrompt) || err.Error() != want {
		t.Fatalf("PromptMessages while streaming = %v, want %q", err, want)
	}
}
