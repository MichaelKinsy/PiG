package extensionconformance

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// userMessageRuntimeAPI is the Go reference for extension.API.SendUserMessage: the handler the Session binds into the shared extension
// runtime (agent-session.ts:3412 _bindExtensionCore sendUserMessage, copied to runtime.sendUserMessage by runner.ts:410-438).
type userMessageRuntimeAPI struct {
	extension.API
	runtime *extension.ExtensionRuntime
}

func (a userMessageRuntimeAPI) SendUserMessage(content any, options *extension.SendUserMessageOptions) {
	if err := a.runtime.SendUserMessage(content, options); err != nil {
		panic(err)
	}
}

// TestConformanceAPISendUserMessageRunsATurn pins pi.sendUserMessage (packages/coding-agent/src/core/extensions/types.ts:1713,
// agent-session.ts sendUserMessage): the content reaches a real Session as a user message of the extension source, the Session runs a
// model turn on it and the assistant reply lands in the transcript after it.
func TestConformanceAPISendUserMessageRunsATurn(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	services.Registry().SetRuntimeAPIKey("faux", "faux-key")
	provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "faux-1"})
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText("reply to the extension")}, ai.FauxAssistantMessageOptions{})})
	model := &ai.Model{ID: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 4000, MaxOutputTokens: 1000}}
	runtime := extension.CreateExtensionRuntime()
	runner := inproc.NewRunner(nil, t.TempDir(), runtime)
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, SkipBuiltinTools: true, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	runner.AddErrorListener(func(err *extension.ExtensionError) { t.Errorf("extension runtime error: %+v", err) })
	var api extension.API = userMessageRuntimeAPI{runtime: runtime}

	api.SendUserMessage("question from an extension", nil)

	deadline := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case event, ok := <-session.Events():
			if !ok {
				t.Fatal("the event channel closed before the turn ended")
			}
			_, done = event.(agent.AgentEndEvent)
		case <-deadline:
			t.Fatal("no agent_end after sendUserMessage")
		}
	}
	if provider.CallCount() != 1 {
		t.Errorf("the model was called %d times, want 1", provider.CallCount())
	}
	var roles []string
	for _, message := range session.Messages() {
		switch {
		case message.User != nil:
			roles = append(roles, "user")
			blocks, ok := message.User.Content.(ai.UserContentBlocks)
			if !ok || len(blocks) != 1 || blocks[0] != (ai.TextContent{Text: "question from an extension"}) {
				t.Errorf("user message content %#v, want the extension's text", message.User.Content)
			}
		case message.Assistant != nil:
			roles = append(roles, "assistant")
		}
	}
	if len(roles) != 2 || roles[0] != "user" || roles[1] != "assistant" {
		t.Errorf("transcript roles %v, want [user assistant]", roles)
	}
}
