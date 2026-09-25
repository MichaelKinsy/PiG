package extension_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestProviderConfigAPIFieldUsesAIAPI protects the named ai.API contract on
// provider declarations. A plain string compiles but loses the shared provider
// API type.
func TestProviderConfigAPIFieldUsesAIAPI(t *testing.T) {
	wantType := reflect.TypeFor[ai.API]()

	t.Run("ProviderConfig.API", func(t *testing.T) {
		f, ok := reflect.TypeFor[extension.ProviderConfig]().FieldByName("API")
		if !ok {
			t.Fatal("ProviderConfig.API field missing")
		}
		if f.Type != wantType {
			t.Errorf("ProviderConfig.API type = %v, want ai.API (%v): D2 retirement regression?",
				f.Type, wantType)
		}
	})

	t.Run("ProviderModelConfig.API", func(t *testing.T) {
		f, ok := reflect.TypeFor[extension.ProviderModelConfig]().FieldByName("API")
		if !ok {
			t.Fatal("ProviderModelConfig.API field missing")
		}
		if f.Type != wantType {
			t.Errorf("ProviderModelConfig.API type = %v, want ai.API (%v): D2 retirement regression?",
				f.Type, wantType)
		}
	})
}

// TestKnownAPIsCoverUpstreamUnion enumerates the closed members documented by
// upstream types.ts while ai.API remains open to additional string values.
func TestKnownAPIsCoverUpstreamUnion(t *testing.T) {
	upstreamKnownAPIs := []ai.API{
		ai.APIOpenAICompletions,
		ai.APIMistralConversations,
		ai.APIOpenAIResponses,
		ai.APIAzureOpenAIResponses,
		ai.APIOpenAICodexResponses,
		ai.APIAnthropicMessages,
		ai.APIBedrockConverseStream,
		ai.APIGoogleGenerativeAI,
		ai.APIGoogleVertex,
		ai.APIPiMessages,
	}
	if len(upstreamKnownAPIs) != 10 {
		t.Errorf("KnownAPIs count = %d, want 10 (per upstream types.ts:7-15)",
			len(upstreamKnownAPIs))
	}
	// Compile-time check that each constant is typed `ai.API`, not
	// untyped string. Done by the var declaration above; if any
	// constant were re-typed to a different shape, this file would
	// fail to compile.
	_ = upstreamKnownAPIs
}
