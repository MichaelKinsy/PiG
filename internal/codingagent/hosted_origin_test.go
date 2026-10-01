package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// D64: PiG's hosted endpoints share one origin. The Node runtime's vendored Pi
// code reaches the same origin through pigidentity.HostedOrigin, so both hosts
// call the same PiG-operated service and neither calls pi.dev.
func TestHostedEndpointsUseTheIdentityOrigin(t *testing.T) {
	for name, endpoint := range map[string]string{
		"install telemetry": defaultInstallTelemetryURL,
		"share gateway":     defaultShareGatewayURL,
	} {
		if !strings.HasPrefix(endpoint, pigidentity.HostedOrigin+"/") {
			t.Errorf("%s endpoint %q is not on the identity origin %s", name, endpoint, pigidentity.HostedOrigin)
		}
	}
}
