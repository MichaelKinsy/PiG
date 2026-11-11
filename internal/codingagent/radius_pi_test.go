package codingagent

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
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

// pi: packages/coding-agent/src/core/radius.ts
// The installed Pi's radius.ts and PiG's give the same provider id, environment variable name, MCP URL and gateway URL for every
// PI_RADIUS_GATEWAY setting: unset, empty, with and without a scheme, mixed-case schemes, trailing slashes, whitespace.
func TestRadiusGatewayMatchesPi(t *testing.T) {
	type probe struct {
		Set   bool   `json:"set"`
		Value string `json:"value"`
	}
	probes := []probe{
		{false, ""}, {true, ""}, {true, "/"}, {true, "http://localhost:8788"}, {true, "localhost:8788/"}, {true, "gateway.example.com"},
		{true, "https://gw.example.com///"}, {true, "HTTPS://GW.example.com/"}, {true, "Http://x//"}, {true, " https://padded.example.com/ "},
		{true, "ftp://other.example.com/"}, {true, "http://127.0.0.1:9999//"},
	}
	var want struct {
		Provider string   `json:"provider"`
		Env      string   `json:"env"`
		MCP      string   `json:"mcp"`
		URLs     []string `json:"urls"`
	}
	pioracle.Run(t, `
const radius = await load("pi-coding-agent/core/radius.js");
const urls = input.probes.map((probe) => {
	if (probe.set) process.env[radius.ENV_RADIUS_GATEWAY] = probe.value;
	else delete process.env[radius.ENV_RADIUS_GATEWAY];
	return radius.getRadiusGatewayUrl();
});
emit({ provider: radius.RADIUS_PROVIDER_ID, env: radius.ENV_RADIUS_GATEWAY, mcp: radius.RADIUS_MCP_URL, urls });`, map[string]any{"probes": probes}, &want)
	if RadiusProviderID != want.Provider || EnvRadiusGateway != want.Env {
		t.Fatalf("constants = %q, %q; Pi has %q, %q", RadiusProviderID, EnvRadiusGateway, want.Provider, want.Env)
	}
	if got := ai.NormalizeRadiusGatewayURL(ai.DefaultRadiusGateway) + "/mcp"; got != want.MCP {
		t.Fatalf("RADIUS_MCP_URL = %q, want %q", got, want.MCP)
	}
	for i, p := range probes {
		if p.Set {
			t.Setenv(EnvRadiusGateway, p.Value)
		} else {
			t.Setenv(EnvRadiusGateway, "")
			_ = os.Unsetenv(EnvRadiusGateway)
		}
		if got := GetRadiusGatewayURL(); got != want.URLs[i] {
			t.Errorf("GetRadiusGatewayURL() with %+v = %q, Pi %q", p, got, want.URLs[i])
		}
	}
}
