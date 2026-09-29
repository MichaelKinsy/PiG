package coding

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func newQueueCharacterizationHarness(t *testing.T, ext extension.Extension, tools []agent.AgentTool) *recoveryHarness {
	t.Helper()
	if ext.Handlers == nil {
		ext.Handlers = map[string][]extension.HandlerFn{}
	}
	h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true, defaultTools: tools == nil, tools: tools, extension: ext})
	if err := h.session.services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
		t.Fatal(err)
	}
	h.session.currentRunner().BindCore(extension.ExtensionActions{SendUserMessage: func(content any, options *extension.SendUserMessageOptions) error {
		return h.session.SendExtensionUserMessage(content, options)
	}}, extension.ContextActions{IsIdle: h.session.IsIdle, HasPendingMessages: h.session.HasPendingMessages, Abort: h.session.RequestAbort}, nil)
	return h
}

type queueWaitingHarness struct {
	h                    *recoveryHarness
	releaseToolExecution func()
	waitForToolStart     <-chan struct{}
	setResponses         func(...scriptedResponse)
	join                 func()
}

// The setup barrier models the upstream helper's awaited return before the pending prompt reaches its provider. Responses are installed after creating the prompt, without racing the scripted provider.
func createQueueWaitingHarness(t *testing.T, ext extension.Extension) queueWaitingHarness {
	t.Helper()
	release := make(chan struct{})
	releaseTool := sync.OnceFunc(func() { close(release) })
	h := newQueueCharacterizationHarness(t, ext, []agent.AgentTool{bashPersistenceWaitTool{release: release}})
	started := make(chan struct{})
	markStarted := sync.OnceFunc(func() { close(started) })
	h.session.Subscribe(func(event agent.AgentEvent) {
		if start, ok := event.(agent.ToolExecutionStartEvent); ok && start.ToolName == "wait" {
			markStarted()
		}
	})
	ready := make(chan struct{})
	readyPrompt := sync.OnceFunc(func() { close(ready) })
	done := make(chan error, 1)
	go func() { <-ready; _, err := h.session.Prompt(t.Context(), "start", nil); done <- err }()
	join := sync.OnceFunc(func() {
		readyPrompt()
		releaseTool()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(join)
	return queueWaitingHarness{h: h, releaseToolExecution: releaseTool, waitForToolStart: started, join: join, setResponses: func(responses ...scriptedResponse) { h.provider.responses = responses; readyPrompt() }}
}

func queueUserTexts(h *recoveryHarness) []string {
	texts := []string{}
	for _, message := range h.session.Messages() {
		if message.User != nil {
			texts = append(texts, extractUserMessageText(message.User.Content))
		}
	}
	return texts
}
func queueAssistantTexts(h *recoveryHarness) []string {
	texts := []string{}
	for _, message := range h.session.Messages() {
		if message.Assistant != nil {
			var parts []string
			for _, block := range message.Assistant.Content {
				if text, ok := block.(ai.TextContent); ok {
					parts = append(parts, text.Text)
				}
			}
			texts = append(texts, strings.Join(parts, "\n"))
		}
	}
	return texts
}
func queueProviderUserTexts(messages []ai.Message) []string {
	texts := []string{}
	for _, message := range messages {
		if user, ok := message.(ai.UserMessage); ok {
			if text, ok := user.Content.(ai.UserText); ok {
				texts = append(texts, string(text))
				continue
			}
			var parts []string
			blocks, _ := user.Content.(ai.UserContentBlocks)
			for _, block := range blocks {
				if text, ok := block.(ai.TextContent); ok {
					parts = append(parts, text.Text)
				}
			}
			texts = append(texts, strings.Join(parts, "\n"))
		}
	}
	return texts
}
func queueProviderHasTextBlock(messages []ai.Message, text string) bool {
	for _, message := range messages {
		user, ok := message.(ai.UserMessage)
		if !ok {
			continue
		}
		blocks, ok := user.Content.(ai.UserContentBlocks)
		if !ok {
			continue
		}
		for _, block := range blocks {
			if content, ok := block.(ai.TextContent); ok && content.Text == text {
				return true
			}
		}
	}
	return false
}

func queueCommandExtension(handler func(context.Context, string) error) extension.Extension {
	return extension.Extension{Commands: map[string]extension.RegisteredCommand{"testcmd": {Name: "testcmd", Description: "Test command", Handler: handler}}, CommandOrder: []string{"testcmd"}}
}
