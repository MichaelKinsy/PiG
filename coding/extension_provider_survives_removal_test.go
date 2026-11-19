//go:build !pig_strip_node_extensions

package coding

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi never unregisters an extension provider on reload: ModelRuntime keeps the registration, and its streamSimple closure, until the process ends
// or an extension unregisters it (agent-session.ts:3815-3855 reload, model-runtime.ts:965-993; probed on Pi 1.5.2: after the extension file was removed
// and /reload ran twice the provider was still listed, still selectable and still answered from the old closure). A removed extension's provider must
// keep answering in PiG too.
func TestRemovedExtensionProviderStillAnswersAfterReload(t *testing.T) {
	services := newTestServices(t)
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test done")
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	if _, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "provider-stream-producer", Source: filepath.Join("testdata", "provider-stream-producer.mjs"), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	applyQueuedRegistrations(host)
	answer := func(stage string) {
		t.Helper()
		model := services.ModelRuntime().GetModel("extension-provider", "faux")
		if model == nil {
			t.Fatalf("%s: the extension provider is no longer listed", stage)
		}
		request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}
		message := services.ModelRuntime().StreamSimple(t.Context(), model, request, ai.StreamOptions{}).Result()
		if message.StopReason == ai.StopReasonError {
			t.Fatalf("%s: the provider failed: %s", stage, message.ErrorMessage)
		}
		if text, _ := message.Content[0].(ai.TextContent); text.Text != "custom provider response" {
			t.Fatalf("%s: answer = %#v", stage, message.Content)
		}
	}
	answer("before the reload")
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return nil, nil })
	for _, stage := range []string{"after the first reload", "after the second reload"} {
		if _, err := host.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
		answer(stage)
	}
}
