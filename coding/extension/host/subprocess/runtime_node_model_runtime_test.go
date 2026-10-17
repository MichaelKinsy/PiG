//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestNodeModelRegistryStreamsThroughHostRuntime(t *testing.T) {
	shortSockDir(t)
	host := newTestHost(t)
	bridge := NewUIBridge(func() {})
	model := map[string]any{"id": "openrouter/org/model/name", "modelId": "org/model/name", "provider": map[string]any{"id": "openrouter"}, "api": "openai-responses"}
	bridge.SetHostAction("getModelInfo", func() json.RawMessage { return modelJSON(model) })
	bridge.SetHostAction("getModels", func() []json.RawMessage { return modelJSONs(model) })
	var calls int
	bridge.SetHostAction("streamModel", func(_ context.Context, model map[string]any, _ map[string]any) (*ai.AssistantMessageEventStream, error) {
		calls++
		if model["provider"] != "openrouter" || model["modelId"] != "org/model/name" {
			t.Fatalf("model = %#v", model)
		}
		start := &ai.AssistantMessage{Provider: "openrouter", Model: "org/model/name", StopReason: ai.StopReasonPending}
		partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "streamed"}}, Provider: "openrouter", Model: "org/model/name", StopReason: ai.StopReasonPending}
		final := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "streamed"}}, Provider: "openrouter", Model: "org/model/name", StopReason: ai.StopReasonStop}
		stream := ai.NewAssistantMessageEventStream()
		for _, event := range []ai.AssistantMessageEvent{
			ai.StartEvent{Partial: start}, ai.TextStartEvent{ContentIndex: 0, Partial: partial},
			ai.TextDeltaEvent{ContentIndex: 0, Delta: "streamed", Partial: partial},
			ai.TextEndEvent{ContentIndex: 0, Content: "streamed", Partial: partial},
			ai.DoneEvent{Reason: ai.StopReasonStop, Message: final},
		} {
			if err := stream.Push(event); err != nil {
				t.Fatal(err)
			}
		}
		return stream, nil
	})
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := host.Load(ctx, ExtConfig{Name: "node-model-runtime", Source: filepath.Join("testdata", "node-model-runtime.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	command, ok := ext.Commands["model_registry_runtime"]
	if !ok {
		t.Fatal("model_registry_runtime command not registered")
	}
	if err := command.Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("stream calls = %d, want 3", calls)
	}
}

// Pi's compat completeSimple runs through the builtin API bridge (D74): the
// request carries the extension's apiKey and no session thinking level, and an
// aborted options.signal cancels the host provider request, which ends the
// stream as aborted.
func TestNodeCompatCompletionAbortCancelsHostRequest(t *testing.T) {
	shortSockDir(t)
	host := newTestHost(t)
	bridge := NewUIBridge(func() {})
	model := map[string]any{"id": "openrouter/org/model/name", "modelId": "org/model/name", "provider": map[string]any{"id": "openrouter"}, "api": "openai-completions"}
	bridge.SetHostAction("getModelInfo", func() json.RawMessage { return modelJSON(model) })
	bridge.SetHostAction("getModels", func() []json.RawMessage { return modelJSONs(model) })
	cancelled := make(chan struct{})
	var request map[string]any
	bridge.SetHostAction("streamModel", func(ctx context.Context, _ map[string]any, got map[string]any) (*ai.AssistantMessageEventStream, error) {
		request = got
		stream := ai.NewAssistantMessageEventStream()
		start := &ai.AssistantMessage{Provider: "openrouter", Model: "org/model/name", StopReason: ai.StopReasonPending}
		if err := stream.Push(ai.StartEvent{Partial: start}); err != nil {
			return nil, err
		}
		go func() {
			<-ctx.Done()
			close(cancelled)
			aborted := &ai.AssistantMessage{Provider: "openrouter", Model: "org/model/name", StopReason: ai.StopReasonAborted, ErrorMessage: "Request aborted"}
			_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: aborted})
		}()
		return stream, nil
	})
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := host.Load(ctx, ExtConfig{Name: "node-model-abort", Source: filepath.Join("testdata", "node-model-abort.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := ext.Commands["model_abort"].Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("the extension's abort did not cancel the host request")
	}
	if request["apiKey"] != "request-key" || request["reasoning"] != "off" {
		t.Fatalf("request = %#v, want apiKey request-key and thinking off", request)
	}
	if _, ok := request["signal"]; ok {
		t.Fatalf("request carries the AbortSignal: %#v", request)
	}
}

// A module that calls a pi-ai function at top level, or in its factory, before it registers anything must still load and get its answer: Pi runs pi-ai in the
// extension's own process with no registration gate (loader.ts loads the module and awaits the factory before any registration is read). The Host answers the
// model stream during loading, and delivers its events on the loading connection, ahead of the register handshake the load waits for.
func TestNodeLoadTimeCompletionAnswersBeforeRegistration(t *testing.T) {
	for _, layout := range []string{"isolated", "packed"} {
		t.Run(layout, func(t *testing.T) { loadTimeCompletion(t, layout == "packed") })
	}
}

func loadTimeCompletion(t *testing.T, packed bool) {
	shortSockDir(t)
	host := newTestHost(t)
	bridge := NewUIBridge(func() {})
	var mu sync.Mutex
	var requests []map[string]any
	bridge.SetHostAction("streamModel", func(_ context.Context, model map[string]any, request map[string]any) (*ai.AssistantMessageEventStream, error) {
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		start := &ai.AssistantMessage{Provider: "probe-provider", Model: "m", StopReason: ai.StopReasonPending}
		final := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "pong"}}, Provider: "probe-provider", Model: "m", StopReason: ai.StopReasonStop}
		stream := ai.NewAssistantMessageEventStream()
		for _, event := range []ai.AssistantMessageEvent{ai.StartEvent{Partial: start}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: final}} {
			if err := stream.Push(event); err != nil {
				return nil, err
			}
		}
		return stream, nil
	})
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	source, err := filepath.Abs(filepath.Join("testdata", "node-load-time-completion.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	peer, err := filepath.Abs(filepath.Join("testdata", "node-model-abort.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	var ext *extension.Extension
	if packed {
		// Two Node factories share one process: the load-time call is made by a member of a packed cell.
		configs := []ExtConfig{{Name: "node-load-time-completion", Source: source, Enabled: true}, {Name: "node-load-time-peer", Source: peer, Enabled: true}}
		host.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
		loaded, errs := host.LoadAll(ctx, configs)
		if len(errs) != 0 {
			t.Fatalf("the extensions did not load: %v", errs)
		}
		for i := range loaded {
			if _, ok := loaded[i].Commands["load_time_completion"]; ok {
				ext = &loaded[i]
			}
		}
		if ext == nil {
			t.Fatal("load_time_completion command not registered")
		}
	} else {
		if ext, err = host.Load(ctx, ExtConfig{Name: "node-load-time-completion", Source: source, Enabled: true}); err != nil {
			t.Fatalf("the extension did not load: %v", err)
		}
	}
	command, ok := ext.Commands["load_time_completion"]
	if !ok {
		t.Fatal("load_time_completion command not registered")
	}
	if err := command.Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("the host streamed %d load-time requests, want 2 (top level, then factory)", len(requests))
	}
	for _, request := range requests {
		if request["apiKey"] != "k" {
			t.Fatalf("request = %#v, want the module's apiKey", request)
		}
	}
}

// Every extension call is answered. A Host with no UI bridge, no call hook and no early model stream used to drop a call it had no handler for, and the Node module
// that awaited it (a pi-ai call at load) waited for ever: the load ended with "register handshake: context deadline exceeded".
func TestHostAnswersACallItHasNoHandlerFor(t *testing.T) {
	// The coding package installs the early model stream when it is linked in; this test host has none.
	previous := earlyModelStream.Swap(nil)
	t.Cleanup(func() { earlyModelStream.Store(previous) })
	shortSockDir(t)
	host := newTestHost(t)
	defer host.Shutdown("test done")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	source, err := filepath.Abs(filepath.Join("testdata", "node-load-time-unhandled.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	ext, err := host.Load(ctx, ExtConfig{Name: "node-load-time-unhandled", Source: source, Enabled: true})
	if err != nil {
		t.Fatalf("the extension did not load: %v", err)
	}
	if err := ext.Commands["load_time_unhandled"].Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
}
