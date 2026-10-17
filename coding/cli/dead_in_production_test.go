package cli

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Helpers that production no longer calls and that tests still exercise: they live in the test binary only.

func newConfigSelector(cwd, agentDir string, sm *codingagent.SettingsManager) (*tui.ConfigSelectorComponent, error) {
	global := codingagent.NewSettingsManagerWithOptions(cwd, agentDir, codingagent.SettingsManagerCreateOptions{ProjectTrusted: new(false)})
	return newScopedConfigSelector(cwd, agentDir, global, sm, false, sm.IsProjectTrusted(), nil)
}

func pathToExtConfig(path string) (subprocess.ExtConfig, bool) {
	configs := pathToExtConfigs(path)
	if len(configs) != 1 || configs[0].ResolveError() != nil {
		return subprocess.ExtConfig{}, false
	}
	return configs[0], true
}

func resetAutomaticExtensionCacheGC() {
	extensionCacheGC.mu.Lock()
	defer extensionCacheGC.mu.Unlock()
	extensionCacheGC.scheduled, extensionCacheGC.configs = false, nil
	extensionCacheGC.started, extensionCacheGC.cancel, extensionCacheGC.done = false, nil, nil
}

func (c headlessCommandCatalog) routePrompt(ctx context.Context, message string) (string, bool) {
	if name, args, ok := c.extensionCommand(message); ok {
		return "", c.executeCommand(ctx, name, args)
	}
	return c.expandPrompt(message), false
}

func rpcBeforeSessionSwitch(ctx context.Context, runner rpcEventRunner, reason, target string) (bool, error) {
	if runner == nil {
		return false, nil
	}
	result, err := runner.Emit(ctx, extension.SessionBeforeSwitchEvent{
		Type:              codingagent.EventSessionBeforeSwitch,
		Reason:            reason,
		TargetSessionFile: target,
	})
	if err != nil {
		return false, err
	}
	return rpcResultCancelled(result), nil
}

func rpcBeforeSessionFork(ctx context.Context, runner rpcEventRunner, entryID, position string) (bool, error) {
	if runner == nil {
		return false, nil
	}
	result, err := runner.Emit(ctx, extension.SessionBeforeForkEvent{
		Type:     codingagent.EventSessionBeforeFork,
		EntryID:  entryID,
		Position: position,
	})
	if err != nil {
		return false, err
	}
	return rpcResultCancelled(result), nil
}

type rpcEventRunner interface {
	Emit(context.Context, any) (any, error)
}

func rpcResultCancelled(result any) bool {
	switch value := result.(type) {
	case extension.SessionBeforeSwitchResult:
		return value.Cancel
	case *extension.SessionBeforeSwitchResult:
		return value != nil && value.Cancel
	case extension.SessionBeforeForkResult:
		return value.Cancel
	case *extension.SessionBeforeForkResult:
		return value != nil && value.Cancel
	}
	data, err := json.Marshal(result)
	if err != nil {
		return false
	}
	var value struct {
		Cancel bool `json:"cancel"`
	}
	return json.Unmarshal(data, &value) == nil && value.Cancel
}
