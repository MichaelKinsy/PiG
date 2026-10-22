package sessiongraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func orNilF(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

// readFile is the parsed JSON of a settings file.
func readFile(path string) any {
	data, err := os.ReadFile(path)
	check(err)
	var value any
	check(json.Unmarshal(data, &value))
	return value
}

// readAll reports every argument-less getter of the SettingsManager, by Pi's name.
func readAll(s sdk.SettingsManager) map[string]any {
	return map[string]any{
		"getDefaultProvider":             orNil(ok(s.GetDefaultProvider())),
		"getDefaultModel":                orNil(ok(s.GetDefaultModel())),
		"getDefaultThinkingLevel":        orNil(ok(s.GetDefaultThinkingLevel())),
		"getTheme":                       orNil(ok(s.GetTheme())),
		"getThemeSetting":                orNil(ok(s.GetThemeSetting())),
		"getSteeringMode":                ok(s.GetSteeringMode()),
		"getFollowUpMode":                ok(s.GetFollowUpMode()),
		"getTransport":                   ok(s.GetTransport()),
		"getSessionShareDestination":     ok(s.GetSessionShareDestination()),
		"getCompactionEnabled":           ok(s.GetCompactionEnabled()),
		"getBranchSummarySkipPrompt":     ok(s.GetBranchSummarySkipPrompt()),
		"getRetryEnabled":                ok(s.GetRetryEnabled()),
		"getHttpIdleTimeoutMs":           ok(s.GetHttpIdleTimeoutMs()),
		"getCacheWarmingMode":            ok(s.GetCacheWarmingMode()),
		"getWebSocketConnectTimeoutMs":   orNilF(ok(s.GetWebSocketConnectTimeoutMs())),
		"getHideThinkingBlock":           ok(s.GetHideThinkingBlock()),
		"getShowCacheMissNotices":        ok(s.GetShowCacheMissNotices()),
		"getExternalEditorCommand":       ok(s.GetExternalEditorCommand()),
		"getShellPath":                   orNil(ok(s.GetShellPath())),
		"getQuietStartup":                ok(s.GetQuietStartup()),
		"getDefaultProjectTrust":         ok(s.GetDefaultProjectTrust()),
		"getShellCommandPrefix":          orNil(ok(s.GetShellCommandPrefix())),
		"getNpmCommand":                  ok(s.GetNpmCommand()),
		"getCollapseChangelog":           ok(s.GetCollapseChangelog()),
		"getEnableInstallTelemetry":      ok(s.GetEnableInstallTelemetry()),
		"getEnableAnalytics":             ok(s.GetEnableAnalytics()),
		"getPackages":                    ok(s.GetPackages()),
		"getExtensionPaths":              ok(s.GetExtensionPaths()),
		"getSkillPaths":                  ok(s.GetSkillPaths()),
		"getPromptTemplatePaths":         ok(s.GetPromptTemplatePaths()),
		"getThemePaths":                  ok(s.GetThemePaths()),
		"getEnableSkillCommands":         ok(s.GetEnableSkillCommands()),
		"getThinkingBudgets":             ok(s.GetThinkingBudgets()),
		"getTerminalCapabilityOverrides": ok(s.GetTerminalCapabilityOverrides()),
		"getShowImages":                  ok(s.GetShowImages()),
		"getImageWidthCells":             ok(s.GetImageWidthCells()),
		"getClearOnShrink":               ok(s.GetClearOnShrink()),
		"getShowTerminalProgress":        ok(s.GetShowTerminalProgress()),
		"getTuiMode":                     ok(s.GetTuiMode()),
		"getFullscreenExitOutput":        ok(s.GetFullscreenExitOutput()),
		"getFullscreenScrollbar":         ok(s.GetFullscreenScrollbar()),
		"getFullscreenCopyOnSelect":      ok(s.GetFullscreenCopyOnSelect()),
		"getFullscreenWheelScrollLines":  ok(s.GetFullscreenWheelScrollLines()),
		"getImageAutoResize":             ok(s.GetImageAutoResize()),
		"getBlockImages":                 ok(s.GetBlockImages()),
		"getEnabledModels":               ok(s.GetEnabledModels()),
		"getDefaultTools":                ok(s.GetDefaultTools()),
		"getDoubleEscapeAction":          ok(s.GetDoubleEscapeAction()),
		"getTreeFilterMode":              ok(s.GetTreeFilterMode()),
		"getShowHardwareCursor":          ok(s.GetShowHardwareCursor()),
		"getEditorPaddingX":              ok(s.GetEditorPaddingX()),
		"getOutputPad":                   ok(s.GetOutputPad()),
		"getAutocompleteMaxVisible":      ok(s.GetAutocompleteMaxVisible()),
		"getCodeBlockIndent":             ok(s.GetCodeBlockIndent()),
		"getMermaidRenderingMode":        ok(s.GetMermaidRenderingMode()),
		"getWarnings":                    ok(s.GetWarnings()),
		"getAllModelThinkingLevels":      ok(s.GetAllModelThinkingLevels()),
		"getBranchSummarySettings":       ok(s.GetBranchSummarySettings()),
		"getRetrySettings":               ok(s.GetRetrySettings()),
		"getProviderRetrySettings":       ok(s.GetProviderRetrySettings()),
		"getLastChangelogVersion":        orNil(ok(s.GetLastChangelogVersion())),
		"getSessionDir":                  orNil(ok(s.GetSessionDir())),
		"getSettings":                    ok(s.GetSettings()),
		"getGlobalSettings":              ok(s.GetGlobalSettings()),
		"getProjectSettings":             ok(s.GetProjectSettings()),
		"isProjectTrusted":               ok(s.IsProjectTrusted()),
	}
}

func settingsScript(ctx sdk.Context, args map[string]any) map[string]any {
	cwd, agentDir, configDir, brokenCwd := fmt.Sprint(args["cwd"]), fmt.Sprint(args["agentDir"]), fmt.Sprint(args["configDir"]), fmt.Sprint(args["brokenCwd"])
	r := map[string]any{}
	s := ok(sdk.SettingsManagerCreate(ctx, cwd, &agentDir, nil))
	r["initial"] = readAll(s)
	r["modelBudget"] = map[string]any{
		"reserve":     ok(s.GetCompactionReserveTokens(map[string]any{"provider": "p", "id": "m"})),
		"reserveNone": ok(s.GetCompactionReserveTokens(nil)),
		"keep":        ok(s.GetCompactionKeepRecentTokens(nil)),
		"settings":    ok(s.GetCompactionSettings(map[string]any{"provider": "p", "id": "m"})),
		"thinking":    orNil(ok(s.GetModelThinkingLevel("p", "m"))),
	}
	check(s.SetDefaultProvider("openai"))
	check(s.SetDefaultModel("gpt"))
	check(s.SetDefaultThinkingLevel("high"))
	check(s.SetTheme("light"))
	check(s.SetSteeringMode("one-at-a-time"))
	check(s.SetFollowUpMode("one-at-a-time"))
	check(s.SetCompactionEnabled(true))
	check(s.SetRetryEnabled(false))
	check(s.SetPackages([]any{"npm:a", map[string]any{"source": "npm:b", "extensions": []any{"x"}}}))
	check(s.SetExtensionPaths([]string{"a", "b"}))
	check(s.SetSkillPaths([]string{"s1"}))
	check(s.SetPromptTemplatePaths([]string{"p1"}))
	check(s.SetThemePaths([]string{"t1"}))
	check(s.SetEnabledModels([]string{"m1", "m2"}))
	check(s.SetQuietStartup("header"))
	check(s.SetShellPath(new("/bin/sh")))
	check(s.SetShellCommandPrefix(new("set -e")))
	check(s.SetNpmCommand([]string{"pnpm", "dlx"}))
	check(s.SetModelThinkingLevel("p", "m", "low"))
	check(s.SetHttpIdleTimeoutMs(float64(5000)))
	check(s.SetImageWidthCells(40))
	check(s.SetOutputPad(0))
	check(s.SetTuiMode("fullscreen"))
	check(s.SetFullscreenWheelScrollLines(float64(3)))
	check(s.SetWarnings(map[string]any{"anthropicExtraUsage": true}))
	check(s.SetDefaultProjectTrust("never"))
	check(s.SetLastChangelogVersion("9.9.9"))
	check(s.SetEnableAnalytics(false))
	check(s.SetTransport("sse"))
	check(s.SetCacheWarmingMode("idle"))
	check(s.SetHideThinkingBlock(true))
	check(s.SetShowImages(false))
	check(s.SetClearOnShrink(true))
	check(s.SetDoubleEscapeAction("tree"))
	check(s.SetTreeFilterMode("user-only"))
	check(s.SetMermaidRenderingMode("off"))
	check(s.SetEditorPaddingX(2))
	check(s.SetAutocompleteMaxVisible(7))
	check(s.SetCollapseChangelog(true))
	check(s.SetBlockImages(true))
	check(s.SetSessionShareDestination("gist"))
	check(s.SetFullscreenExitOutput("resume-hint"))
	check(s.SetFullscreenScrollbar("hidden"))
	check(s.SetFullscreenCopyOnSelect(false))
	check(s.SetShowHardwareCursor(true))
	check(s.SetImageAutoResize(false))
	check(s.SetShowTerminalProgress(true))
	check(s.SetEnableSkillCommands(false))
	check(s.SetEnableInstallTelemetry(false))
	check(s.SetShowCacheMissNotices(false))
	check(s.SetDefaultModelAndProvider("anthropic", "opus"))
	check(s.SetProjectPackages([]any{"npm:proj"}))
	check(s.SetProjectExtensionPaths([]string{"pe"}))
	check(s.SetProjectSkillPaths([]string{"ps"}))
	check(s.SetProjectPromptTemplatePaths([]string{"pp"}))
	check(s.SetProjectThemePaths([]string{"pt"}))
	check(s.Flush())
	r["afterSet"] = readAll(s)
	r["modelThinking"] = orNil(ok(s.GetModelThinkingLevel("p", "m")))
	check(s.RemoveModelThinkingLevel("p", "m"))
	r["removed"] = map[string]any{"thinking": orNil(ok(s.GetModelThinkingLevel("p", "m"))), "trackingId": orNil(ok(s.GetTrackingID())), "deviceId": len(ok(s.GetOrCreateDeviceID())) > 0}
	check(s.Flush())
	r["files"] = map[string]any{"global": readFile(filepath.Join(agentDir, "settings.json")), "project": readFile(filepath.Join(cwd, configDir, "settings.json"))}

	persisted := ok(sdk.SettingsManagerCreate(ctx, cwd, &agentDir, nil))
	r["persisted"] = readAll(persisted)
	check(os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultProvider":"external"}`), 0o600))
	check(persisted.Reload())
	r["reload"] = map[string]any{"provider": orNil(ok(persisted.GetDefaultProvider())), "model": orNil(ok(persisted.GetDefaultModel())), "merged": ok(persisted.GetSettings())}
	check(persisted.SetShellPath(nil))
	check(persisted.SetShellCommandPrefix(nil))
	check(persisted.SetNpmCommand(nil))
	check(persisted.SetEnabledModels(nil))
	check(persisted.SetDefaultModel("kept"))
	check(persisted.Flush())
	r["cleared"] = map[string]any{
		"shellPath": orNil(ok(persisted.GetShellPath())), "prefix": orNil(ok(persisted.GetShellCommandPrefix())), "npm": ok(persisted.GetNpmCommand()), "enabled": ok(persisted.GetEnabledModels()),
		"file": readFile(filepath.Join(agentDir, "settings.json")),
	}

	memory := ok(sdk.SettingsManagerInMemory(ctx, map[string]any{"defaultProvider": "mem", "compaction": map[string]any{"enabled": false}, "retry": map[string]any{"maxRetries": 9}}, nil))
	r["memory"] = map[string]any{
		"provider": orNil(ok(memory.GetDefaultProvider())), "compaction": ok(memory.GetCompactionSettings(nil)), "retry": ok(memory.GetRetrySettings()), "merged": ok(memory.GetSettings()),
		"trusted": ok(memory.IsProjectTrusted()),
	}
	check(memory.SetDefaultModel("mm"))
	check(memory.ApplyOverrides(map[string]any{"defaultModel": "ov"}))
	r["memoryAfter"] = map[string]any{"model": orNil(ok(memory.GetDefaultModel())), "global": ok(memory.GetGlobalSettings()), "merged": ok(memory.GetSettings())}
	untrusted := ok(sdk.SettingsManagerInMemory(ctx, nil, &sdk.SettingsManagerCreateOptions{ProjectTrusted: new(false)}))
	r["untrustedMemory"] = map[string]any{"trusted": ok(untrusted.IsProjectTrusted()), "write": failure(untrusted.SetProjectPackages([]any{"x"}), "refusing to write project settings")}

	broken := ok(sdk.SettingsManagerCreate(ctx, brokenCwd, &agentDir, nil))
	drained := ok(broken.DrainErrors())
	entry := map[string]any{"count": len(drained)}
	if len(drained) > 0 {
		entry["scope"] = drained[0].Scope
		entry["pathSuffix"] = drained[0].Path != nil && strings.HasSuffix(*drained[0].Path, filepath.Join(configDir, "settings.json"))
		entry["hasMessage"] = drained[0].Error != nil && drained[0].Error.Error() != ""
	}
	entry["again"] = len(ok(broken.DrainErrors()))
	r["errors"] = entry

	distrust := ok(sdk.SettingsManagerCreate(ctx, cwd, &agentDir, &sdk.SettingsManagerCreateOptions{ProjectTrusted: new(false)}))
	r["distrusted"] = map[string]any{
		"trusted": ok(distrust.IsProjectTrusted()), "project": ok(distrust.GetProjectSettings()), "merged": ok(distrust.GetSettings()),
		"write": failure(distrust.SetProjectExtensionPaths([]string{"z"}), "refusing to write project settings"),
	}
	check(distrust.SetProjectTrusted(true))
	r["retrusted"] = map[string]any{"trusted": ok(distrust.IsProjectTrusted()), "projectModel": ok(distrust.GetProjectSettings())["defaultModel"]}
	r["storage"] = storageReport(ctx)
	return r
}

type memoryStorage struct {
	store map[string]*string
	calls *[]string
	fail  bool
}

func (m *memoryStorage) WithLock(scope string, fn func(*string) (*string, error)) error {
	if m.fail {
		return errors.New("disk full")
	}
	state := "none"
	if m.store[scope] != nil {
		state = "text"
	}
	*m.calls = append(*m.calls, scope+":"+state)
	next, err := fn(m.store[scope])
	if err != nil {
		return err
	}
	if next != nil {
		*m.calls = append(*m.calls, scope+":write")
		m.store[scope] = next
	}
	return nil
}

func parsedText(text *string) any {
	if text == nil {
		return nil
	}
	var out any
	check(json.Unmarshal([]byte(*text), &out))
	return out
}

func storageReport(ctx sdk.Context) map[string]any {
	global := `{"theme":"dark","quietStartup":true}`
	store := map[string]*string{"global": &global}
	calls := []string{}
	snapshot := func() []string { return append([]string{}, calls...) }
	storage := &memoryStorage{store: store, calls: &calls}
	out := map[string]any{}
	manager := ok(sdk.SettingsManagerFromStorage(ctx, storage, nil))
	out["loaded"] = map[string]any{"theme": orNil(ok(manager.GetTheme())), "quiet": ok(manager.GetQuietStartup()), "calls": snapshot(), "errors": len(ok(manager.DrainErrors()))}
	calls = calls[:0]
	check(manager.SetTheme("light"))
	check(manager.Flush())
	out["wrote"] = map[string]any{"calls": snapshot(), "global": parsedText(store["global"]), "project": store["project"] == nil, "theme": orNil(ok(manager.GetTheme()))}
	solarized := `{"theme":"solarized"}`
	store["global"] = &solarized
	calls = calls[:0]
	check(manager.Reload())
	out["reloaded"] = map[string]any{"theme": orNil(ok(manager.GetTheme())), "calls": snapshot()}
	calls = calls[:0]
	check(manager.SetProjectExtensionPaths([]string{"./e"}))
	check(manager.Flush())
	var projectPaths any
	if extensions, found := ok(manager.GetProjectSettings())["extensions"]; found {
		projectPaths = extensions
	}
	out["projectWrote"] = map[string]any{"project": parsedText(store["project"]), "calls": snapshot(), "paths": projectPaths}
	calls = calls[:0]
	untrusted := ok(sdk.SettingsManagerFromStorage(ctx, storage, &sdk.SettingsManagerCreateOptions{ProjectTrusted: new(false)}))
	out["untrusted"] = map[string]any{"calls": snapshot(), "theme": orNil(ok(untrusted.GetTheme())), "trusted": ok(untrusted.IsProjectTrusted())}
	bad := "{bad"
	store["global"] = &bad
	broken := ok(sdk.SettingsManagerFromStorage(ctx, storage, nil))
	brokenErrors := []any{}
	for _, entry := range ok(broken.DrainErrors()) {
		brokenErrors = append(brokenErrors, map[string]any{"scope": entry.Scope, "noPath": entry.Path == nil, "hasMessage": entry.Error != nil && entry.Error.Error() != ""})
	}
	out["broken"] = brokenErrors
	failing := ok(sdk.SettingsManagerFromStorage(ctx, &memoryStorage{store: map[string]*string{}, calls: &[]string{}, fail: true}, nil))
	failed := []any{}
	for _, entry := range ok(failing.DrainErrors()) {
		failed = append(failed, map[string]any{"scope": entry.Scope, "message": entry.Error.Error()})
	}
	out["failing"] = failed
	return out
}
