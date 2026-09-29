package coding

// Ports packages/coding-agent/src/core/agent-session.ts.

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// ExtensionBindings supplies mode-owned UI and command actions before session_start dispatch.
type ExtensionBindings struct {
	UIContext             extension.UIContext
	CommandContextActions extension.CommandActions
	Mode                  extension.ExtensionMode
}

func (s *Session) bindSessionExtensions(runner *inproc.Runner, bindings ExtensionBindings) {
	runner.SetUIContext(bindings.UIContext, bindings.Mode)
	s.bindExtensionCore(runner)
	runner.BindCommandActions(s.ExtensionCommandActions())
	runner.BindCommandActions(bindings.CommandContextActions)
}

func (s *Session) bindExtensionCore(runner *inproc.Runner) {
	runner.BindCore(extension.ExtensionActions{
		SendMessage:     s.SendMessage,
		SendUserMessage: s.SendExtensionUserMessage,
	}, extension.ContextActions{
		SessionManager: s.inner, ModelRegistry: s.services.Registry(),
		GetModel: func() extension.Model { return s.Model() },
		IsIdle:   s.IsIdle, HasPendingMessages: s.HasPendingMessages,
		IsProjectTrusted: s.services.SettingsManager().IsProjectTrusted,
		Abort:            s.RequestAbort, GetSystemPrompt: s.systemPrompt,
		GetSystemPromptOptions: s.GetSystemPromptOptions,
		GetAllTools:            s.GetAllTools, GetActiveTools: s.ActiveToolNames,
		SetActiveTools: s.SetActiveToolsByName, GetScopedModels: s.ScopedModels,
	}, nil)
}
