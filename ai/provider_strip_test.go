package ai

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A Piglet's strip.apis disables an API the way a pig_strip_<api> Binary compiles it out: building a provider for one of its models reports the strip, and the built-in registry omits it. Other APIs still build.
func TestStrippedAPIProviderBuildReportsStrip(t *testing.T) {
	for _, tc := range []struct {
		api   API
		want  string
		build func(t *testing.T) (Provider, error)
	}{
		{
			api:  APIBedrockConverseStream,
			want: "Provider amazon-bedrock: API bedrock-converse-stream is stripped from this Piglet (strip.apis: bedrock-converse-stream)",
			build: func(t *testing.T) (Provider, error) {
				return directAPIProvider(mustGeneratedModel(t, "amazon-bedrock", "amazon.nova-2-lite-v1:0").ToModel(), "", nil)
			},
		},
		{
			api:  APIMistralConversations,
			want: "Provider mistral: API mistral-conversations is stripped from this Piglet (strip.apis: mistral-conversations)",
			build: func(t *testing.T) (Provider, error) {
				return directAPIProvider(mustGeneratedModel(t, "mistral", "codestral-latest").ToModel(), "key", nil)
			},
		},
		{
			api:  APIGoogleVertex,
			want: "Provider google-vertex: API google-vertex is stripped from this Piglet (strip.apis: google-vertex)",
			build: func(*testing.T) (Provider, error) {
				return NewGoogleVertexAPIProvider(GoogleVertexConfig{APIKey: "key", Model: "gemini-2.5-flash"})
			},
		},
	} {
		t.Run(string(tc.api), func(t *testing.T) {
			if !pigstrip.Has(pigstrip.ListAPIs, string(tc.api)) {
				provider, err := tc.build(t)
				if err != nil || provider == nil {
					t.Fatalf("unstripped build = %v, %v; want a provider", provider, err)
				}
				if _, ok := LookupBuiltInProvider(tc.api); !ok {
					t.Fatalf("unstripped LookupBuiltInProvider(%s) not found", tc.api)
				}
			}
			t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(tc.api)))
			provider, err := tc.build(t)
			if provider != nil || err == nil || err.Error() != tc.want {
				t.Fatalf("stripped build = %v, %v; want %q", provider, err, tc.want)
			}
			if _, ok := LookupBuiltInProvider(tc.api); ok {
				t.Fatalf("LookupBuiltInProvider(%s) found a stripped API", tc.api)
			}
			if slices.Contains(RegisteredAPIs(), tc.api) {
				t.Fatalf("RegisteredAPIs() lists stripped %s", tc.api)
			}
			anthropic, err := directAPIProvider(mustGeneratedModel(t, "anthropic", "claude-haiku-4-5").ToModel(), "key", nil)
			if err != nil || anthropic == nil {
				t.Fatalf("anthropic build with %s stripped = %v, %v", tc.api, anthropic, err)
			}
			if _, ok := LookupBuiltInProvider(APIOpenAICompletions); !ok {
				t.Fatalf("LookupBuiltInProvider(openai-completions) not found with %s stripped", tc.api)
			}
		})
	}
}
