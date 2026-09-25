package codingagent

import (
	"os"
	"testing"
)

func TestGetRadiusGatewayURLHonorsOverride(t *testing.T) {
	t.Setenv(EnvRadiusGateway, "")
	if err := os.Unsetenv(EnvRadiusGateway); err != nil { // t.Setenv above restores it
		t.Fatal(err)
	}
	if got := GetRadiusGatewayURL(); got != "https://radius.pi.dev" {
		t.Fatalf("default gateway = %q", got)
	}
	t.Setenv(EnvRadiusGateway, "localhost:8788/")
	if got := GetRadiusGatewayURL(); got != "https://localhost:8788" {
		t.Fatalf("override gateway = %q", got)
	}
	t.Setenv(EnvRadiusGateway, "http://127.0.0.1:9999//")
	if got := GetRadiusGatewayURL(); got != "http://127.0.0.1:9999" {
		t.Fatalf("http override gateway = %q", got)
	}
	if RadiusProviderID != "radius" {
		t.Fatalf("RadiusProviderID = %q", RadiusProviderID)
	}
}
