package ai

// pi: packages/ai/src/compat.ts

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type compatCloudflareAuthRun struct {
	Name         string `json:"name"`
	Fn           string `json:"fn"`
	Requests     int    `json:"requests"`
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage"`
}

// compat.ts stream and streamSimple send a cloudflare- provider through the catalog's own auth resolution (`models.stream`) when the request carries no
// API key and no AI Gateway authorization header, and straight to the API implementation otherwise. The first path settles an unconfigured provider
// as an error without a request; the second sends the request. Pi runs the same three requests in Node and the settled results must match.
func TestCompatCloudflareAuthRoutingMatchesPi(t *testing.T) {
	const provider = "cloudflare-ai-gateway"
	cmd := exec.CommandContext(t.Context(), "node", "testdata/compat_cloudflare_auth.mjs", pigversion.UpstreamPackageVersion, provider)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []compatCloudflareAuthRun
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
	}))
	defer server.Close()
	model := &Model{ID: "m1", DisplayName: "m1", ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: provider, BaseURL: server.URL + "/v1"}, Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
	request := Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}}
	var got []compatCloudflareAuthRun
	for _, c := range []struct {
		name    string
		options StreamOptions
	}{
		{"no credential", StreamOptions{}},
		{"gateway header", StreamOptions{Headers: ProviderHeaders{"cf-aig-authorization": new("Bearer g")}}},
		{"api key", StreamOptions{APIKey: "k"}},
	} {
		c.options.Env = ProviderEnv{}
		for _, fn := range []string{"stream", "streamSimple"} {
			requests.Store(0)
			run := Stream
			if fn == "streamSimple" {
				run = StreamSimpleContext
			}
			stream, err := run(t.Context(), model, request, c.options)
			entry := compatCloudflareAuthRun{Name: c.name, Fn: fn}
			if err != nil {
				entry.StopReason, entry.ErrorMessage = "error", err.Error()
			} else {
				message := stream.Result()
				entry.StopReason, entry.ErrorMessage = string(message.StopReason), message.ErrorMessage
			}
			entry.Requests = int(requests.Load())
			got = append(got, entry)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PiG %+v\nPi  %+v", got, want)
	}
}
