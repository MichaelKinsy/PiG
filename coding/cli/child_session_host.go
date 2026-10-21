// pig-specific: the headless context a child agent session gives the extensions its resource loader loaded.

package cli

import (
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// bindChildExtensionHost binds session to the bridge of the Extension Host that loaded its extensions, so that their contexts read the child's state as Pi's do (agent-session.ts _bindExtensionCore, which binds the Session's model, scoped models, idle state, signal, tools, commands, session reads and actions in every mode). Nothing binds a UI, so ctx.hasUI is false and the dialogs answer nothing. The returned function unbinds what holds the session.
//
// It binds what print mode binds for the same headless context (print_mode.go bind). It returns nil for a session with no extension host.
func bindChildExtensionHost(bridge *subprocess.UIBridge, session *coding.Session) func() {
	if bridge == nil || session == nil {
		return nil
	}
	current := func() *coding.Session { return session }
	services := session.Services()
	detachModelRegistry := wireSubprocessModelRegistry(bridge, session, services)
	stopRunSignal := watchSessionRunSignal(bridge, session)
	bindSessionReadActions(bridge, current, session.CWD(), session.Inner().GetSessionDir())
	bindSessionAppendEntry(bridge, current, nil)
	flagValues := services.ExtensionFlagValues()
	bindSessionExtensionActions(nil, bridge, current, extension.ContextActions{
		ModelRegistry:    services.Registry(),
		IsProjectTrusted: services.SettingsManager().IsProjectTrusted,
		GetFlagValue:     func(name string) any { return flagValues[name] },
	}, nil)
	// upstream: agent-session.ts:3361-3390 binds getAllTools and getCommands to the Session in every mode.
	bridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
		all := session.GetAllTools()
		out := make([]subprocess.ToolInfo, len(all))
		for i, info := range all {
			out[i] = subprocess.ToolInfo{
				Name: info.Name, Description: info.Description, Parameters: info.Parameters, PromptGuidelines: info.PromptGuidelines,
				Exposure: info.Exposure, Namespace: info.Namespace, Annotations: info.Annotations, ReachesTools: subprocess.ToolInfoReachesTools(info.ReachesTools),
				SourceInfo: info.SourceInfo, Source: info.Source,
			}
		}
		return out
	})
	bridge.SetHostAction("getCommands", func() []subprocess.CommandInfo {
		commands := session.SlashCommands()
		out := make([]subprocess.CommandInfo, len(commands))
		for i, command := range commands {
			out[i] = subprocess.CommandInfo{Name: command.Name, Description: command.Description, Source: command.Source, SourceInfo: command.SourceInfo}
		}
		return out
	})
	return func() {
		detachModelRegistry()
		stopRunSignal()
	}
}
