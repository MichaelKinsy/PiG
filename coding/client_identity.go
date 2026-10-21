package coding

// Ports packages/coding-agent/src/core/client-identity.ts.

import (
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// getClientIdentity is how PiG's requests identify it to providers: a detailed `User-Agent` with PiG's version, and attribution to PiG as a CLI agent while install telemetry is enabled.
func getClientIdentity(settings *icodingagent.SettingsManager) *ai.ClientIdentity {
	identity := &ai.ClientIdentity{UserAgent: new(ai.UserAgentDetailed), Version: new(Version)}
	if settings.IsInstallTelemetryEnabled() {
		identity.Attribution = new(ai.PiAttribution)
	} else {
		identity.AttributionNull = true
	}
	return identity
}
