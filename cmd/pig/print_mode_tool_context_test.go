package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Upstream print mode binds the Session to its extensions with session.bindExtensions (print-mode.ts:74-76), which only adds the mode's UI, command actions and handlers before _bindExtensionCore (agent-session.ts:3173-3194, 3301) binds executeTool, getCallableTools and appendEntry. ctx.sessionManager is the runner's constructor field (runner.ts:362, 398-405, 889-891), which no mode binding replaces. A tool of an in-process extension therefore reaches the Session's own log, its callable tools and pi.appendEntry in print and JSON mode, as codemode's store() and ctx.executeTool() need (codemode/execute.ts:285, 301; codemode/index.ts:35).
func TestPrintModeToolContextKeepsTheSessionBinding(t *testing.T) {
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			provider.SetResponses(fauxSteps(
				ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("probe", map[string]any{}, "probe-1")}, StopReason: "toolUse"},
				fauxTextResponse("final"),
			))
			type observation struct {
				manager   any
				tools     []string
				appendErr error
				stored    bool
			}
			observed := make(chan observation, 1)
			host := printModeTestHost(t, provider)
			host.Extensions = []extension.Extension{{Path: "probe-ext", ToolOrder: []string{"probe"}, Tools: map[string]extension.RegisteredTool{"probe": {Definition: extension.ToolDefinition{
				Name: "probe", Description: "probe", Parameters: json.RawMessage(`{"type":"object"}`),
				Execute: func(ctx context.Context, _ string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
					var got observation
					var result extension.AgentToolResult
					tc := extension.ToolContextFromContext(ctx)
					if tc == nil {
						observed <- got
						return result, nil
					}
					got.manager, _ = tc.SessionManager()
					tools, _ := tc.Tools()
					for _, tool := range tools {
						got.tools = append(got.tools, tool.Name)
					}
					got.appendErr = tc.AppendEntry("probe-store", map[string]any{"k": "v"})
					if log, ok := got.manager.(*codingagent.Session); ok {
						got.stored = slices.ContainsFunc(log.GetBranch(), func(entry codingagent.SessionEntry) bool {
							return strings.Contains(string(entry.Raw()), `"customType":"probe-store"`)
						})
					}
					observed <- got
					return result, nil
				},
			}}}}}
			result := runPrintModeForTest(t, host, printModeOptions{Mode: mode, InitialMessage: "go"})
			if result.err != nil || result.stderr != "" {
				t.Fatalf("run = %v, stderr %q", result.err, result.stderr)
			}
			got := <-observed
			if _, ok := got.manager.(*codingagent.Session); !ok {
				t.Fatalf("ctx.sessionManager = %T, want the Session's own *codingagent.Session", got.manager)
			}
			if !slices.Contains(got.tools, "probe") {
				t.Fatalf("ctx.tools = %v, want the Session's callable tools", got.tools)
			}
			if got.appendErr != nil || !got.stored {
				t.Fatalf("pi.appendEntry = %v, entry in the Session's branch = %t", got.appendErr, got.stored)
			}
		})
	}
}
