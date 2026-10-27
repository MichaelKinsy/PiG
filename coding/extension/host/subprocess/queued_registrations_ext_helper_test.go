package subprocess_test

import "github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"

// applyQueuedRegistrations is the bind that follows a load: the services of createAgentSessionServices apply the provider registrations the extensions queued while they loaded to the registry bound on the Host (agent-session-services.ts:157-193). The Host asks the registry for nothing while an extension loads.
func applyQueuedRegistrations(h *subprocess.Host) {
	runtime := h.Runtime()
	runtime.BindProviderActions(runtime.BoundProviderActions(), nil)
}
