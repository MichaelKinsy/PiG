package sessiongraph

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// childObserved is what the script reads of one session it created over the objects it built.
type childObserved struct {
	session *sdk.AgentSession
	report  map[string]any
}

func entryKinds(manager sdk.SessionManager) []string {
	out := []string{}
	for _, entry := range ok(manager.GetEntries()) {
		kind := fmt.Sprint(entry["type"])
		if kind == "message" {
			kind += ":" + fmt.Sprint(entry["message"].(map[string]any)["role"])
		}
		out = append(out, kind)
	}
	return out
}

func lastText(session *sdk.AgentSession) string {
	messages := ok(session.Messages())
	return messageText(messages[len(messages)-1])
}

// observeChild creates a session with options over manager and settings, binds extensions when bind is set, prompts it and reports what Pi's createAgentSession run reports.
func observeChild(ctx sdk.Context, options sdk.CreateAgentSessionOptions, manager sdk.SessionManager, settings sdk.SettingsManager, bind bool) childObserved {
	options.SessionManager, options.SettingsManager = &manager, &settings
	session := ok(ctx.CreateAgentSession(options))
	events := []string{}
	session.Subscribe(func(event map[string]any) {
		name := event["type"].(string)
		if message, isMessage := event["message"].(map[string]any); isMessage && strings.HasPrefix(name, "message_") {
			name += ":" + message["role"].(string)
		}
		events = append(events, name)
	})
	if bind {
		if err := session.BindExtensions(); err != nil {
			panic(err)
		}
	}
	file := orNil(ok(manager.GetSessionFile()))
	created := map[string]any{
		"model": map[string]any{"provider": session.Model["provider"], "id": session.Model["id"]}, "thinkingLevel": session.ThinkingLevel, "activeTools": session.ActiveTools,
		"fallback": nilIfEmpty(session.ModelFallbackMessage), "extensionErrors": len(session.ExtensionErrors), "appendix": strings.Contains(session.SystemPrompt, "Handle appendix"),
		"sessionId": session.SessionID == ok(manager.GetSessionID()), "sessionFile": (file == nil && session.SessionFile == "") || (file != nil && file == session.SessionFile),
		"persisted": session.SessionFile != "",
	}
	if err := session.Prompt(context.Background(), "hello"); err != nil {
		panic(err)
	}
	messages := ok(session.Messages())
	roles := []string{}
	for _, message := range messages {
		roles = append(roles, message["role"].(string))
	}
	state := ok(session.State())
	final := any(nil)
	if state.LastAssistantText != nil {
		final = *state.LastAssistantText
	}
	return childObserved{session: session, report: map[string]any{"created": created, "events": events, "final": final, "roles": roles, "entries": entryKinds(manager), "text": lastText(session)}}
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// childHandles builds the objects, creates two sessions over them and returns the first session's observation; the objects are unreachable when it returns.
func childHandles(ctx sdk.Context, args map[string]any) (map[string]any, *childObserved, *childObserved, sdk.SessionManager, sdk.SessionManager) {
	r := map[string]any{}
	auth, models, store := fmt.Sprint(args["mrAuth"]), fmt.Sprint(args["mrModels"]), fmt.Sprint(args["mrStore"])
	cwd, dir, agentDir := fmt.Sprint(args["cwd"]), fmt.Sprint(args["dir"]), fmt.Sprint(args["agentDir"])
	rt := ok(sdk.ModelRuntimeCreate(ctx, sdk.ModelRuntimeCreateOptions{AuthPath: auth, ModelsPath: &models, ModelsStorePath: store}))
	settings := ok(sdk.SettingsManagerInMemory(ctx, map[string]any{
		"compaction": map[string]any{"enabled": false}, "retry": map[string]any{"enabled": false}, "defaultProvider": "mr-local", "defaultModel": "m1", "defaultThinkingLevel": "high",
	}, nil))
	loaderOptions := sdk.DefaultResourceLoaderOptions{Cwd: cwd, AgentDir: agentDir, SettingsManager: &settings, NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true}
	withProbe := loaderOptions
	withProbe.AdditionalExtensionPaths = []string{fmt.Sprint(args["probeExt"])}
	withProbe.AppendSystemPrompt = []string{"Handle appendix"}
	loader := ok(sdk.NewDefaultResourceLoader(ctx, withProbe))
	if err := loader.Reload(nil); err != nil {
		panic(err)
	}
	memory := ok(sdk.SessionManagerInMemory(ctx, cwd, nil, nil))
	scoped := []sdk.ScopedModel{{Model: ok(rt.GetModel("mr-local", "m1")), ThinkingLevel: "off"}}
	first := observeChild(ctx, sdk.CreateAgentSessionOptions{ResourceLoader: loader, ModelRuntime: rt, ScopedModels: scoped}, memory, settings, true)
	compaction := ok(settings.GetCompactionEnabled())
	r["memory"] = first.report
	first.report["settings"] = map[string]any{"defaultModel": orNil(ok(settings.GetDefaultModel())), "compaction": compaction}
	file := ok(sdk.SessionManagerCreate(ctx, cwd, &dir, nil))
	plainLoader := ok(sdk.NewDefaultResourceLoader(ctx, loaderOptions))
	if err := plainLoader.Reload(nil); err != nil {
		panic(err)
	}
	second := observeChild(ctx, sdk.CreateAgentSessionOptions{ResourceLoader: plainLoader, ModelRuntime: rt}, file, settings, false)
	_, statErr := os.Stat(*ok(file.GetSessionFile()))
	second.report["onDisk"] = statErr == nil
	second.report["samePath"] = second.session.SessionFile == *ok(file.GetSessionFile())
	r["file"] = second.report
	return r, &first, &second, memory, file
}

func childHandlesScript(ctx sdk.Context, args map[string]any) map[string]any {
	r, first, second, memory, file := childHandles(ctx, args)
	// The extension let go of every object it built (the runtime, settings, loaders and the session managers' objects are collected); the sessions still run over them.
	for range 3 {
		runtime.GC()
		time.Sleep(100 * time.Millisecond)
	}
	if err := first.session.Prompt(context.Background(), "again"); err != nil {
		panic(err)
	}
	r["afterDrop"] = map[string]any{"text": lastText(first.session), "entries": len(entryKinds(memory)), "fileEntries": len(entryKinds(file))}
	first.session.Dispose()
	second.session.Dispose()
	return r
}
