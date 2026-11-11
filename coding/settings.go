package coding

import icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

// NewInMemorySettingsManager creates independent settings without file I/O. Pass it through CreateAgentSessionServicesOptions.SettingsManager to use it for Sessions.
// Ports packages/coding-agent/src/core/settings-manager.ts
// (SettingsManager.inMemory, settings-manager.ts:359-364).
func NewInMemorySettingsManager(settings Settings) *SettingsManager {
	return icodingagent.NewInMemorySettingsManager(settings)
}
