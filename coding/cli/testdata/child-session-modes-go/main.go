// Command child-session-modes-go registers a provider and a command that creates a child agent session over it, prompts the child and writes what the child answered to the file named by PIG_TEST_CHILD_LOG. It runs in print, JSON and RPC mode.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func record(line string) {
	if err := os.WriteFile(os.Getenv("PIG_TEST_CHILD_LOG"), []byte(line+"\n"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func main() {
	ext := sdk.New("child-session-modes-go")
	zero := map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}
	ext.RegisterProvider("child-faux", sdk.ProviderConfig{
		"api": "openai-completions", "baseUrl": "https://child-faux.invalid", "apiKey": "child-key",
		"models": []any{map[string]any{"id": "m", "name": "M", "reasoning": false, "input": []string{"text"}, "cost": zero, "contextWindow": 128000, "maxTokens": 4096}},
		"streamSimple": func(_ sdk.Context, model, _, _ map[string]any) (*sdk.ModelEventStream, error) {
			message := map[string]any{"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": []any{map[string]any{"type": "text", "text": "child answer"}}, "stopReason": "stop", "timestamp": 1,
				"usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}
			stream := sdk.CreateAssistantMessageEventStream()
			stream.Push(map[string]any{"type": "start", "partial": message})
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message})
			return stream, nil
		},
	})
	ext.Command("child", "child", func(ctx sdk.Context, _ string) error {
		session, err := ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{
			Model: map[string]any{"provider": "child-faux", "id": "m"}, InMemory: true,
			Settings:  map[string]any{"compaction": map[string]any{"enabled": false}},
			Resources: sdk.ResourceOptions{NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true},
		})
		if err != nil {
			record("error:" + err.Error())
			return nil
		}
		defer session.Dispose()
		if err := session.Prompt(context.Background(), "hello"); err != nil {
			record("error:" + err.Error())
			return nil
		}
		state, err := session.State()
		if err != nil || state.LastAssistantText == nil {
			record(fmt.Sprintf("error:state %v", err))
			return nil
		}
		record("final:" + *state.LastAssistantText)
		ctx.Notify("child done", "info")
		return nil
	})
	if err := ext.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "child-session-modes-go: %v\n", err)
		os.Exit(1)
	}
}
