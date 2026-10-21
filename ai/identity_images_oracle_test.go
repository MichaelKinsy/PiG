package ai_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/ai/src/api/openrouter-images.ts createClient (providerHeadersToRecord(getIdentityHeaders(model, identity), model.headers, optionsHeaders))

// The headers the OpenRouter image API sends, from the pinned pi-ai and from PiG, for identities and for model and request headers that override or remove
// them: the headers are three sources merged one after the other, and a null request header removes a header of any letter case that an earlier source set.
func TestOpenRouterImagesHeadersMatchPi(t *testing.T) {
	type probe struct {
		Provider     string            `json:"provider,omitempty"`
		ModelHeaders map[string]string `json:"modelHeaders,omitempty"`
		Identity     json.RawMessage   `json:"identity,omitempty"`
		Headers      map[string]any    `json:"headers,omitempty"`
	}
	acme := `{"userAgent":{"custom":"acme/1.0"},"attribution":{"name":"acme","url":"https://acme.example","categories":["cli-agent","cloud-agent"]}}`
	probes := []probe{
		{Identity: json.RawMessage(`{"userAgent":{"custom":"acme/1.0"}}`)},
		{Identity: json.RawMessage(acme)},
		{Identity: json.RawMessage(acme), Provider: "nvidia"},
		{Identity: json.RawMessage(acme), Provider: "opencode"},
		{Identity: json.RawMessage(`{"userAgent":{"custom":"acme/1.0"},"attribution":null}`)},
		// Pi's own default attribution values.
		{Identity: json.RawMessage(`{"userAgent":{"custom":"acme/1.0"},"attribution":{"name":"pi","url":"https://pi.dev","categories":["cli-agent"]}}`)},
		{Identity: json.RawMessage(`{"userAgent":{"custom":"acme/1.0"},"attribution":{"name":"pi","url":"https://pi.dev","categories":["cli-agent"]}}`), Provider: "nvidia"},
		{Identity: json.RawMessage(`{"userAgent":{"custom":"acme/1.0"},"attribution":{"name":"pi","url":"https://pi.dev","categories":["cli-agent"]}}`), Provider: "opencode"},
		// A null request header removes a header an earlier source set, whatever its letter case.
		{Identity: json.RawMessage(acme), Headers: map[string]any{"User-Agent": nil}},
		{Identity: json.RawMessage(acme), Headers: map[string]any{"user-agent": nil, "http-referer": nil}},
		{Identity: json.RawMessage(acme), Headers: map[string]any{"X-OpenRouter-Title": nil, "X-OpenRouter-Categories": "x"}},
		{Identity: json.RawMessage(acme), ModelHeaders: map[string]string{"User-Agent": "model-ua", "HTTP-Referer": "model-ref"}},
		{Identity: json.RawMessage(acme), ModelHeaders: map[string]string{"User-Agent": "model-ua"}, Headers: map[string]any{"user-agent": "request-ua"}},
		{Identity: json.RawMessage(`{"userAgent":{"custom":"acme/1.0"}}`), ModelHeaders: map[string]string{"X-Removed": "remove-me", "X-Kept": "keep-me", "X-Shared": "model"}, Headers: map[string]any{"X-Removed": nil, "x-kept": nil, "X-Shared": "request"}},
		{Identity: json.RawMessage(`{"userAgent":{"custom":"acme/1.0"}}`), ModelHeaders: map[string]string{"x-kept": "keep-me"}, Headers: map[string]any{"X-Kept": nil}},
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/openrouter_images_headers.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []map[string]string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	// The names a probe can set or remove: HTTP clients add others of their own (Go: Accept-Encoding, Content-Length; the OpenAI SDK: x-stainless-*).
	watched := []string{"user-agent", "http-referer", "x-openrouter-title", "x-openrouter-categories", "x-billing-invoke-origin", "x-opencode-client", "x-removed", "x-kept", "x-shared", "authorization", "content-type"}
	for i, p := range probes {
		var got http.Header
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Clone()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"x","choices":[{"message":{"content":"t"}}]}`))
		}))
		provider := p.Provider
		if provider == "" {
			provider = "openrouter"
		}
		model := ai.ImageModel{ID: "m", Name: "m", API: ai.APIImagesOpenRouter, Provider: provider, BaseURL: server.URL + "/v1", Headers: p.ModelHeaders, Input: []string{"text"}, Output: []string{"image"}}
		options := ai.ImagesOptions{APIKey: "k", MaxRetries: new(0)}
		if p.Identity != nil {
			options.Identity = new(ai.ClientIdentity)
			if err := json.Unmarshal(p.Identity, options.Identity); err != nil {
				t.Fatal(err)
			}
		}
		if p.Headers != nil {
			options.Headers = ai.ProviderHeaders{}
			for name, value := range p.Headers {
				if text, ok := value.(string); ok {
					options.Headers[name] = &text
				} else {
					options.Headers[name] = nil
				}
			}
		}
		ai.GenerateImagesOpenRouter(t.Context(), model, ai.ImagesContext{Input: []ai.ContentBlock{ai.TextContent{Text: "draw"}}}, options)
		server.Close()
		gotWatched, wantWatched := map[string]string{}, map[string]string{}
		for _, name := range watched {
			if values, ok := got[http.CanonicalHeaderKey(name)]; ok {
				gotWatched[name] = strings.Join(values, ", ")
			}
			if value, ok := want[i][name]; ok {
				wantWatched[name] = value
			}
		}
		// A removed User-Agent leaves each HTTP client's own (Go's, the OpenAI SDK's): neither may be the identity's.
		for name, value := range p.Headers {
			if strings.EqualFold(name, "user-agent") && value == nil {
				if gotWatched["user-agent"] == "acme/1.0" || wantWatched["user-agent"] == "acme/1.0" {
					t.Errorf("probe %d: a null User-Agent kept the identity's (PiG %q, Pi %q)", i, gotWatched["user-agent"], wantWatched["user-agent"])
				}
				delete(gotWatched, "user-agent")
				delete(wantWatched, "user-agent")
			}
		}
		if !reflect.DeepEqual(gotWatched, wantWatched) {
			one, _ := json.Marshal(p)
			t.Errorf("probe %d %s: PiG sent %v, Pi sent %v", i, one, gotWatched, wantWatched)
		}
	}
}
