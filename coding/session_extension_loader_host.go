package coding

// Ports packages/coding-agent/src/core/agent-session.ts _bindExtensionCore (:3275-3408) for the Extension Host a resource loader started.

import (
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// reportExtensionEntry is the part of upstream's appendEntry action (agent-session.ts:3321-3327) that follows the append: the entry reaches the Session's listeners as an entry_appended event. Only pi.appendEntry emits it; ctx.sessionManager.appendCustomEntry writes the log alone.
func (s *Session) reportExtensionEntry(entry icodingagent.CustomEntry, direct *subprocess.DirectEntryAppend) error {
	if direct != nil && !direct.Event {
		return nil
	}
	if stored, ok := s.Inner().GetEntry(entry.ID); ok {
		s.emitEvent(agent.EntryAppendedEvent{Entry: stored.Raw()})
	}
	return nil
}

// bindLoaderExtensionHost binds the Session's state to the bridge of the Extension Host that loaded its resource loader's extensions, so that their contexts read the Session as Pi's do: _bindExtensionCore binds the Session's model, scoped models, idle state, signal, tools, commands, session reads and actions in every mode. Nothing binds a UI, so ctx.hasUI is false and the dialogs answer nothing, as in Pi's headless context. It binds what print mode binds for the same context. Close unbinds.
//
// A Session a CLI mode runs does not call it: the mode owns its host's bridge and binds it itself.
func (s *Session) bindLoaderExtensionHost(bridge *subprocess.UIBridge) {
	current := func() *Session { return s }
	services := s.services
	detachModels := BindSessionModelOperations(bridge, s, services)
	stopRunSignal := WatchSessionRunSignal(bridge, s)
	stopHostState := WatchHostState(bridge, s)
	BindSessionReadActions(bridge, current, s.CWD(), s.Inner().GetSessionDir())
	icodingagent.BindAppendEntryAction(bridge, func() (*icodingagent.Session, error) { return s.Inner(), nil }, s.reportExtensionEntry)
	flagValues := services.ExtensionFlagValues()
	// loader.ts:329-337: getFlag reads the runtime's flagValues, which hold the registered defaults and the command-line values createAgentSessionServices applied to the runtime of the loader that loaded the extensions. A Session built over a loader the extension made and applied itself (createAgentSessionServices) finds them there, not in its own services.
	runtime := s.ResourceLoader().GetExtensions().Runtime
	BindSessionExtensionActions(nil, bridge, current, extension.ContextActions{
		ModelRegistry:    services.Registry(),
		IsProjectTrusted: services.SettingsManager().IsProjectTrusted,
		GetFlagValue: func(name string) any {
			if runtime != nil {
				if value, has := runtime.FlagValue(name); has {
					return value
				}
			}
			return flagValues[name]
		},
	}, nil)
	// upstream: agent-session.ts:3361-3390 binds getAllTools and getCommands to the Session in every mode.
	bridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
		all := s.GetAllTools()
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
		commands := s.SlashCommands()
		out := make([]subprocess.CommandInfo, len(commands))
		for i, command := range commands {
			out[i] = subprocess.CommandInfo{Name: command.Name, Description: command.Description, Source: command.Source, SourceInfo: command.SourceInfo}
		}
		return out
	})
	s.detachExtensionHost = sync.OnceFunc(func() {
		detachModels()
		stopRunSignal()
		stopHostState()
	})
}
