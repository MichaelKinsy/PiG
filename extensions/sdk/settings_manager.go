package sdk

import (
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// SettingsManager is Pi's `SettingsManager` class (core/settings-manager.ts): the global and project settings an extension reads and writes, or in-memory settings it builds for a child session. The host owns the object; a SettingsManager passed to CreateAgentSession is the very settings object the child session reads. Every method is Pi's under its Go name; a nil pointer result is Pi's undefined.
type SettingsManager struct{ object *graphObject }

// SettingsManagerCreateOptions is Pi's SettingsManagerCreateOptions. A nil ProjectTrusted is Pi's undefined (true).
type SettingsManagerCreateOptions struct {
	ProjectTrusted *bool `json:"projectTrusted,omitempty"`
}

// SettingsError is Pi's SettingsError: a failure to load or write one scope's settings file.
type SettingsError struct {
	Scope string
	Path  *string
	Error error
}

// SettingsManagerCreate is `SettingsManager.create(cwd, agentDir = getAgentDir(), options = {})`: settings loaded from the files of agentDir (global) and cwd (project). A nil agentDir is the host's agent directory.
func SettingsManagerCreate(c Context, cwd string, agentDir *string, options *SettingsManagerCreateOptions) (SettingsManager, error) {
	object, err := newGraphObject(c, "SettingsManager", "create", cwd, agentDir, options)
	return SettingsManager{object: object}, err
}

// SettingsStorage is Pi's SettingsStorage: the backend of [SettingsManagerFromStorage]. WithLock runs fn on the current text of the scope's settings ("global" or "project"; nil when there is none) while it holds its lock. fn returns the text to store, nil to leave it, or an error (a settings file that does not parse); WithLock stores the text and returns the error fn returned.
type SettingsStorage interface {
	WithLock(scope string, fn func(current *string) (*string, error)) error
}

type storageFinish struct {
	Write bool   `json:"write"`
	Text  string `json:"text"`
	Error string `json:"error"`
}

// pendingLock is one WithLock of the extension's storage that the host drives in two calls (host protocol: graphSettingsStorage).
type pendingLock struct {
	current chan *string
	finish  chan storageFinish
	done    chan error
}

// storageCallback is the function the host runs for every WithLock of the storage: ["begin", scope] starts the storage's WithLock and answers its current text, and ["finish", result] gives its fn the host's result and waits for WithLock to return.
func storageCallback(storage SettingsStorage) graphCallback {
	var mu sync.Mutex
	var pending *pendingLock
	return func(args []json.RawMessage) (any, error) {
		var op string
		if len(args) == 0 || json.Unmarshal(args[0], &op) != nil {
			return nil, errors.New("settings storage callback: no operation")
		}
		switch op {
		case "begin":
			var scope string
			if len(args) < 2 || json.Unmarshal(args[1], &scope) != nil {
				return nil, errors.New("settings storage callback: no scope")
			}
			lock := &pendingLock{current: make(chan *string, 1), finish: make(chan storageFinish, 1), done: make(chan error, 1)}
			go func() {
				lock.done <- storage.WithLock(scope, func(current *string) (*string, error) {
					lock.current <- current
					result := <-lock.finish
					if result.Error != "" {
						return nil, errors.New(result.Error)
					}
					if result.Write {
						return &result.Text, nil
					}
					return nil, nil
				})
			}()
			select {
			case current := <-lock.current:
				mu.Lock()
				pending = lock
				mu.Unlock()
				return map[string]any{"current": current}, nil
			case err := <-lock.done:
				if err == nil {
					err = errors.New("settings storage: WithLock returned without calling fn")
				}
				return nil, err
			}
		case "finish":
			mu.Lock()
			lock := pending
			pending = nil
			mu.Unlock()
			if lock == nil {
				return nil, errors.New("settings storage callback: finish without begin")
			}
			var result storageFinish
			if len(args) > 1 {
				if err := json.Unmarshal(args[1], &result); err != nil {
					return nil, err
				}
			}
			lock.finish <- result
			return nil, <-lock.done
		}
		return nil, fmt.Errorf("settings storage callback: unknown operation %q", op)
	}
}

// SettingsManagerFromStorage is `SettingsManager.fromStorage(storage, options = {})`: settings kept by storage, an object of this process the host calls back whenever the manager reads or writes a scope.
func SettingsManagerFromStorage(c Context, storage SettingsStorage, options *SettingsManagerCreateOptions) (SettingsManager, error) {
	object, err := newGraphObject(c, "SettingsManager", "fromStorage", map[string]any{"withLock": c.callbackID(storageCallback(storage))}, options)
	return SettingsManager{object: object}, err
}

// SettingsManagerInMemory is `SettingsManager.inMemory(settings = {}, options = {})`: settings that never touch a file. settings is Pi's Partial<Settings> as a JSON object.
func SettingsManagerInMemory(c Context, settings map[string]any, options *SettingsManagerCreateOptions) (SettingsManager, error) {
	object, err := newGraphObject(c, "SettingsManager", "inMemory", settings, options)
	return SettingsManager{object: object}, err
}

// Handle is the host's name for the settings, which CreateAgentSession puts on the wire.
func (s SettingsManager) Handle() string { return s.object.Handle() }

// DrainErrors returns the errors loading or writing the settings files raised since the last call and forgets them.
func (s SettingsManager) DrainErrors() ([]SettingsError, error) {
	drained, err := graphMethodCall[[]struct {
		Scope string  `json:"scope"`
		Path  *string `json:"path"`
		Error string  `json:"error"`
	}](s.object, "drainErrors")
	if err != nil {
		return nil, err
	}
	out := make([]SettingsError, 0, len(drained))
	for _, entry := range drained {
		out = append(out, SettingsError{Scope: entry.Scope, Path: entry.Path, Error: errors.New(entry.Error)})
	}
	return out, nil
}

func (s SettingsManager) GetSettings() (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getSettings")
}

func (s SettingsManager) GetGlobalSettings() (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getGlobalSettings")
}

func (s SettingsManager) GetProjectSettings() (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getProjectSettings")
}

func (s SettingsManager) IsProjectTrusted() (bool, error) {
	return graphMethodCall[bool](s.object, "isProjectTrusted")
}

func (s SettingsManager) SetProjectTrusted(trusted bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setProjectTrusted", trusted)
	return err
}

func (s SettingsManager) Reload() error {
	_, err := graphMethodAsync[graphDone](s.object, "reload")
	return err
}

func (s SettingsManager) ApplyOverrides(overrides map[string]any) error {
	_, err := graphMethodCall[graphDone](s.object, "applyOverrides", overrides)
	return err
}

func (s SettingsManager) Flush() error {
	_, err := graphMethodAsync[graphDone](s.object, "flush")
	return err
}

func (s SettingsManager) GetLastChangelogVersion() (*string, error) {
	return graphMethodCall[*string](s.object, "getLastChangelogVersion")
}

func (s SettingsManager) SetLastChangelogVersion(version string) error {
	_, err := graphMethodCall[graphDone](s.object, "setLastChangelogVersion", version)
	return err
}

func (s SettingsManager) GetSessionDir() (*string, error) {
	return graphMethodCall[*string](s.object, "getSessionDir")
}

func (s SettingsManager) GetDefaultProvider() (*string, error) {
	return graphMethodCall[*string](s.object, "getDefaultProvider")
}

func (s SettingsManager) GetDefaultModel() (*string, error) {
	return graphMethodCall[*string](s.object, "getDefaultModel")
}

func (s SettingsManager) SetDefaultProvider(provider string) error {
	_, err := graphMethodCall[graphDone](s.object, "setDefaultProvider", provider)
	return err
}

func (s SettingsManager) SetDefaultModel(modelId string) error {
	_, err := graphMethodCall[graphDone](s.object, "setDefaultModel", modelId)
	return err
}

func (s SettingsManager) SetDefaultModelAndProvider(provider string, modelId string) error {
	_, err := graphMethodCall[graphDone](s.object, "setDefaultModelAndProvider", provider, modelId)
	return err
}

func (s SettingsManager) GetSteeringMode() (string, error) {
	return graphMethodCall[string](s.object, "getSteeringMode")
}

func (s SettingsManager) SetSteeringMode(mode string) error {
	_, err := graphMethodCall[graphDone](s.object, "setSteeringMode", mode)
	return err
}

func (s SettingsManager) GetFollowUpMode() (string, error) {
	return graphMethodCall[string](s.object, "getFollowUpMode")
}

func (s SettingsManager) SetFollowUpMode(mode string) error {
	_, err := graphMethodCall[graphDone](s.object, "setFollowUpMode", mode)
	return err
}

func (s SettingsManager) GetThemeSetting() (*string, error) {
	return graphMethodCall[*string](s.object, "getThemeSetting")
}

func (s SettingsManager) GetTheme() (*string, error) {
	return graphMethodCall[*string](s.object, "getTheme")
}

func (s SettingsManager) SetTheme(theme string) error {
	_, err := graphMethodCall[graphDone](s.object, "setTheme", theme)
	return err
}

func (s SettingsManager) GetDefaultThinkingLevel() (*string, error) {
	return graphMethodCall[*string](s.object, "getDefaultThinkingLevel")
}

func (s SettingsManager) SetDefaultThinkingLevel(level string) error {
	_, err := graphMethodCall[graphDone](s.object, "setDefaultThinkingLevel", level)
	return err
}

func (s SettingsManager) GetModelThinkingLevel(provider string, modelId string) (*string, error) {
	return graphMethodCall[*string](s.object, "getModelThinkingLevel", provider, modelId)
}

func (s SettingsManager) GetAllModelThinkingLevels() (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getAllModelThinkingLevels")
}

func (s SettingsManager) SetModelThinkingLevel(provider string, modelId string, level string) error {
	_, err := graphMethodCall[graphDone](s.object, "setModelThinkingLevel", provider, modelId, level)
	return err
}

func (s SettingsManager) RemoveModelThinkingLevel(provider string, modelId string) error {
	_, err := graphMethodCall[graphDone](s.object, "removeModelThinkingLevel", provider, modelId)
	return err
}

func (s SettingsManager) GetTransport() (string, error) {
	return graphMethodCall[string](s.object, "getTransport")
}

func (s SettingsManager) SetTransport(transport string) error {
	_, err := graphMethodCall[graphDone](s.object, "setTransport", transport)
	return err
}

func (s SettingsManager) GetSessionShareDestination() (string, error) {
	return graphMethodCall[string](s.object, "getSessionShareDestination")
}

func (s SettingsManager) SetSessionShareDestination(destination string) error {
	_, err := graphMethodCall[graphDone](s.object, "setSessionShareDestination", destination)
	return err
}

func (s SettingsManager) GetCompactionEnabled() (bool, error) {
	return graphMethodCall[bool](s.object, "getCompactionEnabled")
}

func (s SettingsManager) SetCompactionEnabled(enabled bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setCompactionEnabled", enabled)
	return err
}

func (s SettingsManager) GetCompactionReserveTokens(model map[string]any) (int, error) {
	return graphMethodCall[int](s.object, "getCompactionReserveTokens", model)
}

func (s SettingsManager) GetCompactionKeepRecentTokens(model map[string]any) (int, error) {
	return graphMethodCall[int](s.object, "getCompactionKeepRecentTokens", model)
}

func (s SettingsManager) GetCompactionSettings(model map[string]any) (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getCompactionSettings", model)
}

func (s SettingsManager) GetBranchSummarySettings() (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getBranchSummarySettings")
}

func (s SettingsManager) GetBranchSummarySkipPrompt() (bool, error) {
	return graphMethodCall[bool](s.object, "getBranchSummarySkipPrompt")
}

func (s SettingsManager) GetRetryEnabled() (bool, error) {
	return graphMethodCall[bool](s.object, "getRetryEnabled")
}

func (s SettingsManager) SetRetryEnabled(enabled bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setRetryEnabled", enabled)
	return err
}

func (s SettingsManager) GetRetrySettings() (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getRetrySettings")
}

func (s SettingsManager) GetHttpIdleTimeoutMs() (float64, error) {
	return graphMethodCall[float64](s.object, "getHttpIdleTimeoutMs")
}

func (s SettingsManager) SetHttpIdleTimeoutMs(timeoutMs float64) error {
	_, err := graphMethodCall[graphDone](s.object, "setHttpIdleTimeoutMs", timeoutMs)
	return err
}

func (s SettingsManager) GetCacheWarmingMode() (string, error) {
	return graphMethodCall[string](s.object, "getCacheWarmingMode")
}

func (s SettingsManager) SetCacheWarmingMode(mode string) error {
	_, err := graphMethodCall[graphDone](s.object, "setCacheWarmingMode", mode)
	return err
}

func (s SettingsManager) GetProviderRetrySettings() (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getProviderRetrySettings")
}

func (s SettingsManager) GetWebSocketConnectTimeoutMs() (*float64, error) {
	return graphMethodCall[*float64](s.object, "getWebSocketConnectTimeoutMs")
}

func (s SettingsManager) GetHideThinkingBlock() (bool, error) {
	return graphMethodCall[bool](s.object, "getHideThinkingBlock")
}

func (s SettingsManager) GetShowCacheMissNotices() (bool, error) {
	return graphMethodCall[bool](s.object, "getShowCacheMissNotices")
}

func (s SettingsManager) GetExternalEditorCommand() (string, error) {
	return graphMethodCall[string](s.object, "getExternalEditorCommand")
}

func (s SettingsManager) SetHideThinkingBlock(hide bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setHideThinkingBlock", hide)
	return err
}

func (s SettingsManager) SetShowCacheMissNotices(show bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setShowCacheMissNotices", show)
	return err
}

func (s SettingsManager) GetShellPath() (*string, error) {
	return graphMethodCall[*string](s.object, "getShellPath")
}

func (s SettingsManager) SetShellPath(path *string) error {
	_, err := graphMethodCall[graphDone](s.object, "setShellPath", path)
	return err
}

func (s SettingsManager) GetQuietStartup() (any, error) {
	return graphMethodCall[any](s.object, "getQuietStartup")
}

func (s SettingsManager) SetQuietStartup(quiet any) error {
	_, err := graphMethodCall[graphDone](s.object, "setQuietStartup", quiet)
	return err
}

func (s SettingsManager) GetDefaultProjectTrust() (string, error) {
	return graphMethodCall[string](s.object, "getDefaultProjectTrust")
}

func (s SettingsManager) SetDefaultProjectTrust(defaultProjectTrust string) error {
	_, err := graphMethodCall[graphDone](s.object, "setDefaultProjectTrust", defaultProjectTrust)
	return err
}

func (s SettingsManager) GetShellCommandPrefix() (*string, error) {
	return graphMethodCall[*string](s.object, "getShellCommandPrefix")
}

func (s SettingsManager) SetShellCommandPrefix(prefix *string) error {
	_, err := graphMethodCall[graphDone](s.object, "setShellCommandPrefix", prefix)
	return err
}

func (s SettingsManager) GetNpmCommand() ([]string, error) {
	return graphMethodCall[[]string](s.object, "getNpmCommand")
}

func (s SettingsManager) SetNpmCommand(command []string) error {
	_, err := graphMethodCall[graphDone](s.object, "setNpmCommand", command)
	return err
}

func (s SettingsManager) GetCollapseChangelog() (bool, error) {
	return graphMethodCall[bool](s.object, "getCollapseChangelog")
}

func (s SettingsManager) SetCollapseChangelog(collapse bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setCollapseChangelog", collapse)
	return err
}

func (s SettingsManager) GetEnableInstallTelemetry() (bool, error) {
	return graphMethodCall[bool](s.object, "getEnableInstallTelemetry")
}

func (s SettingsManager) SetEnableInstallTelemetry(enabled bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setEnableInstallTelemetry", enabled)
	return err
}

func (s SettingsManager) GetEnableAnalytics() (bool, error) {
	return graphMethodCall[bool](s.object, "getEnableAnalytics")
}

func (s SettingsManager) GetTrackingID() (*string, error) {
	return graphMethodCall[*string](s.object, "getTrackingId")
}

func (s SettingsManager) SetEnableAnalytics(enabled bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setEnableAnalytics", enabled)
	return err
}

func (s SettingsManager) GetOrCreateDeviceID() (string, error) {
	return graphMethodCall[string](s.object, "getOrCreateDeviceId")
}

func (s SettingsManager) GetPackages() ([]any, error) {
	return graphMethodCall[[]any](s.object, "getPackages")
}

func (s SettingsManager) SetPackages(packages []any) error {
	_, err := graphMethodCall[graphDone](s.object, "setPackages", packages)
	return err
}

func (s SettingsManager) SetProjectPackages(packages []any) error {
	_, err := graphMethodCall[graphDone](s.object, "setProjectPackages", packages)
	return err
}

func (s SettingsManager) GetExtensionPaths() ([]string, error) {
	return graphMethodCall[[]string](s.object, "getExtensionPaths")
}

func (s SettingsManager) SetExtensionPaths(paths []string) error {
	_, err := graphMethodCall[graphDone](s.object, "setExtensionPaths", paths)
	return err
}

func (s SettingsManager) SetProjectExtensionPaths(paths []string) error {
	_, err := graphMethodCall[graphDone](s.object, "setProjectExtensionPaths", paths)
	return err
}

func (s SettingsManager) GetSkillPaths() ([]string, error) {
	return graphMethodCall[[]string](s.object, "getSkillPaths")
}

func (s SettingsManager) SetSkillPaths(paths []string) error {
	_, err := graphMethodCall[graphDone](s.object, "setSkillPaths", paths)
	return err
}

func (s SettingsManager) SetProjectSkillPaths(paths []string) error {
	_, err := graphMethodCall[graphDone](s.object, "setProjectSkillPaths", paths)
	return err
}

func (s SettingsManager) GetPromptTemplatePaths() ([]string, error) {
	return graphMethodCall[[]string](s.object, "getPromptTemplatePaths")
}

func (s SettingsManager) SetPromptTemplatePaths(paths []string) error {
	_, err := graphMethodCall[graphDone](s.object, "setPromptTemplatePaths", paths)
	return err
}

func (s SettingsManager) SetProjectPromptTemplatePaths(paths []string) error {
	_, err := graphMethodCall[graphDone](s.object, "setProjectPromptTemplatePaths", paths)
	return err
}

func (s SettingsManager) GetThemePaths() ([]string, error) {
	return graphMethodCall[[]string](s.object, "getThemePaths")
}

func (s SettingsManager) SetThemePaths(paths []string) error {
	_, err := graphMethodCall[graphDone](s.object, "setThemePaths", paths)
	return err
}

func (s SettingsManager) SetProjectThemePaths(paths []string) error {
	_, err := graphMethodCall[graphDone](s.object, "setProjectThemePaths", paths)
	return err
}

func (s SettingsManager) GetEnableSkillCommands() (bool, error) {
	return graphMethodCall[bool](s.object, "getEnableSkillCommands")
}

func (s SettingsManager) SetEnableSkillCommands(enabled bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setEnableSkillCommands", enabled)
	return err
}

func (s SettingsManager) GetThinkingBudgets() (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getThinkingBudgets")
}

func (s SettingsManager) GetTerminalCapabilityOverrides() (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getTerminalCapabilityOverrides")
}

func (s SettingsManager) GetShowImages() (bool, error) {
	return graphMethodCall[bool](s.object, "getShowImages")
}

func (s SettingsManager) SetShowImages(show bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setShowImages", show)
	return err
}

func (s SettingsManager) GetImageWidthCells() (int, error) {
	return graphMethodCall[int](s.object, "getImageWidthCells")
}

func (s SettingsManager) SetImageWidthCells(width int) error {
	_, err := graphMethodCall[graphDone](s.object, "setImageWidthCells", width)
	return err
}

func (s SettingsManager) GetClearOnShrink() (bool, error) {
	return graphMethodCall[bool](s.object, "getClearOnShrink")
}

func (s SettingsManager) SetClearOnShrink(enabled bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setClearOnShrink", enabled)
	return err
}

func (s SettingsManager) GetShowTerminalProgress() (bool, error) {
	return graphMethodCall[bool](s.object, "getShowTerminalProgress")
}

func (s SettingsManager) SetShowTerminalProgress(enabled bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setShowTerminalProgress", enabled)
	return err
}

func (s SettingsManager) GetTuiMode() (string, error) {
	return graphMethodCall[string](s.object, "getTuiMode")
}

func (s SettingsManager) SetTuiMode(mode string) error {
	_, err := graphMethodCall[graphDone](s.object, "setTuiMode", mode)
	return err
}

func (s SettingsManager) GetFullscreenExitOutput() (string, error) {
	return graphMethodCall[string](s.object, "getFullscreenExitOutput")
}

func (s SettingsManager) SetFullscreenExitOutput(output string) error {
	_, err := graphMethodCall[graphDone](s.object, "setFullscreenExitOutput", output)
	return err
}

func (s SettingsManager) GetFullscreenScrollbar() (string, error) {
	return graphMethodCall[string](s.object, "getFullscreenScrollbar")
}

func (s SettingsManager) SetFullscreenScrollbar(mode string) error {
	_, err := graphMethodCall[graphDone](s.object, "setFullscreenScrollbar", mode)
	return err
}

func (s SettingsManager) GetFullscreenCopyOnSelect() (bool, error) {
	return graphMethodCall[bool](s.object, "getFullscreenCopyOnSelect")
}

func (s SettingsManager) SetFullscreenCopyOnSelect(enabled bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setFullscreenCopyOnSelect", enabled)
	return err
}

func (s SettingsManager) GetFullscreenWheelScrollLines() (any, error) {
	return graphMethodCall[any](s.object, "getFullscreenWheelScrollLines")
}

func (s SettingsManager) SetFullscreenWheelScrollLines(lines any) error {
	_, err := graphMethodCall[graphDone](s.object, "setFullscreenWheelScrollLines", lines)
	return err
}

func (s SettingsManager) GetImageAutoResize() (bool, error) {
	return graphMethodCall[bool](s.object, "getImageAutoResize")
}

func (s SettingsManager) SetImageAutoResize(enabled bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setImageAutoResize", enabled)
	return err
}

func (s SettingsManager) GetBlockImages() (bool, error) {
	return graphMethodCall[bool](s.object, "getBlockImages")
}

func (s SettingsManager) SetBlockImages(blocked bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setBlockImages", blocked)
	return err
}

func (s SettingsManager) GetEnabledModels() ([]string, error) {
	return graphMethodCall[[]string](s.object, "getEnabledModels")
}

func (s SettingsManager) GetDefaultTools() ([]string, error) {
	return graphMethodCall[[]string](s.object, "getDefaultTools")
}

func (s SettingsManager) SetEnabledModels(patterns []string) error {
	_, err := graphMethodCall[graphDone](s.object, "setEnabledModels", patterns)
	return err
}

func (s SettingsManager) GetDoubleEscapeAction() (string, error) {
	return graphMethodCall[string](s.object, "getDoubleEscapeAction")
}

func (s SettingsManager) SetDoubleEscapeAction(action string) error {
	_, err := graphMethodCall[graphDone](s.object, "setDoubleEscapeAction", action)
	return err
}

func (s SettingsManager) GetTreeFilterMode() (string, error) {
	return graphMethodCall[string](s.object, "getTreeFilterMode")
}

func (s SettingsManager) SetTreeFilterMode(mode string) error {
	_, err := graphMethodCall[graphDone](s.object, "setTreeFilterMode", mode)
	return err
}

func (s SettingsManager) GetShowHardwareCursor() (bool, error) {
	return graphMethodCall[bool](s.object, "getShowHardwareCursor")
}

func (s SettingsManager) SetShowHardwareCursor(enabled bool) error {
	_, err := graphMethodCall[graphDone](s.object, "setShowHardwareCursor", enabled)
	return err
}

func (s SettingsManager) GetEditorPaddingX() (int, error) {
	return graphMethodCall[int](s.object, "getEditorPaddingX")
}

func (s SettingsManager) SetEditorPaddingX(padding int) error {
	_, err := graphMethodCall[graphDone](s.object, "setEditorPaddingX", padding)
	return err
}

func (s SettingsManager) GetOutputPad() (int, error) {
	return graphMethodCall[int](s.object, "getOutputPad")
}

func (s SettingsManager) SetOutputPad(padding int) error {
	_, err := graphMethodCall[graphDone](s.object, "setOutputPad", padding)
	return err
}

func (s SettingsManager) GetAutocompleteMaxVisible() (int, error) {
	return graphMethodCall[int](s.object, "getAutocompleteMaxVisible")
}

func (s SettingsManager) SetAutocompleteMaxVisible(maxVisible int) error {
	_, err := graphMethodCall[graphDone](s.object, "setAutocompleteMaxVisible", maxVisible)
	return err
}

func (s SettingsManager) GetCodeBlockIndent() (string, error) {
	return graphMethodCall[string](s.object, "getCodeBlockIndent")
}

func (s SettingsManager) GetMermaidRenderingMode() (string, error) {
	return graphMethodCall[string](s.object, "getMermaidRenderingMode")
}

func (s SettingsManager) SetMermaidRenderingMode(mode string) error {
	_, err := graphMethodCall[graphDone](s.object, "setMermaidRenderingMode", mode)
	return err
}

func (s SettingsManager) GetWarnings() (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getWarnings")
}

func (s SettingsManager) SetWarnings(warnings map[string]any) error {
	_, err := graphMethodCall[graphDone](s.object, "setWarnings", warnings)
	return err
}
