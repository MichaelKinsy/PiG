package codingagent

// Ports packages/coding-agent/src/core/settings-manager.ts

// The SettingsManager an Extension SDK constructs: Pi's class as a host-owned object an extension names by handle. The object is the runtime's own *SettingsManager, built by the same constructors, so every getter and setter is the one the runtime uses. The Node runtime serves Pi's own class in the extension process.

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// GraphSettingsManager is a SettingsManager an extension constructed.
type GraphSettingsManager struct {
	manager *SettingsManager
}

// Manager is the settings object the handle names. A session created from it shares it, as Pi's createAgentSession shares the SettingsManager it is given.
func (g *GraphSettingsManager) Manager() *SettingsManager { return g.manager }

// graphSettingsOptions is Pi's SettingsManagerCreateOptions.
type graphSettingsOptions struct {
	ProjectTrusted *bool `json:"projectTrusted"`
}

// NewGraphSettingsManager builds the SettingsManager of Pi's static factory op (create, inMemory, fromStorage).
func NewGraphSettingsManager(scope subprocess.SessionGraphScope, op string, raw json.RawMessage) (*GraphSettingsManager, error) {
	args, err := ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	switch op {
	case "create":
		// create(cwd, agentDir = getAgentDir(), options = {})
		cwd, err := args.String(0)
		if err != nil {
			return nil, err
		}
		agentDir, err := args.OptString(1)
		if err != nil {
			return nil, err
		}
		var options graphSettingsOptions
		if err := args.Unmarshal(2, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
			return nil, err
		}
		resolvedCwd, err := ResolvePath(cwd, "")
		if err != nil {
			return nil, err
		}
		dir := ""
		if agentDir != nil {
			dir, err = ResolvePath(*agentDir, "")
		} else {
			dir, err = ResolveAgentDir()
		}
		if err != nil {
			return nil, err
		}
		return &GraphSettingsManager{NewSettingsManagerWithOptions(resolvedCwd, dir, SettingsManagerCreateOptions(options))}, nil
	case "fromStorage":
		// fromStorage(storage, options = {}): storage is { withLock } and the extension names it by the callback that runs it.
		var storage struct {
			WithLock string `json:"withLock"`
		}
		if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &storage) }); err != nil {
			return nil, err
		}
		if storage.WithLock == "" {
			return nil, errors.New("SettingsManager.fromStorage needs a storage with withLock")
		}
		var options graphSettingsOptions
		if err := args.Unmarshal(1, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
			return nil, err
		}
		return &GraphSettingsManager{NewSettingsManagerFromStorage(&graphSettingsStorage{scope: scope, callback: storage.WithLock}, SettingsManagerCreateOptions(options))}, nil
	case "inMemory":
		// inMemory(settings = {}, options = {})
		var settings Settings
		if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &settings) }); err != nil {
			return nil, err
		}
		var options graphSettingsOptions
		if err := args.Unmarshal(1, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
			return nil, err
		}
		manager := NewInMemorySettingsManager(settings)
		if options.ProjectTrusted != nil && !*options.ProjectTrusted {
			manager.SetProjectTrusted(false)
		}
		return &GraphSettingsManager{manager}, nil
	}
	return nil, fmt.Errorf("SettingsManager has no static factory %q", op)
}

// The set helpers decode argument i as the type the setter takes (the zero value for undefined and null) and pass it to fn.
func argError(i int, err error) error { return fmt.Errorf("argument %d: %w", i+1, err) }

func setString[T ~string](args GraphArgs, i int, fn func(T) error) error {
	var value string
	if raw := args.Raw(i); raw != nil {
		if err := json.Unmarshal(raw, &value); err != nil {
			return argError(i, err)
		}
	}
	return fn(T(value))
}

// setOptString passes nil for undefined and null, and the string otherwise.
func setOptString(args GraphArgs, i int, fn func(*string) error) error {
	raw := args.Raw(i)
	if raw == nil {
		return fn(nil)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return argError(i, err)
	}
	return fn(&value)
}

func setBool(args GraphArgs, i int, fn func(bool) error) error {
	var value bool
	if raw := args.Raw(i); raw != nil {
		if err := json.Unmarshal(raw, &value); err != nil {
			return argError(i, err)
		}
	}
	return fn(value)
}

func setInt[T ~int](args GraphArgs, i int, fn func(T) error) error {
	var value int
	if raw := args.Raw(i); raw != nil {
		if err := json.Unmarshal(raw, &value); err != nil {
			return argError(i, err)
		}
	}
	return fn(T(value))
}

func setFloat(args GraphArgs, i int, fn func(float64) error) error {
	var value float64
	if raw := args.Raw(i); raw != nil {
		if err := json.Unmarshal(raw, &value); err != nil {
			return argError(i, err)
		}
	}
	return fn(value)
}

func setStrings(args GraphArgs, i int, fn func([]string) error) error {
	var value []string
	if raw := args.Raw(i); raw != nil {
		if err := json.Unmarshal(raw, &value); err != nil {
			return argError(i, err)
		}
	}
	return fn(value)
}

func setPackages(args GraphArgs, i int, fn func([]PackageSource) error) error {
	var value []PackageSource
	if raw := args.Raw(i); raw != nil {
		if err := json.Unmarshal(raw, &value); err != nil {
			return argError(i, err)
		}
	}
	return fn(value)
}

func setQuietStartup(args GraphArgs, i int, fn func(QuietStartup) error) error {
	var value QuietStartup
	if raw := args.Raw(i); raw != nil {
		if err := json.Unmarshal(raw, &value); err != nil {
			return argError(i, err)
		}
	}
	return fn(value)
}

func setWarnings(args GraphArgs, i int, fn func(WarningSettings) error) error {
	var value WarningSettings
	if raw := args.Raw(i); raw != nil {
		if err := json.Unmarshal(raw, &value); err != nil {
			return argError(i, err)
		}
	}
	return fn(value)
}

// setWheelScrollLines is Pi's `number | "auto"`: a JSON number is the lines, and anything else is "auto".
func setWheelScrollLines(args GraphArgs, i int, fn func(WheelScrollLines) error) error {
	var lines float64
	if raw := args.Raw(i); raw != nil && json.Unmarshal(raw, &lines) == nil {
		return fn(WheelScrollLines{Lines: lines})
	}
	return fn(WheelScrollLines{Auto: true})
}

// optStr is a Pi `string | undefined` the runtime holds as a string: empty is undefined.
func optStr(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func settingsJSON(settings Settings) (any, error) {
	encoded, err := json.Marshal(settings)
	return json.RawMessage(encoded), err
}

func applyOverrides(sm *SettingsManager, args GraphArgs) error {
	var overrides Settings
	if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &overrides) }); err != nil {
		return err
	}
	sm.ApplyOverrides(overrides)
	return nil
}

// drainErrors is Pi's drainErrors(): each error as {scope, path?, error: message}.
func drainErrors(sm *SettingsManager) (any, error) {
	out := []map[string]any{}
	for _, drained := range sm.DrainErrors() {
		entry := map[string]any{"scope": drained.Scope}
		if drained.Path != "" {
			entry["path"] = drained.Path
		}
		message := ""
		if drained.Error != nil {
			message = drained.Error.Error()
		}
		entry["error"] = message
		out = append(out, entry)
	}
	return out, nil
}

// modelArg is the provider and id of the optional `Pick<Model, "provider" | "id">` of the compaction getters.
func modelArg(args GraphArgs, i int) (provider, id string, err error) {
	if !args.Has(i) {
		return "", "", nil
	}
	var model struct {
		Provider string `json:"provider"`
		ID       string `json:"id"`
	}
	if err := args.Unmarshal(i, func(raw json.RawMessage) error { return json.Unmarshal(raw, &model) }); err != nil {
		return "", "", err
	}
	return model.Provider, model.ID, nil
}

// compactionToken is getCompactionReserveTokens or getCompactionKeepRecentTokens for the model with that provider and id.
func compactionToken(sm *SettingsManager, setting, provider, id string) (int, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return getCompactionTokenSetting(sm.merged.Compaction, setting, provider, id)
}

// compactionSettings is getCompactionSettings(model) as {enabled, reserveTokens, keepRecentTokens}.
func compactionSettings(sm *SettingsManager, provider, id string) (any, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	reserve, err := getCompactionTokenSetting(sm.merged.Compaction, "reserveTokens", provider, id)
	if err != nil {
		return nil, err
	}
	recent, err := getCompactionTokenSetting(sm.merged.Compaction, "keepRecentTokens", provider, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"enabled": compactionEnabled(sm.merged.Compaction), "reserveTokens": reserve, "keepRecentTokens": recent}, nil
}

func terminalCapabilityOverrides(sm *SettingsManager) map[string]any {
	overrides := sm.GetTerminalCapabilityOverrides()
	out := map[string]any{}
	if overrides.Images != nil {
		out["images"] = string(*overrides.Images)
	}
	if overrides.TrueColor != nil {
		out["trueColor"] = *overrides.TrueColor
	}
	if overrides.Hyperlinks != nil {
		out["hyperlinks"] = *overrides.Hyperlinks
	}
	return out
}

// wheelScrollLines is Pi's WheelScrollLines: "auto" or a number.
func wheelScrollLines(lines WheelScrollLines) any {
	if lines.Auto {
		return "auto"
	}
	return lines.Lines
}

// Call runs a SettingsManager method.
func (g *GraphSettingsManager) Call(_ context.Context, method string, raw json.RawMessage) (any, error) {
	args, err := ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	sm := g.manager
	switch method {
	case "getSettings":
		return settingsJSON(sm.GetSettings())
	case "getGlobalSettings":
		return settingsJSON(sm.GetGlobalSettings())
	case "getProjectSettings":
		return settingsJSON(sm.GetProjectSettings())
	case "isProjectTrusted":
		return sm.IsProjectTrusted(), nil
	case "setProjectTrusted":
		return nil, setBool(args, 0, func(v bool) error {
			sm.SetProjectTrusted(v)
			return nil
		})
	case "reload":
		sm.Reload()
		return nil, nil
	case "applyOverrides":
		return nil, applyOverrides(sm, args)
	case "flush":
		return nil, sm.Flush()
	case "drainErrors":
		return drainErrors(sm)
	case "getLastChangelogVersion":
		return optStr(sm.GetLastChangelogVersion()), nil
	case "setLastChangelogVersion":
		return nil, setString(args, 0, sm.SetLastChangelogVersion)
	case "getSessionDir":
		dir, err := sm.GetSessionDir()
		return optStr(dir), err
	case "getDefaultProvider":
		return optStr(sm.GetDefaultProvider()), nil
	case "getDefaultModel":
		return optStr(sm.GetDefaultModel()), nil
	case "setDefaultProvider":
		return nil, setString(args, 0, sm.SetDefaultProvider)
	case "setDefaultModel":
		return nil, setString(args, 0, sm.SetDefaultModel)
	case "setDefaultModelAndProvider":
		a0, err := args.String(0)
		if err != nil {
			return nil, err
		}
		a1, err := args.String(1)
		if err != nil {
			return nil, err
		}
		return nil, sm.SetDefaultModelAndProvider(a0, a1)
	case "getSteeringMode":
		return sm.GetSteeringMode(), nil
	case "setSteeringMode":
		return nil, setString(args, 0, sm.SetSteeringMode)
	case "getFollowUpMode":
		return sm.GetFollowUpMode(), nil
	case "setFollowUpMode":
		return nil, setString(args, 0, sm.SetFollowUpMode)
	case "getThemeSetting":
		return sm.GetThemeSetting(), nil
	case "getTheme":
		return optStr(sm.GetTheme()), nil
	case "setTheme":
		return nil, setString(args, 0, sm.SetTheme)
	case "getDefaultThinkingLevel":
		return optStr(string(sm.GetDefaultThinkingLevel())), nil
	case "setDefaultThinkingLevel":
		return nil, setString(args, 0, sm.SetDefaultThinkingLevel)
	case "getModelThinkingLevel":
		p, err := args.String(0)
		if err != nil {
			return nil, err
		}
		id, err := args.String(1)
		if err != nil {
			return nil, err
		}
		return optStr(string(sm.GetModelThinkingLevel(p, id))), nil
	case "getAllModelThinkingLevels":
		return sm.GetAllModelThinkingLevels(), nil
	case "setModelThinkingLevel":
		p, err := args.String(0)
		if err != nil {
			return nil, err
		}
		id, err := args.String(1)
		if err != nil {
			return nil, err
		}
		level, err := args.String(2)
		if err != nil {
			return nil, err
		}
		return nil, sm.SetModelThinkingLevel(p, id, ai.ThinkingLevel(level))
	case "removeModelThinkingLevel":
		p, err := args.String(0)
		if err != nil {
			return nil, err
		}
		id, err := args.String(1)
		if err != nil {
			return nil, err
		}
		return nil, sm.RemoveModelThinkingLevel(p, id)
	case "getTransport":
		return sm.GetTransport(), nil
	case "setTransport":
		return nil, setString(args, 0, sm.SetTransport)
	case "getSessionShareDestination":
		return sm.GetSessionShareDestination(), nil
	case "setSessionShareDestination":
		return nil, setString(args, 0, sm.SetSessionShareDestination)
	case "getCompactionEnabled":
		return sm.GetCompactionEnabled(), nil
	case "setCompactionEnabled":
		return nil, setBool(args, 0, sm.SetCompactionEnabled)
	case "getCompactionReserveTokens":
		provider, id, err := modelArg(args, 0)
		if err != nil {
			return nil, err
		}
		return compactionToken(sm, "reserveTokens", provider, id)
	case "getCompactionKeepRecentTokens":
		provider, id, err := modelArg(args, 0)
		if err != nil {
			return nil, err
		}
		return compactionToken(sm, "keepRecentTokens", provider, id)
	case "getCompactionSettings":
		provider, id, err := modelArg(args, 0)
		if err != nil {
			return nil, err
		}
		return compactionSettings(sm, provider, id)
	case "getBranchSummarySettings":
		cfg := sm.GetBranchSummarySettings()
		return map[string]any{"reserveTokens": cfg.ReserveTokens, "skipPrompt": cfg.SkipPrompt}, nil
	case "getBranchSummarySkipPrompt":
		return sm.GetBranchSummarySkipPrompt(), nil
	case "getRetryEnabled":
		return sm.GetRetryEnabled(), nil
	case "setRetryEnabled":
		return nil, setBool(args, 0, sm.SetRetryEnabled)
	case "getRetrySettings":
		cfg := sm.GetRetrySettings()
		return map[string]any{"enabled": cfg.Enabled, "maxRetries": cfg.MaxRetries, "baseDelayMs": cfg.BaseDelayMs, "maxAgentDelayMs": cfg.MaxAgentDelayMs}, nil
	case "getHttpIdleTimeoutMs":
		return sm.GetHttpIdleTimeoutMs()
	case "setHttpIdleTimeoutMs":
		return nil, setFloat(args, 0, sm.SetHttpIdleTimeoutMs)
	case "getCacheWarmingMode":
		return sm.GetCacheWarmingMode(), nil
	case "setCacheWarmingMode":
		return nil, setString(args, 0, sm.SetCacheWarmingMode)
	case "getProviderRetrySettings":
		cfg := sm.GetProviderRetrySettings()
		out := map[string]any{"maxRetryDelayMs": cfg.MaxRetryDelayMs}
		if cfg.TimeoutMs != nil {
			out["timeoutMs"] = *cfg.TimeoutMs
		}
		if cfg.MaxRetries != nil {
			out["maxRetries"] = *cfg.MaxRetries
		}
		return out, nil
	case "getWebSocketConnectTimeoutMs":
		return sm.GetWebSocketConnectTimeoutMs()
	case "getHideThinkingBlock":
		return sm.GetHideThinkingBlock(), nil
	case "getShowCacheMissNotices":
		return sm.GetShowCacheMissNotices(), nil
	case "getExternalEditorCommand":
		return sm.GetExternalEditorCommand(), nil
	case "setHideThinkingBlock":
		return nil, setBool(args, 0, sm.SetHideThinkingBlock)
	case "setShowCacheMissNotices":
		return nil, setBool(args, 0, sm.SetShowCacheMissNotices)
	case "getShellPath":
		path, err := sm.GetShellPath()
		return optStr(path), err
	case "setShellPath":
		return nil, setOptString(args, 0, func(v *string) error {
			if v == nil {
				return sm.UpdateGlobal(func(s *Settings) { s.ShellPath, s.emptyStrings.shellPath = "", false })
			}
			return sm.SetShellPath(*v)
		})
	case "getQuietStartup":
		return sm.GetQuietStartup(), nil
	case "setQuietStartup":
		return nil, setQuietStartup(args, 0, sm.SetQuietStartup)
	case "getDefaultProjectTrust":
		return sm.GetDefaultProjectTrust(), nil
	case "setDefaultProjectTrust":
		return nil, setString(args, 0, sm.SetDefaultProjectTrust)
	case "getShellCommandPrefix":
		return optStr(sm.GetShellCommandPrefix()), nil
	case "setShellCommandPrefix":
		return nil, setOptString(args, 0, func(v *string) error {
			if v == nil {
				return sm.UpdateGlobal(func(s *Settings) { s.CommandPrefix, s.emptyStrings.commandPrefix = "", false })
			}
			return sm.SetShellCommandPrefix(*v)
		})
	case "getNpmCommand":
		return sm.GetNpmCommand(), nil
	case "setNpmCommand":
		return nil, setStrings(args, 0, sm.SetNpmCommand)
	case "getCollapseChangelog":
		return sm.GetCollapseChangelog(), nil
	case "setCollapseChangelog":
		return nil, setBool(args, 0, sm.SetCollapseChangelog)
	case "getEnableInstallTelemetry":
		return sm.GetEnableInstallTelemetry(), nil
	case "setEnableInstallTelemetry":
		return nil, setBool(args, 0, sm.SetEnableInstallTelemetry)
	case "getEnableAnalytics":
		return sm.GetEnableAnalytics(), nil
	case "getTrackingId":
		return optStr(sm.GetTrackingID()), nil
	case "setEnableAnalytics":
		return nil, setBool(args, 0, sm.SetEnableAnalytics)
	case "getOrCreateDeviceId":
		return sm.GetOrCreateDeviceID(), nil
	case "getPackages":
		return sm.GetPackages(), nil
	case "setPackages":
		return nil, setPackages(args, 0, sm.SetPackages)
	case "setProjectPackages":
		return nil, setPackages(args, 0, sm.SetProjectPackages)
	case "getExtensionPaths":
		return sm.GetExtensionPaths(), nil
	case "setExtensionPaths":
		return nil, setStrings(args, 0, sm.SetExtensionPaths)
	case "setProjectExtensionPaths":
		return nil, setStrings(args, 0, sm.SetProjectExtensionPaths)
	case "getSkillPaths":
		return sm.GetSkillPaths(), nil
	case "setSkillPaths":
		return nil, setStrings(args, 0, sm.SetSkillPaths)
	case "setProjectSkillPaths":
		return nil, setStrings(args, 0, sm.SetProjectSkillPaths)
	case "getPromptTemplatePaths":
		return sm.GetPromptTemplatePaths(), nil
	case "setPromptTemplatePaths":
		return nil, setStrings(args, 0, sm.SetPromptTemplatePaths)
	case "setProjectPromptTemplatePaths":
		return nil, setStrings(args, 0, sm.SetProjectPromptTemplatePaths)
	case "getThemePaths":
		return sm.GetThemePaths(), nil
	case "setThemePaths":
		return nil, setStrings(args, 0, sm.SetThemePaths)
	case "setProjectThemePaths":
		return nil, setStrings(args, 0, sm.SetProjectThemePaths)
	case "getEnableSkillCommands":
		return sm.GetEnableSkillCommands(), nil
	case "setEnableSkillCommands":
		return nil, setBool(args, 0, sm.SetEnableSkillCommands)
	case "getThinkingBudgets":
		return sm.GetThinkingBudgets(), nil
	case "getTerminalCapabilityOverrides":
		return terminalCapabilityOverrides(sm), nil
	case "getShowImages":
		return sm.GetShowImages(), nil
	case "setShowImages":
		return nil, setBool(args, 0, sm.SetShowImages)
	case "getImageWidthCells":
		return sm.GetImageWidthCells(), nil
	case "setImageWidthCells":
		return nil, setInt(args, 0, sm.SetImageWidthCells)
	case "getClearOnShrink":
		return sm.GetClearOnShrink(), nil
	case "setClearOnShrink":
		return nil, setBool(args, 0, sm.SetClearOnShrink)
	case "getShowTerminalProgress":
		return sm.GetShowTerminalProgress(), nil
	case "setShowTerminalProgress":
		return nil, setBool(args, 0, sm.SetShowTerminalProgress)
	case "getTuiMode":
		return sm.GetTuiMode(), nil
	case "setTuiMode":
		return nil, setString(args, 0, sm.SetTuiMode)
	case "getFullscreenExitOutput":
		return sm.GetFullscreenExitOutput(), nil
	case "setFullscreenExitOutput":
		return nil, setString(args, 0, sm.SetFullscreenExitOutput)
	case "getFullscreenScrollbar":
		return sm.GetFullscreenScrollbar(), nil
	case "setFullscreenScrollbar":
		return nil, setString(args, 0, sm.SetFullscreenScrollbar)
	case "getFullscreenCopyOnSelect":
		return sm.GetFullscreenCopyOnSelect(), nil
	case "setFullscreenCopyOnSelect":
		return nil, setBool(args, 0, sm.SetFullscreenCopyOnSelect)
	case "getFullscreenWheelScrollLines":
		return wheelScrollLines(sm.GetFullscreenWheelScrollLines()), nil
	case "setFullscreenWheelScrollLines":
		return nil, setWheelScrollLines(args, 0, sm.SetFullscreenWheelScrollLines)
	case "getImageAutoResize":
		return sm.GetImageAutoResize(), nil
	case "setImageAutoResize":
		return nil, setBool(args, 0, sm.SetImageAutoResize)
	case "getBlockImages":
		return sm.GetBlockImages(), nil
	case "setBlockImages":
		return nil, setBool(args, 0, sm.SetBlockImages)
	case "getEnabledModels":
		return sm.GetEnabledModels(), nil
	case "getDefaultTools":
		return sm.GetDefaultTools(), nil
	case "setEnabledModels":
		return nil, setStrings(args, 0, sm.SetEnabledModels)
	case "getDoubleEscapeAction":
		return sm.GetDoubleEscapeAction(), nil
	case "setDoubleEscapeAction":
		return nil, setString(args, 0, sm.SetDoubleEscapeAction)
	case "getTreeFilterMode":
		return sm.GetTreeFilterMode(), nil
	case "setTreeFilterMode":
		return nil, setString(args, 0, sm.SetTreeFilterMode)
	case "getShowHardwareCursor":
		return sm.GetShowHardwareCursor(), nil
	case "setShowHardwareCursor":
		return nil, setBool(args, 0, sm.SetShowHardwareCursor)
	case "getEditorPaddingX":
		return sm.GetEditorPaddingX(), nil
	case "setEditorPaddingX":
		return nil, setInt(args, 0, sm.SetEditorPaddingX)
	case "getOutputPad":
		return sm.GetOutputPad(), nil
	case "setOutputPad":
		return nil, setInt(args, 0, sm.SetOutputPad)
	case "getAutocompleteMaxVisible":
		return sm.GetAutocompleteMaxVisible(), nil
	case "setAutocompleteMaxVisible":
		return nil, setInt(args, 0, sm.SetAutocompleteMaxVisible)
	case "getCodeBlockIndent":
		return sm.GetCodeBlockIndent(), nil
	case "getMermaidRenderingMode":
		return sm.GetMermaidRenderingMode(), nil
	case "setMermaidRenderingMode":
		return nil, setString(args, 0, sm.SetMermaidRenderingMode)
	case "getWarnings":
		return readMerged(sm, func(s *Settings) WarningSettings {
			if s.Warnings == nil {
				return WarningSettings{}
			}
			return *s.Warnings
		}), nil
	case "setWarnings":
		return nil, setWarnings(args, 0, sm.SetWarnings)
	}
	return nil, errors.New("SettingsManager has no method " + method)
}

var _ subprocess.SessionGraphObject = (*GraphSettingsManager)(nil)

// graphSettingsStorage is a SettingsStorage whose withLock the extension implements. Its callback runs in two steps so that Pi's fn, which is the host's, runs between them while the extension's withLock holds its lock: ["begin", scope] answers {"current"} from inside the extension's withLock, and ["finish", {"write", "text", "error"}] hands back what fn returned and lets that withLock return.
type graphSettingsStorage struct {
	mu       sync.Mutex
	scope    subprocess.SessionGraphScope
	callback string
}

func (g *graphSettingsStorage) WithLock(scope string, fn func(current *string) (*string, error)) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	//portlint:allow ctxbackground Pi's SettingsStorage.withLock is synchronous and carries no signal; the extension's lock callbacks run to their end
	ctx := context.Background()
	begin, err := json.Marshal([]any{"begin", scope})
	if err != nil {
		return err
	}
	answer, err := g.scope.Callback(ctx, g.callback, begin)
	if err != nil {
		return err
	}
	var current struct {
		Current *string `json:"current"`
	}
	if err := json.Unmarshal(answer, &current); err != nil {
		return fmt.Errorf("settings storage answered %s: %w", answer, err)
	}
	next, fnErr := fn(current.Current)
	finish := map[string]any{"write": false}
	switch {
	case fnErr != nil:
		finish = map[string]any{"error": fnErr.Error()}
	case next != nil:
		finish = map[string]any{"write": true, "text": *next}
	}
	encoded, err := json.Marshal([]any{"finish", finish})
	if err != nil {
		return err
	}
	if _, err := g.scope.Callback(ctx, g.callback, encoded); err != nil {
		return err
	}
	return fnErr
}
