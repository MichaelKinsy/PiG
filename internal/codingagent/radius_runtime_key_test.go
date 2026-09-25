package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestRadiusAPIKeyRuntimeKeyOutranksStored mirrors upstream, where the
// RuntimeCredentials overlay masks a stored Radius credential and removal
// reveals it again (CTCORE-005).
func TestRadiusAPIKeyRuntimeKeyOutranksStored(t *testing.T) {
	registry, _, _ := radiusTestRegistry(t, "", map[string]ai.Credential{"radius": {Type: ai.CredentialAPIKey, Key: "stored-key"}})
	registry.SetRuntimeAPIKey("radius", "runtime-key")
	if got, err := registry.RadiusAPIKey(context.Background(), "radius"); err != nil || got != "runtime-key" {
		t.Fatalf("RadiusAPIKey = %q, %v; want runtime-key", got, err)
	}
	registry.RemoveRuntimeAPIKey("radius")
	if got, err := registry.RadiusAPIKey(context.Background(), "radius"); err != nil || got != "stored-key" {
		t.Fatalf("RadiusAPIKey after removal = %q, %v; want stored-key", got, err)
	}
}
