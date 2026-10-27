package sessiongraph

import (
	"path/filepath"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// extensionRefsScript passes the references of CreateCodemodeExtension, CreateToolSearchExtension and CreateMcpExtension to a DefaultResourceLoader, as testdata/session-graph.mjs extensionRefsScript does with Pi's factories; the report is Pi's own run's.
func extensionRefsScript(ctx sdk.Context, args map[string]any) map[string]any {
	r := map[string]any{}
	str := func(key string) string { return args[key].(string) }
	cwd, agentDir := str("cwd"), str("agentDir")
	models, store := str("mrModels"), str("mrStore")
	rt := ok(sdk.ModelRuntimeCreate(ctx, sdk.ModelRuntimeCreateOptions{AuthPath: str("mrAuth"), ModelsPath: &models, ModelsStorePath: store}))
	settings := ok(sdk.SettingsManagerInMemory(ctx, map[string]any{
		"compaction": map[string]any{"enabled": false}, "retry": map[string]any{"enabled": false}, "defaultProvider": "mr-local", "defaultModel": "m1",
	}, nil))
	quiet := func(factories ...sdk.ExtensionFactory) sdk.DefaultResourceLoaderOptions {
		options := sdk.DefaultResourceLoaderOptions{
			Cwd: cwd, AgentDir: agentDir, SettingsManager: &settings, NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
			AdditionalExtensionPaths: []string{str("refsProbe")},
		}
		for _, factory := range factories {
			options.ExtensionFactories = append(options.ExtensionFactories, factory)
		}
		return options
	}
	observe := func(options sdk.DefaultResourceLoaderOptions, tools []string) any {
		loader := ok(sdk.NewDefaultResourceLoader(ctx, options))
		if err := loader.Reload(nil); err != nil {
			panic(err)
		}
		manager := ok(sdk.SessionManagerInMemory(ctx, cwd, nil, nil))
		session := ok(ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{SessionManager: &manager, SettingsManager: &settings, ModelRuntime: rt, ResourceLoader: loader, Tools: tools}))
		if err := session.BindExtensions(); err != nil {
			panic(err)
		}
		seen := []any{}
		for _, entry := range ok(manager.GetEntries()) {
			if entry["type"] == "custom" && entry["customType"] == "refs_seen" {
				seen = append(seen, entry["data"])
			}
		}
		session.Dispose()
		return seen
	}
	budget, hideModels, wait := 123.0, false, 0.0
	factories := func() []sdk.ExtensionFactory {
		return []sdk.ExtensionFactory{
			sdk.CreateCodemodeExtension(sdk.CodemodeExtensionOptions{Mode: "only", InlineBudget: &budget, Models: &hideModels}),
			sdk.CreateToolSearchExtension(),
			sdk.CreateMcpExtension(sdk.McpExtensionOptions{LogPath: filepath.Join(str("dir"), "mcp.log"), StartupWaitMs: &wait}),
		}
	}
	both := []string{"codemode", "tool_search"}
	r["inactive"] = observe(quiet(factories()...), nil)
	r["active"] = observe(quiet(factories()...), both)
	r["defaults"] = observe(quiet(sdk.CreateCodemodeExtension(), sdk.CreateToolSearchExtension()), both)
	r["none"] = observe(quiet(), both)
	return r
}
