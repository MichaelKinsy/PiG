package coding

// Ports packages/coding-agent/src/core/agent-session.ts.

import (
	"reflect"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// ExtensionBindings supplies mode-owned UI, command actions and handlers before session_start dispatch (upstream ExtensionBindings, agent-session.ts:300-307). A field left at its zero value is Pi's undefined: BindExtensions keeps the earlier value for it.
type ExtensionBindings struct {
	UIContext             extension.UIContext
	CommandContextActions extension.CommandActions
	Mode                  extension.ExtensionMode
	// AbortHandler replaces the default abort of the extension context (agent-session.ts:3443-3447).
	AbortHandler func()
	// ShutdownHandler backs ctx.shutdown() (agent-session.ts:3451).
	ShutdownHandler func()
	// OnError receives extension errors; a later binding replaces the earlier listener (agent-session.ts:3340-3343).
	OnError extension.ErrorListener
}

// mergedWith returns the bindings after bindings.fieldwise overrides: only the fields the argument defines replace the stored ones (agent-session.ts:3259-3276).
func (b ExtensionBindings) mergedWith(next ExtensionBindings) ExtensionBindings {
	if next.UIContext != nil {
		b.UIContext = next.UIContext
	}
	if next.Mode != "" {
		b.Mode = next.Mode
	}
	if !reflect.ValueOf(next.CommandContextActions).IsZero() {
		b.CommandContextActions = next.CommandContextActions
	}
	if next.AbortHandler != nil {
		b.AbortHandler = next.AbortHandler
	}
	if next.ShutdownHandler != nil {
		b.ShutdownHandler = next.ShutdownHandler
	}
	if next.OnError != nil {
		b.OnError = next.OnError
	}
	return b
}

// hasBindings is Pi's reload condition (agent-session.ts:3688-3692): the mode supplied a UI context, command actions, a shutdown handler or an error listener.
func (b ExtensionBindings) hasBindings() bool {
	return b.UIContext != nil || !reflect.ValueOf(b.CommandContextActions).IsZero() || b.ShutdownHandler != nil || b.OnError != nil
}

// mergeExtensionBindings merges bindings into the session's stored bindings and returns the result.
func (s *Session) mergeExtensionBindings(next ExtensionBindings) ExtensionBindings {
	s.extensionBindingsMu.Lock()
	defer s.extensionBindingsMu.Unlock()
	var merged ExtensionBindings
	if current := s.extensionBindings.Load(); current != nil {
		merged = *current
	}
	merged = merged.mergedWith(next)
	s.extensionBindings.Store(&merged)
	return merged
}

// bindSessionExtensions is Pi's _applyExtensionBindings (agent-session.ts:3336-3344) plus the core binding every mode shares: it points the runner at the stored bindings and replaces the error listener of the previous binding.
func (s *Session) bindSessionExtensions(runner *inproc.Runner, bindings ExtensionBindings) {
	runner.SetUIContext(bindings.UIContext, bindings.Mode)
	s.bindExtensionCore(runner)
	runner.BindCommandActions(s.ExtensionCommandActions())
	runner.BindCommandActions(bindings.CommandContextActions)
	s.extensionBindingsMu.Lock()
	defer s.extensionBindingsMu.Unlock()
	if s.extensionErrorUnsubscribe != nil {
		s.extensionErrorUnsubscribe()
		s.extensionErrorUnsubscribe = nil
	}
	if bindings.OnError != nil {
		s.extensionErrorUnsubscribe = runner.AddErrorListener(bindings.OnError)
	}
}

// SlashCommands lists the commands pi.getCommands() reports: the extension commands under their invocation names, then the prompt templates, then the skills (named `skill:<name>`) of the Session's resource loader, each with the sourceInfo the resource carries; a resource without one gets the provenance its path has under the Session's project and user directories (agent-session.ts _bindExtensionCore getCommands).
func (s *Session) SlashCommands() []extension.SlashCommandInfo {
	return s.slashCommandsFor(s.currentRunner())
}

// slashCommandsFor is SlashCommands over the runner being bound: a runtime replacement binds its new runner before the Session's current one changes.
func (s *Session) slashCommandsFor(runner *inproc.Runner) []extension.SlashCommandInfo {
	catalog := icodingagent.SlashCommandCatalog{PromptTemplates: s.PromptTemplates(), Skills: s.ResourceLoader().GetSkills().Skills, CWD: s.services.CWD(), AgentDir: s.services.AgentDir()}
	if runner != nil {
		catalog.Runner = runner
	}
	return catalog.Commands()
}

func (s *Session) bindExtensionCore(runner *inproc.Runner) {
	runner.BindCore(extension.ExtensionActions{
		SendMessage:     s.SendMessage,
		SendUserMessage: s.SendExtensionUserMessage,
		GetCommands:     func() []extension.SlashCommandInfo { return s.slashCommandsFor(runner) },
		// upstream: agent-session.ts:3339 binds getSettings to the session's settings manager in every mode.
		GetSettings: s.SettingsManager().ExtensionSettings,
		// upstream: agent-session.ts:3339-3355 binds the tool actions of the runtime the extension API reads.
		GetActiveTools: s.ActiveToolNames, GetAllTools: s.GetAllTools, SetActiveTools: s.SetActiveToolsByName, RefreshTools: s.RefreshTools,
		SetLabel: func(entryID string, label *string) error {
			_, err := s.inner.AppendLabelChange(entryID, label)
			return err
		},
	}, extension.ContextActions{
		SessionManager: s.inner, ModelRegistry: s.services.Registry(),
		GetModel: func() extension.Model { return s.Model() },
		IsIdle:   s.IsIdle, HasPendingMessages: s.HasPendingMessages, GetSignal: s.Signal,
		IsProjectTrusted: s.SettingsManager().IsProjectTrusted,
		Abort:            s.extensionAbort, Shutdown: s.extensionShutdown(), GetSystemPrompt: s.systemPrompt,
		GetSystemPromptOptions: s.GetSystemPromptOptions,
		GetAllTools:            s.GetAllTools, GetActiveTools: s.ActiveToolNames,
		SetActiveTools: s.SetActiveToolsByName, RefreshTools: s.RefreshTools, GetScopedModels: s.ScopedModels,
		GetThinkingLevel: func() extension.ThinkingLevel { return s.ThinkingLevel() },
		ToolActions:      s.ToolActions(),
	}, nil)
}

// extensionAbort is ctx.abort() (agent-session.ts:3442-3448): the mode's abort handler when it bound one, else the session's abort. The handler is read at call time, as Pi reads this._extensionAbortHandler.
func (s *Session) extensionAbort() {
	if bindings := s.extensionBindings.Load(); bindings != nil && bindings.AbortHandler != nil {
		bindings.AbortHandler()
		return
	}
	s.RequestAbort()
}

// extensionShutdown is ctx.shutdown() (agent-session.ts:3450-3452): it calls the mode's shutdown handler when one is bound, at call time. Without a bound handler ctx.shutdown stays whatever the mode bound on the runner.
func (s *Session) extensionShutdown() func() {
	return func() {
		if bindings := s.extensionBindings.Load(); bindings != nil && bindings.ShutdownHandler != nil {
			bindings.ShutdownHandler()
		}
	}
}
