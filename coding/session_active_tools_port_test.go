package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestExtensionActiveToolsNextTurnPort(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6162-extension-active-tools-next-turn.test.ts:44
		{"applies pi.setActiveTools before the next provider request in the same run", false},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6162-extension-active-tools-next-turn.test.ts:73
		{"reports the refreshed system prompt during the run", false},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6162-extension-active-tools-next-turn.test.ts:102
		{"preserves before_agent_start system prompt overrides when tools change mid-run", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var session *Session
			makeTool := func(name, label, description, snippet, text string, execute func()) agent.AgentTool {
				tool, err := newBridgeTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: name, Label: label, Description: description, PromptSnippet: snippet, Parameters: json.RawMessage(`{"type":"object","properties":{}}`), Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
					if execute != nil {
						execute()
					}
					return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}, nil
				}}})
				if err != nil {
					t.Fatal(err)
				}
				return tool
			}
			tools := []agent.AgentTool{
				makeTool("switch_tools", "Switch Tools", "Switch the active extension tool set", "Switch to the next extension tool", "switched", func() { session.SetActiveToolsByName([]string{"after_switch"}) }),
				makeTool("after_switch", "After Switch", "Tool that should be available after switching", "Run after the active tool set changes", "after", nil),
			}
			var ext extension.Extension
			if tc.override {
				ext = extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
					event := args[0].(extension.BeforeAgentStartEvent)
					return &extension.BeforeAgentStartEventResult{SystemPrompt: new(event.SystemPrompt + "\n\nkeep this run override")}, nil
				}}}}
			}
			var providerNames [][]string
			var providerPrompts, sessionPrompts []string
			capture := func(request []ai.Message) {
				var names []string
				for _, tool := range ai.GetCurrentTools(request) {
					names = append(names, tool.Name)
				}
				providerNames = append(providerNames, names)
				providerPrompts = append(providerPrompts, ai.GetCurrentSystemPrompt(request))
				sessionPrompts = append(sessionPrompts, session.systemPrompt())
			}
			h := newRecoveryHarness(t, harnessOptions{tools: tools, extension: ext}, func(request []ai.Message) *ai.AssistantMessage {
				capture(request)
				return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "switch", Name: "switch_tools", Arguments: ai.JsonObject{}}}}
			}, func(request []ai.Message) *ai.AssistantMessage {
				capture(request)
				return fauxReply("done", ai.StopReasonStop, 0)(request)
			})
			session = h.session
			session.SetActiveToolsByName([]string{"switch_tools"})
			if !reflect.DeepEqual(session.ActiveToolNames(), []string{"switch_tools"}) {
				t.Fatal(session.ActiveToolNames())
			}
			if _, err := session.Send(t.Context(), "start"); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(session.ActiveToolNames(), []string{"after_switch"}) || !reflect.DeepEqual(providerNames, [][]string{{"switch_tools"}, {"after_switch"}}) {
				t.Fatalf("active %v, provider %v", session.ActiveToolNames(), providerNames)
			}
			if len(providerPrompts) != 2 || !reflect.DeepEqual(sessionPrompts, providerPrompts) {
				t.Fatalf("provider prompts=%q, Session prompts=%q", providerPrompts, sessionPrompts)
			}
			if tc.override {
				for _, prompt := range providerPrompts {
					if !strings.Contains(prompt, "keep this run override") {
						t.Fatal("lost override")
					}
				}
			} else if providerPrompts[0] == providerPrompts[1] {
				t.Fatal("provider prompt did not change with active tools")
			}
			var toolLines [][]string
			for _, prompt := range providerPrompts {
				var lines []string
				for line := range strings.SplitSeq(prompt, "\n") {
					if strings.HasPrefix(line, "- switch_tools:") || strings.HasPrefix(line, "- after_switch:") {
						lines = append(lines, line)
					}
				}
				toolLines = append(toolLines, lines)
			}
			data, err := json.Marshal([]any{providerNames, toolLines, reflect.DeepEqual(sessionPrompts, providerPrompts), tc.override})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("NEXT_TOOLS %s\n", data)
		})
	}
}
