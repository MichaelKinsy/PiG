package sessiongraph

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// servicesScript is createAgentSessionServices and createAgentSessionFromServices over the same options as testdata/session-graph.mjs servicesScript; the report is Pi's own run's.
func servicesScript(ctx sdk.Context, args map[string]any) map[string]any {
	r := map[string]any{}
	str := func(key string) string { return args[key].(string) }
	cwd, agentDir := str("cwd"), str("agentDir")
	models, store := str("mrModels"), str("mrStore")
	rt := ok(sdk.ModelRuntimeCreate(ctx, sdk.ModelRuntimeCreateOptions{AuthPath: str("mrAuth"), ModelsPath: &models, ModelsStorePath: store}))
	settings := ok(sdk.SettingsManagerInMemory(ctx, map[string]any{
		"compaction": map[string]any{"enabled": false}, "retry": map[string]any{"enabled": false}, "defaultProvider": "mr-local", "defaultModel": "m1", "defaultThinkingLevel": "high",
	}, nil))
	quiet := sdk.DefaultResourceLoaderOptions{NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true}
	withProbe := quiet
	withProbe.AdditionalExtensionPaths = []string{str("servicesProbe")}
	withProbe.AppendSystemPrompt = []string{"Services appendix"}
	services := ok(sdk.CreateAgentSessionServices(ctx, sdk.CreateAgentSessionServicesOptions{
		Cwd: filepath.Join(cwd, "sub", ".."), AgentDir: agentDir, SettingsManager: &settings, ModelRuntime: rt, ResourceLoaderOptions: &withProbe,
		ExtensionFlagValues: []sdk.ExtensionFlagValue{{Name: "probe-flag", Value: true}, {Name: "missing-flag", Value: "x"}, {Name: "probe-text", Value: true}, {Name: "other", Value: false}},
	}))
	loaded := ok(services.ResourceLoader.GetExtensions())
	paths := []string{}
	for _, extension := range loaded.Extensions {
		paths = append(paths, extension.Path)
	}
	ids := []string{}
	for _, id := range ok(rt.GetRegisteredProviderIDs()) {
		if strings.HasPrefix(id, "svc-") {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	r["services"] = map[string]any{
		"cwd": services.Cwd, "agentDir": services.AgentDir, "sameRuntime": services.ModelRuntime == rt, "sameSettings": services.SettingsManager == &settings,
		"diagnostics": services.Diagnostics, "extensions": paths, "extensionErrors": len(loaded.Errors), "appendix": ok(services.ResourceLoader.GetAppendSystemPrompt()),
		"providers": map[string]any{"ids": ids, "models": allModelIDs(ok(rt.GetModels("svc-prov")))},
	}
	own := ok(sdk.CreateAgentSessionServices(ctx, sdk.CreateAgentSessionServicesOptions{Cwd: cwd, AgentDir: str("servicesAgentDir"), ResourceLoaderOptions: &quiet}))
	r["own"] = map[string]any{
		"cwd": own.Cwd, "agentDir": own.AgentDir, "diagnostics": own.Diagnostics, "models": allModelIDs(ok(own.ModelRuntime.GetModels("svc-local"))),
		"defaultModel": orNil(ok(own.SettingsManager.GetDefaultModel())), "project": ok(own.SettingsManager.GetProjectSettings()), "appendix": ok(own.ResourceLoader.GetAppendSystemPrompt()),
		"registered": ok(own.ModelRuntime.GetRegisteredProviderIDs()),
	}
	defaulted := ok(sdk.CreateAgentSessionServices(ctx, sdk.CreateAgentSessionServicesOptions{Cwd: cwd, SettingsManager: &settings, ModelRuntime: rt, ResourceLoaderOptions: &quiet}))
	r["defaulted"] = map[string]any{"agentDir": defaulted.AgentDir == ok(sdk.AgentDir()), "cwd": defaulted.Cwd}
	memory := ok(sdk.SessionManagerInMemory(ctx, cwd, nil, nil))
	session := ok(sdk.CreateAgentSessionFromServices(ctx, sdk.CreateAgentSessionFromServicesOptions{
		Services: services, SessionManager: &memory, SessionStartEvent: &sdk.SessionStartEvent{Reason: "fork", PreviousSessionFile: "/previous.jsonl"},
		ScopedModels: []sdk.ScopedModel{{Model: ok(rt.GetModel("mr-local", "m1")), ThinkingLevel: "off"}},
	}))
	events := []string{}
	session.Subscribe(func(event map[string]any) {
		name := event["type"].(string)
		if message, isMessage := event["message"].(map[string]any); isMessage && strings.HasPrefix(name, "message_") {
			name += ":" + message["role"].(string)
		}
		events = append(events, name)
	})
	if err := session.BindExtensions(); err != nil {
		panic(err)
	}
	created := map[string]any{
		"model": map[string]any{"provider": session.Model["provider"], "id": session.Model["id"]}, "thinkingLevel": session.ThinkingLevel, "fallback": nilIfEmpty(session.ModelFallbackMessage),
		"extensionErrors": len(session.ExtensionErrors), "appendix": strings.Contains(session.SystemPrompt, "Services appendix"), "sessionId": session.SessionID == ok(memory.GetSessionID()),
	}
	if err := session.Prompt(context.Background(), "hello"); err != nil {
		panic(err)
	}
	roles := []string{}
	for _, message := range ok(session.Messages()) {
		roles = append(roles, message["role"].(string))
	}
	started := []any{}
	for _, entry := range ok(memory.GetEntries()) {
		if entry["type"] == "custom" && entry["customType"] == "probe_start" {
			started = append(started, entry["data"])
		}
	}
	r["session"] = map[string]any{"created": created, "events": events, "roles": roles, "text": lastText(session), "started": started}
	session.Dispose()
	return r
}

func allModelIDs(models []map[string]any) []string {
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model["id"].(string))
	}
	return ids
}
