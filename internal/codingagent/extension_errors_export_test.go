package codingagent

import "github.com/MichaelKinsy/PiG/coding/extension"

// SetRuntimeHost gives the mode the runtime host that owns its Session replacement, as NewInteractiveMode's first argument does.
func (h *TestHarness) SetRuntimeHost(rt InteractiveRuntime) { h.Do(func() { h.m.runtimeHost = rt }) }

// PendingExtensionErrors returns the extension errors the runner's listener queued and the main loop has not shown yet.
func (h *TestHarness) PendingExtensionErrors() []extension.ExtensionError {
	h.m.extensionErrorsMu.Lock()
	defer h.m.extensionErrorsMu.Unlock()
	return append([]extension.ExtensionError(nil), h.m.pendingExtensionErrors...)
}

// ShowPendingExtensionErrors shows the queued extension errors in the transcript on the owner loop, as the main loop does.
func (h *TestHarness) ShowPendingExtensionErrors() { h.Do(h.m.showPendingExtensionErrors) }
