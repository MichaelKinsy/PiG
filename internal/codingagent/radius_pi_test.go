package codingagent

import (
	"os"
	"testing"
)

// pi: packages/coding-agent/src/core/radius.ts

// radius.ts getRadiusGatewayUrl: normalizeRadiusGatewayUrl(process.env.PI_RADIUS_GATEWAY ?? DEFAULT_RADIUS_GATEWAY), where normalize adds
// https:// to a value without an http(s) scheme and strips trailing slashes (radius-config.ts:52-55).
func TestGetRadiusGatewayURLMatchesPi(t *testing.T) {
	if RadiusProviderID != "radius" || EnvRadiusGateway != "PI_RADIUS_GATEWAY" {
		t.Fatalf("constants = %q, %q", RadiusProviderID, EnvRadiusGateway)
	}
	cases := []struct {
		name  string
		set   bool
		value string
		want  string
	}{
		{"unset uses the default gateway", false, "", "https://radius.pi.dev"},
		{"override with a scheme", true, "http://localhost:8080", "http://localhost:8080"},
		{"override without a scheme gets https", true, "gateway.example.com", "https://gateway.example.com"},
		{"trailing slashes are stripped", true, "https://gw.example.com///", "https://gw.example.com"},
		{"scheme match is case-insensitive", true, "HTTPS://GW.example.com/", "HTTPS://GW.example.com"},
		{"set but empty is used, not defaulted (??)", true, "", "https:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.set {
				t.Setenv(EnvRadiusGateway, c.value)
			} else {
				if old, ok := os.LookupEnv(EnvRadiusGateway); ok {
					t.Setenv(EnvRadiusGateway, old)
				}
				_ = os.Unsetenv(EnvRadiusGateway)
			}
			if got := GetRadiusGatewayURL(); got != c.want {
				t.Fatalf("GetRadiusGatewayURL() = %q, want %q", got, c.want)
			}
		})
	}
}
