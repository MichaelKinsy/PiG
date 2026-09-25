package codingagent

// Mirrors upstream .upstream/current/packages/coding-agent/src/core/radius.ts.

import (
	"os"

	"github.com/MichaelKinsy/PiG/ai"
)

// RadiusProviderID is the built-in Radius provider ID.
const RadiusProviderID = ai.RadiusProviderID

// EnvRadiusGateway overrides the Radius gateway origin for GetRadiusGatewayURL.
// In Pi 0.87.1 only the experimental server and relay read it; the built-in
// provider always uses ai.DefaultRadiusGateway.
const EnvRadiusGateway = "PI_RADIUS_GATEWAY"

// GetRadiusGatewayURL returns the Radius gateway origin, honoring
// PI_RADIUS_GATEWAY. Like upstream's `??`, a set but empty value is used.
func GetRadiusGatewayURL() string {
	gateway, ok := os.LookupEnv(EnvRadiusGateway)
	if !ok {
		gateway = ai.DefaultRadiusGateway
	}
	return ai.NormalizeRadiusGatewayURL(gateway)
}
