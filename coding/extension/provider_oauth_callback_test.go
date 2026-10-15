package extension

import (
	"encoding/json"
	"testing"
)

// Pi types.ts:1937-1938 ProviderConfig.oauth.usesCallbackServer: a deprecated flag retained for source compatibility; a registration carrying it keeps it.
func TestProviderConfigOAuthKeepsUsesCallbackServer(t *testing.T) {
	var config ProviderConfig
	if err := json.Unmarshal([]byte(`{"name":"p","oauth":{"name":"Login","usesCallbackServer":true}}`), &config); err != nil {
		t.Fatal(err)
	}
	if config.OAuth == nil || !config.OAuth.UsesCallbackServer || config.OAuth.Name != "Login" {
		t.Fatalf("oauth=%+v", config.OAuth)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var round struct {
		OAuth struct {
			UsesCallbackServer bool `json:"usesCallbackServer"`
		} `json:"oauth"`
	}
	if err := json.Unmarshal(encoded, &round); err != nil || !round.OAuth.UsesCallbackServer {
		t.Fatalf("round trip lost the flag: %s (%v)", encoded, err)
	}
}
