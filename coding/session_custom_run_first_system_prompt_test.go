package coding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func firstRunSnippetTool(t *testing.T, name, snippet string) agent.AgentTool {
	t.Helper()
	tool, err := newBridgeTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: name, Label: name, Description: name + " tool", PromptSnippet: snippet, Parameters: json.RawMessage(`{"type":"object","properties":{}}`), Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

// agent-session.ts sendCustomMessage awaits _startRun, whose _prepareRun records the run's prompt sections with
// _preparePromptAndToolLoadout for a custom starting message as for a prompt. A session's first run started by a
// triggerTurn message therefore reaches the provider with the same system prompt a first prompt gets.
func TestCustomMessageFirstRunCarriesTheSessionSystemPrompt(t *testing.T) {
	firstRequestPrompt := func(t *testing.T, start func(*Session) error) string {
		t.Helper()
		var prompt string
		h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{firstRunSnippetTool(t, "snippet_tool", "Run snippet_tool for the snippet work.")}}, func(request []ai.Message) *ai.AssistantMessage {
			prompt = ai.GetCurrentSystemPrompt(request)
			return fauxReply("done", ai.StopReasonStop, 0)(request)
		})
		if err := start(h.session); err != nil {
			t.Fatal(err)
		}
		// The prompt names the harness's own temp cwd (with forward slashes, as Pi's buildSystemPrompt writes it on Windows) and the docs bundle under its home.
		cwd := h.session.CWD()
		return strings.NewReplacer(cwd, "<cwd>", filepath.ToSlash(cwd), "<cwd>", os.Getenv("PIG_HOME"), "<home>").Replace(prompt)
	}
	fromPrompt := firstRequestPrompt(t, func(s *Session) error { return s.Prompt(t.Context(), "hello") })
	fromMessage := firstRequestPrompt(t, func(s *Session) error {
		return s.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "kick", Content: "begin", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)})
	})
	if !strings.Contains(fromPrompt, "- snippet_tool: Run snippet_tool for the snippet work.") {
		t.Fatalf("the prompt run's system prompt lacks the tool snippet: %q", fromPrompt)
	}
	if fromMessage != fromPrompt {
		t.Fatalf("the custom-message run's system prompt = %q, want the prompt run's %q", fromMessage, fromPrompt)
	}
}
