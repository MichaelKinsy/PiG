package coding

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A model from BuildModel streams through its own per-model provider, which holds the stored credential's env as the auth resolution
// env. Pi materializes the Cloudflare placeholders from that env merged under the request env (models.ts:868 applyAuth, then the
// provider's cloudflareStreams at cloudflare-stream.ts:21-28), so a request that carries no env still reaches the account's endpoint.
func TestBuildModelCloudflareEndpointUsesTheStoredCredentialEnv(t *testing.T) {
	for _, tc := range []struct {
		provider string
		api      ai.API
	}{
		{"cloudflare-ai-gateway", ai.APIOpenAICompletions},
		{"cloudflare-ai-gateway", ai.APIOpenAIResponses},
		{"cloudflare-ai-gateway", ai.APIAnthropicMessages},
		{"cloudflare-workers-ai", ai.APIOpenAICompletions},
	} {
		t.Run(fmt.Sprintf("%s/%s", tc.provider, tc.api), func(t *testing.T) {
			services := newRuntimeTestServices(t)
			env := map[string]string{"CLOUDFLARE_ACCOUNT_ID": "stored-account", "CLOUDFLARE_GATEWAY_ID": "stored-gateway"}
			if err := services.Auth().Set(tc.provider, ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-token", Env: env}); err != nil {
				t.Fatal(err)
			}
			var catalog *ai.Model
			for _, candidate := range services.ModelRuntime().GetModels() {
				if candidate.ProviderMeta.ProviderID == tc.provider && candidate.ProviderMeta.API == tc.api {
					catalog = candidate
					break
				}
			}
			if catalog == nil {
				t.Fatalf("pinned catalog has no %s/%s model", tc.provider, tc.api)
			}
			model, err := BuildModel(tc.provider+"/"+catalog.ID, services)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.NewReplacer("{CLOUDFLARE_ACCOUNT_ID}", "stored-account", "{CLOUDFLARE_GATEWAY_ID}", "stored-gateway").Replace(catalog.ProviderMeta.BaseURL)
			if want == catalog.ProviderMeta.BaseURL {
				t.Fatalf("catalog endpoint %q has no placeholder", want)
			}
			var endpoint string
			fetch := &http.Client{Transport: cloudflareCompatTransport(func(request *http.Request) (*http.Response, error) {
				if endpoint == "" {
					endpoint = request.URL.Scheme + "://" + request.URL.Host + request.URL.Path
				}
				return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"fixture"}}`))}, nil
			})}
			stream, err := model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{}}), ai.StreamOptions{Fetch: fetch})
			if err != nil {
				t.Fatal(err)
			}
			stream.Result()
			if !strings.HasPrefix(endpoint, want+"/") {
				t.Fatalf("endpoint = %q, want under %q", endpoint, want)
			}
		})
	}
}
