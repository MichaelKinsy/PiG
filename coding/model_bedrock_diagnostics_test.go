package coding

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// ModelRuntime forwards the provider's terminal assistant, including Bedrock failure diagnostics.
func TestModelRuntimePreservesBedrockFailureDiagnostics(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Amzn-Errortype", "ValidationException")
		w.Header().Set("X-Amzn-Requestid", "request-runtime")
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"message":"invalid-model"}`)
	}))
	defer server.Close()
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "aws-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "aws-credentials"))
	config := map[string]any{"providers": map[string]any{"amazon-bedrock": map[string]any{"baseUrl": server.URL, "api": "bedrock-converse-stream"}}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	runtime := services.ModelRuntime()
	model := runtime.GetModel("amazon-bedrock", "us.anthropic.claude-opus-4-8")
	if model == nil {
		t.Fatal("missing model")
	}
	result := runtime.Complete(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}})
	if result.StopReason != ai.StopReasonError || result.ErrorMessage != "Validation error: invalid-model" || len(result.Diagnostics) != 1 {
		t.Fatalf("result=%+v", result)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Type != "bedrock_response_failure" || diagnostic.Error != nil {
		t.Fatalf("diagnostic=%+v", diagnostic)
	}
	raw, err = json.Marshal(diagnostic.Details)
	if err != nil {
		t.Fatal(err)
	}
	var details struct {
		Status    int    `json:"status"`
		ErrorCode string `json:"errorCode"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatal(err)
	}
	if details.Status != 400 || details.ErrorCode != "ValidationException" || details.RequestID != "request-runtime" {
		t.Fatalf("details=%s", raw)
	}
}
