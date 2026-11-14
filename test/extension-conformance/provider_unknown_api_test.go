package extensionconformance

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// providerUnknownAPIAnswer loads one fixture of a case, points the base URL of every provider it registers at a counting listener, and completes a request for the model of
// provider. It returns the result and the number of requests the listener saw.
func providerUnknownAPIAnswer(t *testing.T, tc providerCancelCase, provider string) (*ai.AssistantMessage, int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test done")
	host.SetProviderCallbacks(func(name string, config extension.ProviderConfig) error {
		config.BaseURL = server.URL + "/v1"
		return services.Registry().RegisterExtensionProvider(name, config)
	}, services.Registry().UnregisterProvider)
	if err := loadProviderFixture(t, host, tc.name, tc.source, tc.fused, tc.packed); err != nil {
		t.Fatal(err)
	}
	applyQueuedRegistrations(host)
	model := services.ModelRuntime().GetModel(provider, "faux")
	if model == nil {
		t.Fatalf("%s/faux is not registered", provider)
	}
	result := services.ModelRuntime().Complete(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{APIKey: "extension-key"})
	return result, requests.Load()
}

// A provider an extension registers whose model names an api that no extension registered, and with no streamSimple of its own, is refused when its request starts with the
// text of Pi, "No API provider registered for api: <api>", and nothing is sent to its base URL (provider-composer.ts:605-606), whichever SDK registered it and however it is
// loaded. A provider of the same extension that registers its own streamSimple for its api still runs its implementation.
func TestExtensionProviderWithAnUnregisteredAPIIsRefusedAcrossSDKs(t *testing.T) {
	cases := []providerCancelCase{
		{name: "go-isolated", source: "testdata/provider-go"}, {name: "go-packed", source: "testdata/provider-go", packed: true},
		{name: "python-isolated", source: "testdata/provider-python"}, {name: "python-packed", source: "testdata/provider-python", packed: true},
		{name: "rust-isolated", source: "testdata/provider-rust"}, {name: "rust-packed", source: "testdata/provider-rust", packed: true},
		{name: "lisp-isolated", source: "testdata/provider-lisp"}, {name: "lisp-packed", source: "testdata/provider-lisp", packed: true},
		{name: "node-isolated", source: "testdata/provider-snapshot.mjs"}, {name: "node-packed", source: "testdata/provider-snapshot.mjs", packed: true},
		{name: "fused-go", fused: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			refused, requests := providerUnknownAPIAnswer(t, tc, "unknown-api-provider")
			if want := "No API provider registered for api: unknown-api-no-extension-registered"; refused.StopReason != ai.StopReasonError || refused.ErrorMessage != want {
				t.Errorf("the request for the unregistered api ended %q with the error %q, want an error with %q", refused.StopReason, refused.ErrorMessage, want)
			}
			if requests != 0 {
				t.Errorf("the base URL of the unregistered api received %d requests, want 0", requests)
			}
			ran, requests := providerUnknownAPIAnswer(t, tc, "snapshot-provider")
			if ran.StopReason != ai.StopReasonStop || ran.ErrorMessage != "" {
				t.Errorf("the request for the extension-registered api ended %q with the error %q, want its implementation result", ran.StopReason, ran.ErrorMessage)
			}
			if requests != 0 {
				t.Errorf("the base URL of the extension-registered api received %d requests, want 0 (its implementation answers)", requests)
			}
		})
	}
}
