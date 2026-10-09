package evalsuites

// Ports packages/evals/evals/extensions.docs.eval.ts.
//
// Pi's output reads session.resourceLoader.getExtensions(). A PiG Session runs in a pig process, so the output
// reports what that process exposes: extension_error events, and the tool as loaded when it returned a successful
// result in the Session (a tool that never loaded cannot return one).

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/internal/evals"
)

const (
	helloToolName   = "hello"
	helloToolResult = "Hello, Bob!"
)

func extensionErrors(raw []json.RawMessage) []string {
	messages := []string{}
	for _, entry := range raw {
		var event struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(entry, &event) == nil {
			messages = append(messages, event.Error)
		}
	}
	return messages
}

// Extensions asks the agent to create a tool extension, reload, and use the tool.
func Extensions() evals.Suite {
	tools := append(append([]string{}, evals.DocumentationEvalTools[:]...), helloToolName)
	return evals.Suite{
		Name: "Create and use a tool extension",
		File: "evals/extensions.docs.eval.go",
		Harness: documentationHarness(evals.PiCodingAgentHarnessOptions{
			Tools: tools,
			Output: func(_ context.Context, run evals.AgentRun) (any, error) {
				result, loaded := run.SuccessfulTools[helloToolName]
				var toolResult any
				if loaded {
					toolResult = result
				}
				return map[string]any{
					"response": run.Response, "extensionErrors": extensionErrors(run.ExtensionErrors), "toolLoaded": loaded, "toolResult": toolResult,
				}, nil
			},
		}),
		Judges: []evals.Judge{
			mustJudge(map[string]any{"response": helloToolResult, "extensionErrors": []any{}, "toolLoaded": true, "toolResult": helloToolResult}),
			evals.ToolCallJudge(evals.ExpectedTool{Name: helloToolName, Arguments: map[string]any{"name": "Bob"}}),
		},
		NoJudgeThreshold: true,
		Cases: []evals.Case{singleCase("creates, reloads, and invokes the extension", evals.PiCodingAgentInput{
			{Type: evals.PiCodingAgentStepPrompt, Content: "Configure this running PiG installation with an extension containing a hello tool that takes a name and returns a greeting. Do not create project source. For example, passing Bob should return `Hello, Bob!`."},
			{Type: evals.PiCodingAgentStepReload},
			{Type: evals.PiCodingAgentStepPrompt, Content: "Use the hello tool to greet Bob. Respond with exactly the tool's greeting and nothing else."},
		})},
	}
}
