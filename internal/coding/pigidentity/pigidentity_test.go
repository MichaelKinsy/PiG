package pigidentity

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The owner-approved PiG values (docs/parity/DIVERGENCES.md D26, D64, D65).
// They are spelled out here so a change to identity.json cannot pass by
// rewriting both sides.
func TestIdentityValuesAreThePiGBrand(t *testing.T) {
	want := map[string]string{
		"AttributionName":     "PiG",
		"AttributionURL":      "https://github.com/MichaelKinsy/PiG",
		"AttributionCategory": "cli-agent",
		"CodexOriginator":     "pig",
		"XAIReferrer":         "pig",
		"AgentMarker":         "pig",
		"UserAgentProduct":    "pig",
		"HostedOrigin":        "https://pi-in-go.dev",
	}
	got := map[string]string{
		"AttributionName":     AttributionName,
		"AttributionURL":      AttributionURL,
		"AttributionCategory": AttributionCategory,
		"CodexOriginator":     CodexOriginator,
		"XAIReferrer":         XAIReferrer,
		"AgentMarker":         AgentMarker,
		"UserAgentProduct":    UserAgentProduct,
		"HostedOrigin":        HostedOrigin,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("identity = %v, want %v", got, want)
	}
}

// identity.json is the one source of these values: the Node vendoring step
// (automation/gen/pi-identity-patches.mjs) reads it, and the Go constants must
// spell exactly its values, every key included.
func TestIdentityJSONIsTheSingleSource(t *testing.T) {
	data, err := os.ReadFile("identity.json")
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]string
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"attributionName":     AttributionName,
		"attributionUrl":      AttributionURL,
		"attributionCategory": AttributionCategory,
		"codexOriginator":     CodexOriginator,
		"chatgptAgentName":    ChatGPTAgentName,
		"xaiReferrer":         XAIReferrer,
		"agentMarker":         AgentMarker,
		"userAgentProduct":    UserAgentProduct,
		"hostedOrigin":        HostedOrigin,
	}
	if !reflect.DeepEqual(file, want) {
		t.Fatalf("identity.json = %v, want the exported values %v", file, want)
	}
}
