package inproc

import "github.com/MichaelKinsy/PiG/coding/extension"

// BindScopedModels binds the Session's scope without replacing the mode's other context actions. Bind before dispatch; existing contexts retain their captured callback.
// upstream: packages/coding-agent/src/core/agent-session.ts:_bindExtensionCore
func (r *Runner) BindScopedModels(getScopedModels func() []extension.ScopedModel) {
	r.contextActions.GetScopedModels = getScopedModels
}
