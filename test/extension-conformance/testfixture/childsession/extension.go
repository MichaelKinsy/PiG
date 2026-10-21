// Package childsession is the Go SDK extension of the child agent session conformance rows: it creates a session over the host's model runtime with a custom tool, subscribes, prompts, reads the transcript and state back and disposes. Fused, isolated and packed realizations run this one factory; the Python and Rust fixtures of child_session_test.go and the Node fixture carry the same tool, so one observation proves every SDK.
package childsession

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Name is the extension's registered name.
const Name = "child-session"

// Extension is the factory.
func Extension() *sdk.Extension {
	e := sdk.New(Name)
	e.RegisterTool(sdk.ToolDefinition{
		Name: "child_run", Label: "child_run", Description: "child_run", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (any, error) {
			var seen []string
			updates, streamed, partial := 0, "", ""
			text := func(message any) string {
				if message, ok := message.(map[string]any); ok {
					if content, ok := message["content"].([]any); ok && len(content) > 0 {
						if block, ok := content[0].(map[string]any); ok {
							if value, ok := block["text"].(string); ok {
								return value
							}
						}
					}
				}
				return ""
			}
			session, err := ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{
				Model: map[string]any{"provider": "sentinel", "id": "echo"}, Tools: []string{"sentinel_tool"}, InMemory: true,
				Settings:  map[string]any{"compaction": map[string]any{"enabled": false}},
				Resources: sdk.ResourceOptions{NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true, AppendSystemPrompt: []string{"Child appendix"}},
				CustomTools: []sdk.ToolDefinition{{
					Name: "sentinel_tool", Label: "sentinel", Description: "uppercases x", Parameters: sdk.Schema{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}, "required": []string{"x"}},
					Execute: func(_ sdk.Context, params map[string]any) (any, error) {
						return strings.ToUpper(params["x"].(string)) + "!", nil
					},
				}},
			})
			if err != nil {
				return nil, err
			}
			defer session.Dispose()
			session.Subscribe(func(event map[string]any) {
				name := event["type"].(string)
				if message, ok := event["message"].(map[string]any); ok && strings.HasPrefix(name, "message_") {
					name += ":" + message["role"].(string)
				}
				seen = append(seen, name)
				if event["type"] == "message_update" {
					updates++
					streamed = text(event["message"])
					if inner, ok := event["assistantMessageEvent"].(map[string]any); ok {
						partial = text(inner["partial"])
					}
				}
			})
			if err := session.Prompt(context.Background(), args["prompt"].(string)); err != nil {
				return nil, err
			}
			messages, err := session.Messages()
			if err != nil {
				return nil, err
			}
			roles := []string{}
			for _, message := range messages {
				roles = append(roles, message["role"].(string))
			}
			system := map[string]any{}
			if len(messages) > 0 {
				keys := []string{}
				for key := range messages[0] {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				sections := []string{}
				if members, ok := messages[0]["sections"].(map[string]any); ok {
					for key := range members {
						sections = append(sections, key)
					}
				}
				sort.Strings(sections)
				system = map[string]any{"keys": keys, "role": messages[0]["role"], "content": messages[0]["content"], "sections": sections, "toolsAdded": messages[0]["toolsAdded"]}
			}
			state, err := session.State()
			if err != nil {
				return nil, err
			}
			final := ""
			if state.LastAssistantText != nil {
				final = *state.LastAssistantText
			}
			out, err := json.Marshal(map[string]any{"events": seen, "updates": updates, "streamed": streamed, "partial": partial, "roles": roles, "system": system, "final": final, "model": state.Model["id"], "activeTools": state.ActiveTools, "file": state.SessionFile, "appendix": strings.Contains(state.SystemPrompt, "Child appendix")})
			return string(out), err
		},
	})
	registerLifecycle(e)
	registerEvents(e)
	return e
}
