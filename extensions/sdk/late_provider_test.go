package sdk

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A provider registered after the factory finished, through ctx.modelRegistry.registerProvider, carries the same implementations a factory registration does. Pi's pi.registerProvider takes the whole ProviderConfig, callbacks included, at any time (.upstream/v0.99.2/packages/coding-agent/src/core/extensions/types.ts:1766-1803,1875-1903, loader.ts:449-457, runner.ts:517-523). The callbacks stay in the extension; the host call names them as the register payload does (image_apis, classifier_apis, stream_simple) and the host runs each through a provider_operation or provider_stream_simple request.

var lateZero = map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}

func lateProviderConfig(ran chan<- string) ProviderConfig {
	return ProviderConfig{
		"api": "late-chat-api", "baseUrl": "https://late.test/v1", "apiKey": "late-key",
		"models": []any{
			map[string]any{"id": "chat", "name": "Chat", "reasoning": false, "input": []string{"text"}, "cost": lateZero, "contextWindow": 1000, "maxTokens": 100},
			map[string]any{"id": "flux", "name": "Flux", "type": "image", "api": "late-images", "input": []string{"text"}, "output": []string{"image"}, "cost": lateZero},
		},
		"images": map[string]ProviderImagesFunc{"late-images": func(model, request map[string]any, options ProviderOperationOptions) (map[string]any, error) {
			ran <- "images:" + model["id"].(string) + ":" + options.Values["apiKey"].(string)
			return map[string]any{"api": "late-images", "provider": "late", "model": "flux", "output": []any{}, "stopReason": "stop", "timestamp": 1}, nil
		}},
		"classifiers": map[string]ProviderClassifyFunc{"late-classifier": func(model map[string]any, request ClassifierContext, options ProviderOperationOptions) (ClassifierResult, error) {
			ran <- "classifier"
			return ClassifierResult{API: "late-classifier", Provider: "late", Model: "cls", Answers: NewOrderedObject(), StopReason: "stop", Timestamp: 2}, nil
		}},
		"streamSimple": ProviderStreamSimpleFunc(func(ctx Context, model, request, options map[string]any) (*ModelEventStream, error) {
			ran <- "stream:" + model["id"].(string) + ":" + options["apiKey"].(string)
			message := map[string]any{"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": []any{map[string]any{"type": "text", "text": "late"}}, "stopReason": "stop", "timestamp": 1}
			stream := CreateAssistantMessageEventStream()
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message})
			return stream, nil
		}),
	}
}

func TestModelRegistryRegisterProviderKeepsOperationsAfterTheFactory(t *testing.T) {
	ext := New("late")
	ran := make(chan string, 8)
	registered := make(chan error, 1)
	ext.Command("late", "", func(ctx Context, _ string) error {
		registered <- ctx.ModelRegistry().RegisterProvider("late", lateProviderConfig(ran))
		return nil
	})
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	if len(reg.Providers) != 0 {
		t.Fatalf("the factory registered %+v", reg.Providers)
	}
	calls, resp := runSurfaceCommand(t, host, "late", func(*callMsg) *callResultMsg { return &callResultMsg{Result: json.RawMessage(`null`)} })
	if resp.Error != nil || len(calls) != 1 || calls[0].Method != "registerProvider" {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	if err := recv(t, registered); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	args := decodeArgs(t, calls[0].Args)
	config, _ := args["config"].(map[string]any)
	for _, key := range []string{"images", "classifiers", "streamSimple"} {
		if _, leaked := config[key]; leaked {
			t.Fatalf("the %s callbacks crossed the wire: %s", key, calls[0].Args)
		}
	}
	if args["name"] != "late" || args["stream_simple"] != true || !reflect.DeepEqual(args["image_apis"], []any{"late-images"}) || !reflect.DeepEqual(args["classifier_apis"], []any{"late-classifier"}) {
		t.Fatalf("registerProvider args = %s", calls[0].Args)
	}
	if config["baseUrl"] != "https://late.test/v1" || len(config["models"].([]any)) != 2 {
		t.Fatalf("config on the wire = %v", config)
	}

	// The host runs each implementation through the request the register payload's declaration also produces.
	_, resp = runSurfaceRequest(t, host, "img-1", &requestMsg{Method: "provider_operation", Tool: "late", Args: json.RawMessage(`{"kind":"images","api":"late-images","model":{"type":"image","id":"flux","provider":"late","api":"late-images"},"context":{"input":[]},"options":{"apiKey":"sk-late"}}`)}, nil)
	if resp.Error != nil || recv(t, ran) != "images:flux:sk-late" {
		t.Fatalf("images response = %+v", resp)
	}
	_, resp = runSurfaceRequest(t, host, "cls-1", &requestMsg{Method: "provider_operation", Tool: "late", Args: json.RawMessage(`{"kind":"classifiers","api":"late-classifier","model":{"id":"cls"},"context":{"state":{},"questions":{}}}`)}, nil)
	if resp.Error != nil || recv(t, ran) != "classifier" {
		t.Fatalf("classifier response = %+v", resp)
	}
	_, resp = runSurfaceRequest(t, host, "stream-1", &requestMsg{Method: "provider_stream_simple", Tool: "late", Args: json.RawMessage(`{"model":{"id":"chat","provider":"late","api":"late-chat-api"},"context":{"messages":[]},"options":{"apiKey":"sk-stream"}}`)}, nil)
	if resp.Error != nil || recv(t, ran) != "stream:chat:sk-stream" || !strings.Contains(string(resp.Result), `"text":"late"`) {
		t.Fatalf("stream response = %+v", resp)
	}
}

// model-runtime.ts:753-766 merges a re-registration's defined values over the previous one: a late registration that defines no operation keeps the implementations of the one before, so the extension must keep their callbacks.
func TestModelRegistryRegisterProviderMergesOperationsLikeTheFactory(t *testing.T) {
	ext := New("late")
	ran := make(chan string, 4)
	ext.Command("twice", "", func(ctx Context, _ string) error {
		if err := ctx.ModelRegistry().RegisterProvider("late", lateProviderConfig(ran)); err != nil {
			return err
		}
		return ctx.ModelRegistry().RegisterProvider("late", ProviderConfig{"baseUrl": "https://late.test/v2"})
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceCommand(t, host, "twice", func(*callMsg) *callResultMsg { return &callResultMsg{Result: json.RawMessage(`null`)} })
	if resp.Error != nil || len(calls) != 2 {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	second := decodeArgs(t, calls[1].Args)
	if _, declared := second["image_apis"]; declared || second["stream_simple"] == true {
		t.Fatalf("a registration without operations declared some: %s", calls[1].Args)
	}
	_, resp = runSurfaceRequest(t, host, "img-1", &requestMsg{Method: "provider_operation", Tool: "late", Args: json.RawMessage(`{"kind":"images","api":"late-images","model":{"id":"flux"},"context":{"input":[]},"options":{"apiKey":"k"}}`)}, nil)
	if resp.Error != nil || recv(t, ran) != "images:flux:k" {
		t.Fatalf("the kept images implementation: %+v", resp)
	}
}

// types.ts:1805-1819: unregisterProvider removes the registration; the extension drops the callbacks it held for it, so a request that still names the provider finds none.
func TestModelRegistryUnregisterProviderDropsItsOperations(t *testing.T) {
	ext := New("late")
	ran := make(chan string, 4)
	ext.Command("gone", "", func(ctx Context, _ string) error {
		if err := ctx.ModelRegistry().RegisterProvider("late", lateProviderConfig(ran)); err != nil {
			return err
		}
		return ctx.ModelRegistry().UnregisterProvider("late")
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceCommand(t, host, "gone", func(*callMsg) *callResultMsg { return &callResultMsg{Result: json.RawMessage(`null`)} })
	if resp.Error != nil || len(calls) != 2 || calls[1].Method != "unregisterProvider" {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	_, resp = runSurfaceRequest(t, host, "img-1", &requestMsg{Method: "provider_operation", Tool: "late", Args: json.RawMessage(`{"kind":"images","api":"late-images","model":{"id":"flux"},"context":{"input":[]},"options":{}}`)}, nil)
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "no image implementation") {
		t.Fatalf("an unregistered provider's images request = %+v", resp)
	}
	_, resp = runSurfaceRequest(t, host, "stream-1", &requestMsg{Method: "provider_stream_simple", Tool: "late", Args: json.RawMessage(`{"model":{"id":"chat"},"context":{"messages":[]},"options":{}}`)}, nil)
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "unknown provider stream") {
		t.Fatalf("an unregistered provider's stream request = %+v", resp)
	}
	select {
	case got := <-ran:
		t.Fatalf("an unregistered provider's callback ran: %s", got)
	default:
	}
}

// A callback of the wrong type is a programming error, as it is in a factory registration (the SDK panics there); the late call refuses it before the host hears of it.
func TestModelRegistryRegisterProviderRefusesAnInvalidCallback(t *testing.T) {
	ext := New("late")
	refused := make(chan any, 1)
	ext.Command("bad", "", func(ctx Context, _ string) error {
		defer func() { refused <- recover() }()
		_ = ctx.ModelRegistry().RegisterProvider("late", ProviderConfig{"streamSimple": "not a function"})
		return errors.New("registered")
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, _ := runSurfaceCommand(t, host, "bad", func(*callMsg) *callResultMsg { return &callResultMsg{Result: json.RawMessage(`null`)} })
	if got := recv(t, refused); got == nil || !strings.Contains(got.(string), "streamSimple") || len(calls) != 0 {
		t.Fatalf("panic = %v, calls = %+v", got, calls)
	}
}

// model-registry.test.ts:1320-1343 "failed registerProvider does not remove existing provider models": a registration the host refuses leaves the provider as it was, so the callbacks of the refused config must not replace the held ones.
func TestModelRegistryRefusedRegistrationKeepsTheHeldCallbacks(t *testing.T) {
	ext := New("late")
	ran := make(chan string, 4)
	refused := make(chan error, 1)
	ext.Command("refused", "", func(ctx Context, _ string) error {
		if err := ctx.ModelRegistry().RegisterProvider("late", lateProviderConfig(ran)); err != nil {
			return err
		}
		second := lateProviderConfig(ran)
		second["images"] = map[string]ProviderImagesFunc{"late-images": func(map[string]any, map[string]any, ProviderOperationOptions) (map[string]any, error) {
			ran <- "refused images"
			return map[string]any{"stopReason": "stop"}, nil
		}}
		refused <- ctx.ModelRegistry().RegisterProvider("late", second)
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	registrations := 0
	calls, resp := runSurfaceCommand(t, host, "refused", func(*callMsg) *callResultMsg {
		registrations++
		if registrations == 2 {
			return &callResultMsg{Error: &errorInfo{Message: "invalid model definitions"}}
		}
		return &callResultMsg{Result: json.RawMessage(`null`)}
	})
	if resp.Error != nil || len(calls) != 2 {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	if err := recv(t, refused); err == nil || !strings.Contains(err.Error(), "invalid model definitions") {
		t.Fatalf("the refused registration returned %v", err)
	}
	_, resp = runSurfaceRequest(t, host, "img-1", &requestMsg{Method: "provider_operation", Tool: "late", Args: json.RawMessage(`{"kind":"images","api":"late-images","model":{"id":"flux"},"context":{"input":[]},"options":{"apiKey":"k"}}`)}, nil)
	if resp.Error != nil || recv(t, ran) != "images:flux:k" {
		t.Fatalf("after a refused registration the held images callback = %+v", resp)
	}
}

// A refused callback leaves the extension as it was: the late call holds no provider lock after its panic and keeps the callbacks registered before, as Pi's validateExtensionProvider throws before it touches the stored registration (.upstream/v0.99.2/packages/coding-agent/src/core/model-runtime.ts registerProvider).
func TestModelRegistryInvalidCallbackLeavesTheProviderUsable(t *testing.T) {
	ext := New("late")
	ran := make(chan string, 4)
	refused := make(chan any, 1)
	ext.Command("bad", "", func(ctx Context, _ string) error {
		if err := ctx.ModelRegistry().RegisterProvider("late", lateProviderConfig(ran)); err != nil {
			return err
		}
		invalid := lateProviderConfig(ran)
		invalid["streamSimple"] = ProviderStreamSimpleFunc(func(Context, map[string]any, map[string]any, map[string]any) (*ModelEventStream, error) {
			ran <- "refused stream"
			return nil, errors.New("refused stream ran")
		})
		invalid["classifiers"] = "not a map of callbacks"
		defer func() { refused <- recover() }()
		_ = ctx.ModelRegistry().RegisterProvider("late", invalid)
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, _ := runSurfaceCommand(t, host, "bad", func(*callMsg) *callResultMsg { return &callResultMsg{Result: json.RawMessage(`null`)} })
	if got := recv(t, refused); got == nil || !strings.Contains(got.(string), "classifiers") || len(calls) != 1 {
		t.Fatalf("panic = %v, calls = %+v", got, calls)
	}
	// A provider lock the panic left held blocks the stream request forever; the deadline turns that into a failure.
	_ = host.nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, resp := runSurfaceRequest(t, host, "stream-1", &requestMsg{Method: "provider_stream_simple", Tool: "late", Args: json.RawMessage(`{"model":{"id":"chat","provider":"late","api":"late-chat-api"},"context":{"messages":[]},"options":{"apiKey":"sk-stream"}}`)}, nil)
	if resp.Error != nil || recv(t, ran) != "stream:chat:sk-stream" {
		t.Fatalf("stream response after a refused callback = %+v", resp)
	}
}

// A config value JSON cannot encode fails the late call, as the call's own encoding did before the callbacks were split out, and the host hears nothing.
func TestModelRegistryRegisterProviderReportsAnUnencodableConfig(t *testing.T) {
	ext := New("late")
	ran := make(chan string, 4)
	registered := make(chan error, 1)
	ext.Command("unencodable", "", func(ctx Context, _ string) error {
		if err := ctx.ModelRegistry().RegisterProvider("late", lateProviderConfig(ran)); err != nil {
			return err
		}
		config := lateProviderConfig(ran)
		delete(config, "images")
		config["headers"] = map[string]any{"x": make(chan int)}
		registered <- ctx.ModelRegistry().RegisterProvider("late", config)
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceCommand(t, host, "unencodable", func(*callMsg) *callResultMsg { return &callResultMsg{Result: json.RawMessage(`null`)} })
	if err := recv(t, registered); err == nil || resp.Error != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v, response = %+v", err, calls, resp)
	}
	_, resp = runSurfaceRequest(t, host, "img-1", &requestMsg{Method: "provider_operation", Tool: "late", Args: json.RawMessage(`{"kind":"images","api":"late-images","model":{"id":"flux"},"context":{"input":[]},"options":{"apiKey":"k"}}`)}, nil)
	if resp.Error != nil || recv(t, ran) != "images:flux:k" {
		t.Fatalf("the held images callback after an unencodable config = %+v", resp)
	}
}

// Pi's pi.registerProvider takes effect at once after the factory (types.ts:1766-1803, runner.ts:517-523), so Extension.RegisterProvider called once the extension runs sends the host call a ModelRegistry registration sends, with the same declaration, instead of queueing a registration nothing sends.
func TestExtensionRegisterProviderAfterRunSendsTheHostCall(t *testing.T) {
	ext := New("late")
	ran := make(chan string, 4)
	ext.Command("late", "", func(Context, string) error {
		ext.RegisterProvider("late", lateProviderConfig(ran))
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceCommand(t, host, "late", func(*callMsg) *callResultMsg { return &callResultMsg{Result: json.RawMessage(`null`)} })
	if resp.Error != nil || len(calls) != 1 || calls[0].Method != "registerProvider" {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	args := decodeArgs(t, calls[0].Args)
	if args["name"] != "late" || args["stream_simple"] != true || !reflect.DeepEqual(args["image_apis"], []any{"late-images"}) || !reflect.DeepEqual(args["classifier_apis"], []any{"late-classifier"}) {
		t.Fatalf("registerProvider args = %s", calls[0].Args)
	}
	_, resp = runSurfaceRequest(t, host, "img-1", &requestMsg{Method: "provider_operation", Tool: "late", Args: json.RawMessage(`{"kind":"images","api":"late-images","model":{"id":"flux"},"context":{"input":[]},"options":{"apiKey":"k"}}`)}, nil)
	if resp.Error != nil || recv(t, ran) != "images:flux:k" {
		t.Fatalf("the late images callback = %+v", resp)
	}
}

// A late registration the host refuses throws in Pi (model-runtime.ts registerProvider validates before it stores); Extension.RegisterProvider has no error result, so it panics with the host's error and keeps the callbacks the provider had.
func TestExtensionRegisterProviderAfterRunPanicsOnARefusal(t *testing.T) {
	ext := New("late")
	ran := make(chan string, 4)
	refused := make(chan any, 1)
	ext.Command("refused", "", func(Context, string) error {
		ext.RegisterProvider("late", lateProviderConfig(ran))
		second := lateProviderConfig(ran)
		second["images"] = map[string]ProviderImagesFunc{"late-images": func(map[string]any, map[string]any, ProviderOperationOptions) (map[string]any, error) {
			ran <- "refused images"
			return map[string]any{"stopReason": "stop"}, nil
		}}
		defer func() { refused <- recover() }()
		ext.RegisterProvider("late", second)
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	registrations := 0
	calls, _ := runSurfaceCommand(t, host, "refused", func(*callMsg) *callResultMsg {
		registrations++
		if registrations == 2 {
			return &callResultMsg{Error: &errorInfo{Message: "invalid model definitions"}}
		}
		return &callResultMsg{Result: json.RawMessage(`null`)}
	})
	if got, ok := recv(t, refused).(error); !ok || !strings.Contains(got.Error(), "invalid model definitions") || len(calls) != 2 {
		t.Fatalf("panic = %v, calls = %+v", got, calls)
	}
	_, resp := runSurfaceRequest(t, host, "img-1", &requestMsg{Method: "provider_operation", Tool: "late", Args: json.RawMessage(`{"kind":"images","api":"late-images","model":{"id":"flux"},"context":{"input":[]},"options":{"apiKey":"k"}}`)}, nil)
	if resp.Error != nil || recv(t, ran) != "images:flux:k" {
		t.Fatalf("after a refused registration the held images callback = %+v", resp)
	}
}

// Pi's pi.unregisterProvider also takes effect at once after the factory (types.ts:1805-1819): the host removes the provider before the extension drops its callbacks.
func TestExtensionUnregisterProviderAfterRunSendsTheHostCall(t *testing.T) {
	ext := New("late")
	ran := make(chan string, 4)
	ext.Command("gone", "", func(Context, string) error {
		ext.RegisterProvider("late", lateProviderConfig(ran))
		ext.UnregisterProvider("late")
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceCommand(t, host, "gone", func(*callMsg) *callResultMsg { return &callResultMsg{Result: json.RawMessage(`null`)} })
	if resp.Error != nil || len(calls) != 2 || calls[0].Method != "registerProvider" || calls[1].Method != "unregisterProvider" || decodeArgs(t, calls[1].Args)["name"] != "late" {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	_, resp = runSurfaceRequest(t, host, "img-1", &requestMsg{Method: "provider_operation", Tool: "late", Args: json.RawMessage(`{"kind":"images","api":"late-images","model":{"id":"flux"},"context":{"input":[]},"options":{}}`)}, nil)
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "no image implementation") {
		t.Fatalf("an unregistered provider's images request = %+v", resp)
	}
}

// Before Run the registration is queued for the register payload and makes no host call.
func TestExtensionRegisterProviderBeforeRunQueues(t *testing.T) {
	ext := New("late")
	ext.RegisterProvider("queued", lateProviderConfig(make(chan string, 1)))
	ext.RegisterProvider("dropped", ProviderConfig{"baseUrl": "https://dropped.test"})
	ext.UnregisterProvider("dropped")
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	if len(reg.Providers) != 1 || reg.Providers[0].Name != "queued" || !reg.Providers[0].StreamSimple || !reflect.DeepEqual(reg.Providers[0].ImageAPIs, []string{"late-images"}) {
		t.Fatalf("register payload providers = %+v", reg.Providers)
	}
}
