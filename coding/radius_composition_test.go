package coding

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestRadiusConfiguredModelStreamsWithComposedAuthAndHeaders(t *testing.T) {
	t.Setenv("RADIUS_API_KEY", "environment-key")
	requests := make(chan http.Header, 1)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		requests <- r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"start\"}\n\ndata: {\"type\":\"done\",\"reason\":\"stop\",\"usage\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":0,\"cost\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0,\"total\":0}}}\n\n")
	}))
	defer gateway.Close()
	agentDir := t.TempDir()
	config := `{"providers":{"radius-dev":{"oauth":"radius","baseUrl":"` + gateway.URL + `/v1","apiKey":"configured-key","headers":{"X-Provider":"provider"},"models":[{"id":"custom","api":"pi-messages","headers":{"X-Model":"model"}}],"modelOverrides":{"custom":{"name":"Composed","contextWindow":4242,"headers":{"X-Override":"override"}}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	runtime := services.ModelRuntime()
	model := runtime.GetModel("radius-dev", "custom")
	if model == nil || model.DisplayName != "Composed" || model.Capabilities.ContextWindow != 4242 {
		_, buildErr := BuildModel("radius-dev/custom", services)
		t.Fatalf("configured model = %+v; load=%s build=%v", model, services.Registry().LoadError(), buildErr)
	}
	message := runtime.Complete(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}}}, ai.StreamOptions{})
	if message.StopReason != ai.StopReasonStop {
		t.Fatalf("completion = %+v", message)
	}
	headers := <-requests
	if headers.Get("Authorization") != "Bearer configured-key" || headers.Get("X-Provider") != "provider" || headers.Get("X-Model") != "model" || headers.Get("X-Override") != "override" {
		t.Fatalf("composed request headers = %v", headers)
	}
}
