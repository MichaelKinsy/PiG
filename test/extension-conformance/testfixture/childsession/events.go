package childsession

import (
	"context"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// sentinelScope is the scopedModels of the child_events scenario: two models with thinking levels no default produces.
func sentinelScope() []sdk.ScopedModel {
	return []sdk.ScopedModel{
		{Model: map[string]any{"provider": "sentinel", "id": "echo"}, ThinkingLevel: "high"},
		{Model: map[string]any{"provider": "nokey", "id": "locked"}, ThinkingLevel: "low"},
	}
}

// registerEvents is the child_events scenario of the Node row (child_session_node_test.go): a session whose resources load a probe extension (args.probe) is created with scopedModels, its extensions are bound, and a streamed prompt runs. The probe records the events it receives in a file the test reads. args.via "loader" builds the session over a DefaultResourceLoader this extension created; any other value selects the resources by option.
func registerEvents(e *sdk.Extension) {
	e.RegisterTool(sdk.ToolDefinition{
		Name: "child_events", Label: "child_events", Description: "child_events", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (any, error) {
			options := sdk.CreateAgentSessionOptions{
				Model: map[string]any{"provider": "sentinel", "id": "echo"}, InMemory: true,
				Settings:     map[string]any{"compaction": map[string]any{"enabled": false}},
				ScopedModels: sentinelScope(),
			}
			quiet := sdk.ResourceOptions{NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true, AdditionalExtensionPaths: []string{args["probe"].(string)}}
			if args["via"] == "loader" {
				loader, err := sdk.NewDefaultResourceLoader(ctx, sdk.DefaultResourceLoaderOptions{
					Cwd: args["cwd"].(string), AgentDir: args["agentDir"].(string),
					NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true, AdditionalExtensionPaths: quiet.AdditionalExtensionPaths,
				})
				if err != nil {
					return nil, err
				}
				if err := loader.Reload(nil); err != nil {
					return nil, err
				}
				options.ResourceLoader = loader
			} else {
				options.Resources = quiet
			}
			session, err := ctx.CreateAgentSession(options)
			if err != nil {
				return nil, err
			}
			defer session.Dispose()
			if err := session.BindExtensions(); err != nil {
				return nil, err
			}
			if err := session.Prompt(context.Background(), "stream:3"); err != nil {
				return nil, err
			}
			return "done", nil
		},
	})
}
