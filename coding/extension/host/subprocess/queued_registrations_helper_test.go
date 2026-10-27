package subprocess

import "github.com/MichaelKinsy/PiG/coding/extension"

// applyQueuedRegistrations is the bind that follows a load: the services of createAgentSessionServices, or the Runner that bindCore binds, apply the provider registrations the extensions queued while they loaded to the registry bound on the Host, and report each rejected one (agent-session-services.ts:157-169, runner.ts:471-487). The Host asks the registry for nothing while an extension loads.
func applyQueuedRegistrations(h *Host) []*extension.ExtensionError {
	var failures []*extension.ExtensionError
	runtime := h.Runtime()
	runtime.BindProviderActions(runtime.BoundProviderActions(), func(err *extension.ExtensionError) { failures = append(failures, err) })
	return failures
}
