// Command pig-eval-extension is the extension the eval harness loads into the pig process under test. It registers
// the system prompt transform and the custom tools the harness declares, and forwards each call to the harness over
// the bridge socket (internal/evals/bridge). Run as `pig-eval-extension credential`, it prints the run's API key for
// the "!" command in the agent's auth.json.
package main

import (
	"fmt"
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/evals/bridge"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "pig-eval-extension:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) == 2 && os.Args[1] == bridge.CredentialCommand {
		response, err := bridge.Call(bridge.Request{Op: bridge.OpCredential})
		if err != nil {
			return err
		}
		_, err = fmt.Println(response.Credential)
		return err
	}
	spec, err := bridge.LoadSpec()
	if err != nil {
		return err
	}
	extension := sdk.New(bridge.ExtensionName)
	if spec.TransformSystemPrompt {
		extension.OnEvent(sdk.EventBeforeAgentStart, func(_ sdk.Context, data map[string]any) (any, error) {
			systemPrompt, _ := data["systemPrompt"].(string)
			response, err := bridge.Call(bridge.Request{Op: bridge.OpTransform, SystemPrompt: systemPrompt})
			if err != nil {
				return nil, err
			}
			return map[string]any{"systemPrompt": response.SystemPrompt}, nil
		})
	}
	for _, tool := range spec.Tools {
		definition := sdk.ToolDefinition{
			Name: tool.Name, Label: tool.Label, Description: tool.Description, PromptSnippet: tool.PromptSnippet, Parameters: tool.Parameters,
			Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
				response, err := bridge.Call(bridge.Request{Op: bridge.OpTool, Tool: tool.Name, ToolCallID: ctx.ToolCallID(), Params: params})
				if err != nil {
					return nil, err
				}
				return sdk.ToolResult{Content: response.Content, Details: response.Details, Terminate: response.Terminate}, nil
			},
		}
		if tool.ConstrainedSampling != nil {
			definition.ConstrainedSampling = sdk.ConstrainedSampling{Type: tool.ConstrainedSampling.Type, Strict: tool.ConstrainedSampling.Strict}
		}
		extension.RegisterTool(definition)
	}
	return extension.Run()
}
