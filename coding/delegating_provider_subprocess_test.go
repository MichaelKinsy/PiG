package coding

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// An extension provider whose streamSimple calls pi-ai's compat streamSimple for a stock API completes through the real Session, Model Runtime, Extension Host and Node runtime. Pi dispatches that call on model.api through the pi-ai api registry (packages/ai/src/compat.ts:278-293) and never re-enters the registered provider. PiG re-entered the provider's own streamSimple without bound (issue #164, pi-commandcode-provider): the extension process stopped answering heartbeats and the cell was killed.
func TestRegisteredProviderDelegatingToCompatStreamSimpleReachesTheAPI(t *testing.T) {
	services := newTestServices(t)
	session, err := NewSession(services, SessionOptions{Model: &ai.Model{ID: "primary", Provider: &scriptedProvider{}}, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test done")
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	bridge := subprocess.NewUIBridge(func() {})
	detach := icodingagent.WireModelOperations(bridge, icodingagent.ModelOperationBindings{CurrentModel: session.Model, ModelLookup: services.ModelRuntime().GetModel, ModelCatalog: func(...string) []*ai.Model { return services.ModelRuntime().GetModels() }, Registry: services.Registry().ModelRegistry, ModelBuilder: func(spec string) (*ai.Model, error) { return BuildModel(spec, services) }, SessionHandle: session})
	defer detach()
	host.SetUIBridge(bridge)
	ext, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "delegating-provider", Source: filepath.Join("testdata", "delegating-provider.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	responses := map[ai.API]string{
		ai.APIOpenAICompletions: "data: {\"choices\":[{\"delta\":{\"content\":\"delegated\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		ai.APIOpenAIResponses:   "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n",
		ai.APIAnthropicMessages: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"delegated\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	for api, response := range responses {
		for _, registeredAPI := range []ai.API{"delegating-api", api} {
			t.Run(string(api)+"/registered="+string(registeredAPI), func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte(response))
				}))
				defer server.Close()
				provider := "delegating-" + string(api) + "-" + string(registeredAPI)
				if err := ext.Commands["register-delegating"].Handler(t.Context(), fmt.Sprintf("%s %s %s %s", provider, api, server.URL, registeredAPI)); err != nil {
					t.Fatal(err)
				}
				chat := services.ModelRuntime().GetModel(provider, "chat")
				if chat == nil {
					t.Fatal("the delegating provider's model is not registered")
				}
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				result := services.ModelRuntime().CompleteSimple(ctx, chat, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{APIKey: "delegating-key"})
				if result.StopReason != ai.StopReasonStop || requests.Load() != 1 || (api != ai.APIOpenAIResponses && lateProviderText(result) != "delegated") {
					t.Fatalf("requests=%d result=%+v", requests.Load(), result)
				}
			})
		}
	}
}
