//go:build !pig_strip_node_extensions

package subprocess_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// TestNodeNativeProviderCallbackFromAnotherRequest pins the ownership of a reverse
// provider callback. The Host declares onPayload for one provider call
// (provider-composer.ts:361-366 passes the caller's hook to the Provider), and a real
// Provider invokes it from its own transport chain, which is often while the extension
// is handling a different Host request. The callback still belongs to the provider call
// that declared it, so the Host must answer it there.
func TestNodeNativeProviderCallbackFromAnotherRequest(t *testing.T) {
	runNativeProviderCallbackProbe(t, "")
}

// TestNodeNativeProviderCallbackWithoutAmbientRequest covers a Provider that invokes the
// declared callback from a chain with no Host request at all (a transport timer or
// socket created outside any request): the callback still answers its provider call.
func TestNodeNativeProviderCallbackWithoutAmbientRequest(t *testing.T) {
	runNativeProviderCallbackProbe(t, "detached")
}

func runNativeProviderCallbackProbe(t *testing.T, mode string) {
	t.Helper()
	dir := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	host := subprocess.NewHostWithConfigRoot(dir, dir)
	defer host.Shutdown("test done")
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	host.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
	host.SetUIBridge(subprocess.NewUIBridge(nil))

	path, err := filepath.Abs(filepath.Join("testdata", "node-native-provider-payload.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, loadErr := host.Load(t.Context(), subprocess.ExtConfig{Name: "node-native-provider-payload", Source: path, Enabled: true})
	if loadErr != nil || loaded == nil {
		t.Fatalf("load: %v", loadErr)
	}
	runner := inproc.NewRunner([]extension.Extension{*loaded}, dir)
	model := services.ModelRuntime().GetModel("native-probe", "native-model")
	if model == nil {
		t.Fatal("the native model is absent from the catalog")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	var payloadCalls int
	options := ai.StreamOptions{OnPayload: func(payload any, _ *ai.Model) (any, error) {
		if payload == nil {
			t.Error("the provider sent an empty payload to the caller's hook")
		}
		payloadCalls++
		return nil, nil
	}}
	stream := services.ModelRuntime().StreamSimple(ctx, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("question")}}}, options)
	events := make(chan struct{})
	finished := make(chan struct{})
	var first sync.Once
	var text string
	go func() {
		defer close(finished)
		for event := range stream.Events(ctx) {
			first.Do(func() { close(events) })
			if done, ok := event.(ai.DoneEvent); ok {
				for _, block := range done.Message.Content {
					if t2, ok := block.(ai.TextContent); ok {
						text += t2.Text
					}
				}
			}
		}
	}()
	select {
	case <-events:
	case <-finished:
		t.Fatalf("the native stream ended before it started: %+v", stream.Result())
	}

	// The provider stream is in flight. Its declared payload callback is now invoked
	// from the request the Host sends for this command.
	if !runner.ExecuteCommand(ctx, "payload_probe", mode) {
		t.Fatal("the payload probe command did not run")
	}
	<-finished
	result := stream.Result()
	if result.StopReason != ai.StopReasonStop {
		t.Fatalf("native stream result = %+v", result)
	}
	if text != "native answer" {
		t.Fatalf("native stream text = %q, want %q", text, "native answer")
	}
	if payloadCalls != 1 {
		t.Fatalf("caller payload hooks = %d, want 1", payloadCalls)
	}
}

// TestNodeNativeProviderReceivesTheCallersOnResponseAndOnPayloadByName pins the dispatch of a native provider's reverse callbacks by their name
// (Pi types.ts:1917-1920: streamSimple invokes options.onPayload before the request and options.onResponse after the response; sdk.ts:387-433
// handleProviderResponse is the caller that turns the latter into after_provider_response; 1918-1919: it may invoke
// options.onProviderStreamEvent, which handleProviderStreamEvent turns into provider_stream_event). The payload hook must see only payloads,
// the response hook only the response status and headers, and the stream event hook only the parsed event.
func TestNodeNativeProviderReceivesTheCallersOnResponseAndOnPayloadByName(t *testing.T) {
	dir := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	host := subprocess.NewHostWithConfigRoot(dir, dir)
	defer host.Shutdown("test done")
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	host.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
	host.SetUIBridge(subprocess.NewUIBridge(nil))
	path, err := filepath.Abs(filepath.Join("testdata", "node-native-provider-response.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, loadErr := host.Load(t.Context(), subprocess.ExtConfig{Name: "node-native-provider-response", Source: path, Enabled: true})
	if loadErr != nil || loaded == nil {
		t.Fatalf("load: %v", loadErr)
	}
	_ = inproc.NewRunner([]extension.Extension{*loaded}, dir)
	model := services.ModelRuntime().GetModel("native-probe", "native-model")
	if model == nil {
		t.Fatal("the native model is absent from the catalog")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	var mu sync.Mutex
	var payloads []any
	var responses []ai.ProviderResponse
	var streamEvents []any
	options := ai.StreamOptions{
		OnProviderStreamEvent: func(_ context.Context, data any, _ *ai.Model) error {
			mu.Lock()
			defer mu.Unlock()
			streamEvents = append(streamEvents, data)
			return nil
		},
		OnPayload: func(payload any, _ *ai.Model) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			payloads = append(payloads, payload)
			return nil, nil
		},
		OnResponse: func(_ context.Context, response ai.ProviderResponse, _ *ai.Model) error {
			mu.Lock()
			defer mu.Unlock()
			responses = append(responses, response)
			return nil
		},
	}
	stream := services.ModelRuntime().StreamSimple(ctx, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("question")}}}, options)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for range stream.Events(ctx) {
		}
	}()
	select {
	case <-finished:
	case <-time.After(60 * time.Second):
		t.Fatal("the native stream never finished")
	}
	result := stream.Result()
	if result.StopReason != ai.StopReasonStop {
		t.Fatalf("native stream result = %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 1 {
		t.Fatalf("the caller's payload hook ran %d times with %v, want once with the payload", len(payloads), payloads)
	}
	if len(responses) != 1 || responses[0].Status != 201 || responses[0].Headers["x-probe"] != "yes" {
		t.Fatalf("the caller's response hook saw %+v, want one 201 response with x-probe: yes", responses)
	}
	// types.ts:1918-1919: the provider's parsed stream event reaches the caller's onProviderStreamEvent unchanged.
	if len(streamEvents) != 1 || fmt.Sprint(streamEvents[0]) != "map[index:1 raw:chunk]" {
		t.Fatalf("the caller's provider stream event hook saw %v, want one {raw: chunk, index: 1}", streamEvents)
	}
}
