package sessiongraph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func entryMessageText(message map[string]any) string {
	if text, isText := message["content"].(string); isText {
		return text
	}
	var out strings.Builder
	blocks, _ := message["content"].([]any)
	for _, block := range blocks {
		if entry, isMap := block.(map[string]any); isMap && entry["type"] == "text" {
			fmt.Fprint(&out, entry["text"])
		}
	}
	return out.String()
}

// agentSessionRuntimeScript is createAgentSessionRuntime and AgentSessionRuntime over the same steps as testdata/agent-session-runtime.mjs; the report is Pi's own run's.
func agentSessionRuntimeScript(ctx sdk.Context, args map[string]any) map[string]any {
	str := func(key string) string { return args[key].(string) }
	cwd, agentDir, root := str("cwd"), str("agentDir"), str("root")
	r := map[string]any{}
	models := str("mrModels")
	rt := ok(sdk.ModelRuntimeCreate(ctx, sdk.ModelRuntimeCreateOptions{AuthPath: str("mrAuth"), ModelsPath: &models, ModelsStorePath: str("mrStore")}))
	settings := ok(sdk.SettingsManagerInMemory(ctx, map[string]any{
		"compaction": map[string]any{"enabled": false}, "retry": map[string]any{"enabled": false}, "defaultProvider": "mr-local", "defaultModel": "m1",
	}, nil))
	sessionDir := filepath.Join(root, "runtime-sessions")
	logFile, cancelFile := filepath.Join(cwd, "runtime-events.log"), filepath.Join(cwd, "cancel-next")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		panic(err)
	}
	calls := []map[string]any{}
	factory := func(options sdk.CreateAgentSessionRuntimeFactoryOptions) (*sdk.CreateAgentSessionRuntimeResult, error) {
		call := map[string]any{"cwd": options.Cwd, "reason": nil, "previous": false, "persisted": ok(options.SessionManager.IsPersisted())}
		if event := options.SessionStartEvent; event != nil {
			call["reason"], call["previous"] = event.Reason, event.PreviousSessionFile != ""
		}
		calls = append(calls, call)
		loaderOptions := sdk.DefaultResourceLoaderOptions{NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true, AdditionalExtensionPaths: []string{str("runtimeProbe")}}
		services, err := sdk.CreateAgentSessionServices(ctx, sdk.CreateAgentSessionServicesOptions{Cwd: options.Cwd, AgentDir: options.AgentDir, SettingsManager: &settings, ModelRuntime: rt, ResourceLoaderOptions: &loaderOptions})
		if err != nil {
			return nil, err
		}
		session, err := sdk.CreateAgentSessionFromServices(ctx, sdk.CreateAgentSessionFromServicesOptions{Services: services, SessionManager: &options.SessionManager, SessionStartEvent: options.SessionStartEvent})
		if err != nil {
			return nil, err
		}
		return &sdk.CreateAgentSessionRuntimeResult{Session: session, Services: services, Diagnostics: services.Diagnostics, ModelFallbackMessage: session.ModelFallbackMessage}, nil
	}
	manager := ok(sdk.SessionManagerCreate(ctx, cwd, &sessionDir, nil))
	runtimeFile := *ok(manager.GetSessionFile())
	runtime := ok(sdk.CreateAgentSessionRuntime(ctx, factory, sdk.CreateAgentSessionRuntimeOptions{Cwd: cwd, AgentDir: agentDir, SessionManager: manager}))
	rebinds, invalidations := 0, 0
	runtime.SetRebindSession(func(session *sdk.AgentSession) error {
		rebinds++
		return session.BindExtensions()
	})
	runtime.SetBeforeSessionInvalidate(func() { invalidations++ })
	if err := runtime.Session().BindExtensions(); err != nil {
		panic(err)
	}
	current := func() sdk.SessionManager { return ok(runtime.Session().SessionManager()) }
	seen := map[string]bool{ok(manager.GetSessionID()): true}
	isNew := func() bool {
		id := ok(current().GetSessionID())
		fresh := !seen[id]
		seen[id] = true
		return fresh
	}
	kinds := func() []string {
		out := []string{}
		for _, entry := range ok(current().GetEntries()) {
			out = append(out, entry["type"].(string))
		}
		return out
	}
	roles := func() []string {
		out := []string{}
		for _, message := range ok(runtime.Session().Messages()) {
			out = append(out, message["role"].(string))
		}
		return out
	}
	userEntry := func(text string) map[string]any {
		for _, entry := range ok(current().GetEntries()) {
			if message, isMessage := entry["message"].(map[string]any); entry["type"] == "message" && isMessage && message["role"] == "user" && entryMessageText(message) == text {
				return entry
			}
		}
		panic("no user entry " + text)
	}
	cancelling := func(fn func()) {
		if err := os.WriteFile(cancelFile, nil, 0o644); err != nil {
			panic(err)
		}
		defer func() { _ = os.Remove(cancelFile) }()
		fn()
	}
	prompt := func(text string) {
		if err := runtime.Session().Prompt(context.Background(), text); err != nil {
			panic(err)
		}
	}
	parentOf := func() any { return ok(current().GetHeader())["parentSession"] }
	runtimeServices := runtime.Services()
	r["initial"] = map[string]any{
		"cwd": runtime.Cwd(), "diagnostics": append([]sdk.AgentSessionRuntimeDiagnostic{}, runtime.Diagnostics()...), "fallback": nilIfEmpty(runtime.ModelFallbackMessage()),
		"sameLog": ok(current().GetSessionID()) == ok(manager.GetSessionID()), "servicesCwd": runtimeServices.Cwd, "servicesAgentDir": runtimeServices.AgentDir == agentDir, "calls": len(calls),
	}
	prompt("one")
	_, statErr := os.Stat(runtimeFile)
	r["prompted"] = map[string]any{"kinds": kinds(), "persisted": statErr == nil}

	// newSession.
	var cancelledNew sdk.CancelledResult
	cancelling(func() { cancelledNew = ok(runtime.NewSession(nil)) })
	r["newCancelled"] = map[string]any{"result": cancelledNew, "sameSession": !isNew(), "calls": len(calls), "rebinds": rebinds, "invalidations": invalidations}
	created := ok(runtime.NewSession(&sdk.NewSessionRuntimeOptions{
		ParentSession: runtimeFile,
		Setup: func(sm sdk.SessionManager) error {
			_, err := sm.AppendCustomMessageEntry("setup-note", "from setup", true, nil)
			return err
		},
	}))
	r["newSession"] = map[string]any{
		"result": created, "fresh": isNew(), "parentIsFirst": parentOf() == runtimeFile, "kinds": kinds(), "roles": roles(), "calls": append([]map[string]any{}, calls[1:]...), "rebinds": rebinds, "invalidations": invalidations,
	}
	prompt("two")
	forkResult := func(result sdk.ForkResult) map[string]any {
		out := map[string]any{"cancelled": result.Cancelled}
		if result.SelectedText != nil {
			out["selectedText"] = *result.SelectedText
		}
		return out
	}

	// fork.
	secondFile := *ok(current().GetSessionFile())
	two := userEntry("two")
	var cancelledFork sdk.ForkResult
	cancelling(func() { cancelledFork = ok(runtime.Fork(two["id"].(string), nil)) })
	r["forkCancelled"] = map[string]any{"result": forkResult(cancelledFork), "sameSession": !isNew()}
	forked := ok(runtime.Fork(two["id"].(string), nil))
	r["fork"] = map[string]any{
		"result": forkResult(forked), "fresh": isNew(), "parentIsNotSecond": *ok(current().GetSessionFile()) != secondFile, "roles": roles(), "kinds": kinds(), "calls": len(calls) - 2,
	}
	prompt("three")
	assistantID := func() string {
		for _, entry := range ok(current().GetEntries()) {
			if message, isMessage := entry["message"].(map[string]any); entry["type"] == "message" && isMessage && message["role"] == "assistant" {
				return entry["id"].(string)
			}
		}
		panic("no assistant entry")
	}
	forkedAt := ok(runtime.Fork(assistantID(), &sdk.ForkRuntimeOptions{Position: "at"}))
	r["forkAt"] = map[string]any{"result": forkResult(forkedAt), "fresh": isNew(), "roles": roles()}
	r["forkErrors"] = map[string]any{
		"missing": errText(func() error { _, err := runtime.Fork("no-such-entry", nil); return err }()),
		"notUser": errText(func() error { _, err := runtime.Fork(assistantID(), nil); return err }()),
	}

	// switchSession.
	var cancelledSwitch sdk.CancelledResult
	cancelling(func() { cancelledSwitch = ok(runtime.SwitchSession(runtimeFile, nil)) })
	r["switchCancelled"] = map[string]any{"result": cancelledSwitch, "sameSession": !isNew()}
	switched := ok(runtime.SwitchSession(runtimeFile, nil))
	r["switch"] = map[string]any{
		"result": switched, "backOnFirst": ok(current().GetSessionID()) == ok(manager.GetSessionID()), "file": filepath.Base(*ok(current().GetSessionFile())) == filepath.Base(runtimeFile), "roles": roles(),
	}

	// A fork before the first message.
	emptied := ok(runtime.Fork(userEntry("one")["id"].(string), nil))
	r["forkRoot"] = map[string]any{"result": forkResult(emptied), "fresh": isNew(), "parentIsFirst": parentOf() == runtimeFile, "kinds": kinds()}

	// importFromJsonl.
	importDir := filepath.Join(root, "import")
	if err := os.MkdirAll(importDir, 0o755); err != nil {
		panic(err)
	}
	importPath := filepath.Join(importDir, "imported.jsonl")
	content, err := os.ReadFile(runtimeFile)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(importPath, content, 0o644); err != nil {
		panic(err)
	}
	imported := ok(runtime.ImportFromJsonl(importPath, nil))
	importedFile := *ok(current().GetSessionFile())
	_, sourceErr := os.Stat(importPath)
	r["import"] = map[string]any{
		"result": imported, "fresh": isNew(), "inSessionDir": strings.HasPrefix(importedFile, sessionDir), "name": filepath.Base(importedFile), "roles": roles(), "sourceKept": sourceErr == nil,
		"missing": errText(func() error {
			_, err := runtime.ImportFromJsonl(filepath.Join(importDir, "missing.jsonl"), nil)
			return err
		}()),
	}

	// A direct refresh of the live session (the runtime's own after a setup is the one the report shows); Pi's run reads nothing from it, so it adds nothing to the report.
	if err := runtime.Session().RefreshContext(); err != nil {
		panic(err)
	}
	if err := runtime.Dispose(); err != nil {
		panic(err)
	}
	reasons := []string{}
	for _, call := range calls {
		reason := "null"
		if value, isText := call["reason"].(string); isText {
			reason = value
		}
		if call["previous"] == true {
			reason += "+previous"
		}
		reasons = append(reasons, reason)
	}
	r["counts"] = map[string]any{"rebinds": rebinds, "invalidations": invalidations, "factoryCalls": len(calls), "reasons": reasons}
	logged, err := os.ReadFile(logFile)
	if err != nil {
		panic(err)
	}
	r["events"] = strings.Split(strings.TrimRight(string(logged), "\n"), "\n")
	return r
}
