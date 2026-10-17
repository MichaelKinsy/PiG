package codingagent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestNativeAuthResultOmitsAbsentSource(t *testing.T) {
	result := AuthResultJSON(&ai.AuthResult{Auth: ai.ModelAuth{APIKey: "native-key"}})
	if _, present := result["source"]; present {
		t.Fatalf("absent native auth source was fabricated: %v", result)
	}
	var explicit ai.AuthResult
	if err := json.Unmarshal([]byte(`{"auth":{"apiKey":"key"},"source":""}`), &explicit); err != nil {
		t.Fatal(err)
	}
	if source, present := AuthResultJSON(&explicit)["source"]; !present || source != "" {
		t.Fatal("explicit empty auth source was lost")
	}
}

// ModelRuntime.getProviderAuthStatus (Pi 0.87.1:562-571) prioritizes runtime,
// stored and configured credentials, and names template environment variables.
func TestExtensionProviderAuthStatusSources(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	if err := registry.RegisterExtensionProvider("fixture", extension.ProviderConfig{APIKey: "literal"}); err != nil {
		t.Error(err)
	}
	if got := registry.ExtensionProviderAuthStatus("fixture"); got != (ai.AuthStatus{Configured: true, Source: ai.AuthSourceFallback}) {
		t.Fatalf("literal: %+v", got)
	}
	registry.SetRuntimeAPIKey("fixture", "runtime-key")
	if got := registry.ExtensionProviderAuthStatus("fixture"); got != (ai.AuthStatus{Configured: true, Source: ai.AuthSourceRuntime}) {
		t.Fatalf("runtime: %+v", got)
	}
	for _, tc := range []struct {
		value     string
		env       map[string]string
		extension bool
		want      ai.AuthStatus
	}{
		{"${REGISTRY_KEY}", map[string]string{"REGISTRY_KEY": "value"}, false, ai.AuthStatus{Configured: true, Source: ai.AuthSourceEnvironment, Label: "REGISTRY_KEY"}},
		{"${REGISTRY_MISSING}", nil, false, ai.AuthStatus{}},
		{"!printf secret", nil, false, ai.AuthStatus{Configured: true, Source: ai.AuthSourceModelsJSONCommand}},
		{"literal", nil, false, ai.AuthStatus{Configured: true, Source: ai.AuthSourceModelsJSONKey}},
		{"literal", nil, true, ai.AuthStatus{Configured: true, Source: ai.AuthSourceFallback}},
	} {
		for key, value := range tc.env {
			t.Setenv(key, value)
		}
		if got := configuredRequestAuthStatus(tc.value, tc.extension); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %+v, want %+v", tc.value, got, tc.want)
		}
	}
}
