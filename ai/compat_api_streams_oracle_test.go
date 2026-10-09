package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type compatAPIOracleRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    any               `json:"body"`
}

type compatAPIOracleResult struct {
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage"`
	Text         string `json:"text"`
	Input        int    `json:"input"`
	Output       int    `json:"output"`
	API          string `json:"api"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
}

type compatAPIOracleRun struct {
	Request compatAPIOracleRequest `json:"request"`
	Result  compatAPIOracleResult  `json:"result"`
}

type compatAPIOracle struct {
	Responses map[string]string `json:"responses"`
	Served    map[string]struct {
		Stream       compatAPIOracleRun `json:"stream"`
		StreamSimple compatAPIOracleRun `json:"streamSimple"`
	} `json:"served"`
	Setup map[string]compatAPIOracleResult `json:"setup"`
}

func compatAPIResult(message *AssistantMessage) compatAPIOracleResult {
	result := compatAPIOracleResult{StopReason: string(message.StopReason), ErrorMessage: message.ErrorMessage, Input: message.Usage.Input, Output: message.Usage.Output, API: string(message.API), Provider: message.Provider, Model: message.Model}
	for _, part := range message.Content {
		if text, ok := part.(TextContent); ok {
			result.Text += text.Text
		}
	}
	return result
}

// Ports packages/ai/src/compat.ts azureOpenAIResponsesApi, googleGenerativeAIApi, googleVertexApi and mistralConversationsApi (api/*.lazy.ts -> lazy.ts lazyApi): the pinned pi-ai answers each API object's stream and streamSimple against a local server and with no credentials,
// and the Go API objects send the same request (method, URL, authentication header, body) and return the same message. Bedrock's no-credential text is the AWS SDK's own and differs by language, so it is not compared.
// mutation-checked: building the provider for the wrong API, or dropping the simple option mapping, fails it.
func TestCompatAPIStreamsMatchPi(t *testing.T) {
	// The Pi pin runs without the caller's credentials: only PATH and a home that does not exist. Windows also needs its
	// system root and temporary directory for Node to start, and `env -i` is not a Windows program.
	oracle := exec.CommandContext(t.Context(), "node", "testdata/compat_api_streams.mjs", pigversion.UpstreamVersion)
	oracle.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent"}
	for _, name := range []string{"SystemRoot", "SYSTEMDRIVE", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(name); ok && runtime.GOOS == "windows" {
			oracle.Env = append(oracle.Env, name+"="+value)
		}
	}
	output, err := oracle.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v", err)
	}
	var pi compatAPIOracle
	if err := json.Unmarshal(output, &pi); err != nil {
		t.Fatal(err)
	}
	apis := map[string]struct {
		make     func() *ProviderStreams
		api      API
		provider string
		path     string
	}{
		"azureOpenAIResponsesApi": {AzureOpenAIResponsesAPI, APIAzureOpenAIResponses, "azure-openai-responses", "/openai/v1"},
		"googleGenerativeAIApi":   {GoogleGenerativeAIAPI, APIGoogleGenerativeAI, "google", "/v1beta"},
		"mistralConversationsApi": {MistralConversationsAPI, APIMistralConversations, "mistral", ""},
	}
	for name, c := range apis {
		t.Run(name, func(t *testing.T) {
			var seen *compatAPIOracleRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				request := compatAPIOracleRequest{Method: r.Method, URL: r.URL.RequestURI(), Headers: map[string]string{}}
				for _, header := range []string{"x-goog-api-key", "authorization", "api-key"} {
					if value := r.Header.Get(header); value != "" {
						request.Headers[header] = value
					}
				}
				if err := json.Unmarshal(raw, &request.Body); err != nil {
					t.Errorf("body: %v", err)
				}
				seen = &request
				w.Header().Set("content-type", "text/event-stream")
				_, _ = w.Write([]byte(pi.Responses[name]))
			}))
			defer server.Close()
			model := &Model{ID: "m1", DisplayName: "m1", ProviderMeta: ProviderMetadata{API: c.api, ProviderID: c.provider, BaseURL: server.URL + c.path}, Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
			options := StreamOptions{APIKey: "k"}
			want := pi.Served[name]
			for label, run := range map[string]struct {
				stream func() (*AssistantMessageEventStream, error)
				want   compatAPIOracleRun
			}{
				"stream": {func() (*AssistantMessageEventStream, error) {
					return c.make().Stream(t.Context(), model, NormalizeContext(compatHello), options)
				}, want.Stream},
				"streamSimple": {func() (*AssistantMessageEventStream, error) {
					return c.make().StreamSimple(t.Context(), model, NormalizeContext(compatHello), options)
				}, want.StreamSimple},
			} {
				seen = nil
				events, err := run.stream()
				if err != nil {
					t.Fatalf("%s: %v", label, err)
				}
				got := compatAPIOracleRun{Result: compatAPIResult(events.Result())}
				if seen != nil {
					got.Request = *seen
				}
				if !reflect.DeepEqual(got, run.want) {
					gotJSON, _ := json.MarshalIndent(got, "", " ")
					wantJSON, _ := json.MarshalIndent(run.want, "", " ")
					t.Errorf("%s differs from Pi\n got %s\nwant %s", label, gotJSON, wantJSON)
				}
			}
		})
	}
	setup := map[string]struct {
		make     func() *ProviderStreams
		api      API
		provider string
		baseURL  string
	}{
		"azureOpenAIResponsesApi": {AzureOpenAIResponsesAPI, APIAzureOpenAIResponses, "azure-openai-responses", "https://x.openai.azure.com/openai/v1"},
		"googleGenerativeAIApi":   {GoogleGenerativeAIAPI, APIGoogleGenerativeAI, "google", "https://generativelanguage.googleapis.com/v1beta"},
		"googleVertexApi":         {GoogleVertexAPI, APIGoogleVertex, "google-vertex", "https://{location}-aiplatform.googleapis.com"},
		"mistralConversationsApi": {MistralConversationsAPI, APIMistralConversations, "mistral", "https://api.mistral.ai"},
	}
	for _, key := range []string{"AZURE_OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "MISTRAL_API_KEY"} {
		t.Setenv(key, "")
	}
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	for name, c := range setup {
		t.Run(name+" without credentials", func(t *testing.T) {
			model := &Model{ID: "m1", DisplayName: "m1", ProviderMeta: ProviderMetadata{API: c.api, ProviderID: c.provider, BaseURL: c.baseURL}, Input: []string{"text"}}
			events, err := c.make().Stream(t.Context(), model, NormalizeContext(compatHello), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if got := compatAPIResult(events.Result()); got != pi.Setup[name] {
				t.Errorf("got %+v, Pi %+v", got, pi.Setup[name])
			}
		})
	}
	// bedrockConverseStreamApi: Pi's no-credential text ("Could not load credentials from any providers") is the JS credential chain's; the AWS SDK for Go reports its own. Both end the stream with an error message that carries the model identity; the Go message names the Bedrock Runtime call, which proves the request was routed to the Bedrock provider.
	t.Run("bedrockConverseStreamApi without credentials", func(t *testing.T) {
		t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
		for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_BEARER_TOKEN_BEDROCK", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"} {
			t.Setenv(key, "")
		}
		t.Setenv("HOME", t.TempDir())
		model := &Model{ID: "m1", DisplayName: "m1", ProviderMeta: ProviderMetadata{API: APIBedrockConverseStream, ProviderID: "amazon-bedrock"}, Input: []string{"text"}}
		events, err := BedrockConverseStreamAPI().Stream(t.Context(), model, NormalizeContext(compatHello), StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got := compatAPIResult(events.Result())
		if got.StopReason != "error" || !strings.Contains(got.ErrorMessage, "Bedrock Runtime") || got.API != "bedrock-converse-stream" || got.Provider != "amazon-bedrock" || got.Model != "m1" {
			t.Errorf("got %+v", got)
		}
	})
}
