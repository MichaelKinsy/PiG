package coding

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// model-runtime.ts streamDeferred, fetchDeferred and cancelDeferred send a model of a provider an extension registered to that provider object's fetchDeferred/cancelDeferred after the
// request authentication its stream gets: the resolved key and base URL and the caller's options reach the provider, a provider without the operation fails with "does not support deferred
// responses", a provider that errors or returns no stream fails the request, and an unconfigured provider fails before the provider is called.
// Pi: packages/coding-agent/src/core/model-runtime.ts streamDeferred/fetchDeferred/cancelDeferred.
func TestModelRuntimeDeferredOperationsOfAnExtensionRegisteredProvider(t *testing.T) {
	const id = "ext-deferred"
	register := func(t *testing.T, configure func(*extension.NativeProvider)) (*ModelRuntime, *ai.Model) {
		t.Helper()
		services := newRegistrationServices(t)
		provider := &extension.NativeProvider{ID: id, Name: "Ext", Models: []extension.ProviderModelConfig{{ID: "model", API: ai.APIOpenAICompletions, BaseURL: "https://ext.invalid"}},
			CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) {
				return &ai.AuthCheck{Type: ai.CredentialAPIKey}, nil
			},
			ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
				return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "resolved-key", BaseURL: "https://resolved.test/v1"}}, nil, nil
			},
			ResolveRefreshCredential: func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) { return nil, nil, nil },
			Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				return ai.NewAssistantMessageEventStream(), nil
			},
		}
		configure(provider)
		if err := services.Registry().RegisterNativeProvider(t.Context(), provider); err != nil {
			t.Fatal(err)
		}
		services.Registry().YieldToRegistrationRefresh(t.Context())
		model := services.ModelRuntime().GetModel(id, "model")
		if model == nil {
			t.Fatal("registered model missing")
		}
		return services.ModelRuntime(), model
	}
	// A request that never settles is a failure of the test, not a hang.
	within := func(t *testing.T, fetch func() *ai.AssistantMessage) *ai.AssistantMessage {
		t.Helper()
		done := make(chan *ai.AssistantMessage, 1)
		go func() { done <- fetch() }()
		select {
		case reply := <-done:
			return reply
		case <-time.After(5 * time.Second):
			t.Fatal("the deferred request never settled")
			return nil
		}
	}
	handle := ai.DeferredHandle{Provider: id, ModelID: "model", API: ai.APIOpenAICompletions, ID: "h1"}
	doneStream := func(request *ai.Model) *ai.AssistantMessageEventStream {
		message := &ai.AssistantMessage{API: request.ProviderMeta.API, Provider: request.ProviderMeta.ProviderID, Model: request.ID, Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonStop}
		stream := ai.NewAssistantMessageEventStream()
		_ = stream.Push(ai.StartEvent{Partial: message})
		_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
		return stream
	}

	t.Run("fetch forwards the authenticated request and the options", func(t *testing.T) {
		var gotURL string
		var gotOptions ai.DeferredFetchOptions
		var gotHandle ai.DeferredHandle
		runtime, model := register(t, func(p *extension.NativeProvider) {
			p.FetchDeferred = func(_ context.Context, request *ai.Model, h ai.DeferredHandle, options ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error) {
				gotURL, gotOptions, gotHandle = request.ProviderMeta.BaseURL, options, h
				return doneStream(request), nil
			}
		})
		reply := runtime.FetchDeferred(t.Context(), model, handle, ai.DeferredFetchOptions{Wait: new(float64(25)), StreamOptions: ai.StreamOptions{Headers: ai.ProviderHeaders{"X-A": new("a")}}})
		if reply.StopReason != ai.StopReasonStop || reply.Model != "model" {
			t.Fatalf("reply = %+v", reply)
		}
		if gotURL != "https://resolved.test/v1" || gotOptions.APIKey != "resolved-key" || gotOptions.Wait == nil || *gotOptions.Wait != 25 || gotOptions.Headers["X-A"] == nil || gotHandle.ID != "h1" {
			t.Errorf("provider saw url=%q options=%+v handle=%+v", gotURL, gotOptions, gotHandle)
		}
		streamed := runtime.StreamDeferred(t.Context(), model, handle)
		if result := streamed.Result(); result.StopReason != ai.StopReasonStop {
			t.Errorf("StreamDeferred without options = %+v", result)
		}
		if gotOptions.Wait != nil || gotOptions.APIKey != "resolved-key" {
			t.Errorf("an absent options argument reached the provider as %+v", gotOptions)
		}
	})

	t.Run("fetch failures become the failed message", func(t *testing.T) {
		for name, tc := range map[string]struct {
			fetch func(context.Context, *ai.Model, ai.DeferredHandle, ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error)
			want  string
		}{
			"no fetchDeferred": {nil, "Provider ext-deferred does not support deferred responses"},
			"provider error": {func(context.Context, *ai.Model, ai.DeferredHandle, ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error) {
				return nil, errors.New("fetch exploded")
			}, "fetch exploded"},
			"nil stream": {func(context.Context, *ai.Model, ai.DeferredHandle, ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error) {
				return nil, nil
			}, `provider "ext-deferred" returned a nil stream`},
		} {
			t.Run(name, func(t *testing.T) {
				runtime, model := register(t, func(p *extension.NativeProvider) { p.FetchDeferred = tc.fetch })
				reply := within(t, func() *ai.AssistantMessage { return runtime.FetchDeferred(t.Context(), model, handle) })
				if reply.StopReason != ai.StopReasonError || !strings.Contains(reply.ErrorMessage, tc.want) {
					t.Errorf("reply = %+v, want an error containing %q", reply, tc.want)
				}
			})
		}
		t.Run("unconfigured provider", func(t *testing.T) {
			called := false
			runtime, model := register(t, func(p *extension.NativeProvider) {
				p.ResolveAuth = func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
					return nil, nil, nil
				}
				p.FetchDeferred = func(_ context.Context, request *ai.Model, _ ai.DeferredHandle, _ ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error) {
					called = true
					return doneStream(request), nil
				}
			})
			reply := within(t, func() *ai.AssistantMessage { return runtime.FetchDeferred(t.Context(), model, handle) })
			if called || reply.StopReason != ai.StopReasonError || !strings.Contains(reply.ErrorMessage, "provider is not configured: ext-deferred") {
				t.Errorf("called=%v reply=%+v", called, reply)
			}
		})
	})

	t.Run("cancel forwards the authenticated request and the options", func(t *testing.T) {
		var gotURL string
		var gotOptions ai.DeferredCancelOptions
		var gotHandle ai.DeferredHandle
		runtime, model := register(t, func(p *extension.NativeProvider) {
			p.CancelDeferred = func(_ context.Context, request *ai.Model, h ai.DeferredHandle, options ai.DeferredCancelOptions) error {
				gotURL, gotOptions, gotHandle = request.ProviderMeta.BaseURL, options, h
				return nil
			}
		})
		if err := runtime.CancelDeferred(t.Context(), model, handle, ai.DeferredCancelOptions{TimeoutMs: new(100)}); err != nil {
			t.Fatal(err)
		}
		if gotURL != "https://resolved.test/v1" || gotOptions.APIKey != "resolved-key" || gotOptions.TimeoutMs == nil || *gotOptions.TimeoutMs != 100 || gotHandle.ID != "h1" {
			t.Errorf("provider saw url=%q options=%+v handle=%+v", gotURL, gotOptions, gotHandle)
		}
		if err := runtime.CancelDeferred(t.Context(), model, handle); err != nil || gotOptions.TimeoutMs != nil {
			t.Errorf("an absent options argument reached the provider as %+v: %v", gotOptions, err)
		}
	})

	t.Run("cancel failures", func(t *testing.T) {
		runtime, model := register(t, func(*extension.NativeProvider) {})
		if err := runtime.CancelDeferred(t.Context(), model, handle); err == nil || !strings.Contains(err.Error(), "Provider ext-deferred does not support deferred responses") {
			t.Errorf("no cancelDeferred: %v", err)
		}
		called := false
		runtime, model = register(t, func(p *extension.NativeProvider) {
			p.ResolveAuth = func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
				return nil, nil, nil
			}
			p.CancelDeferred = func(context.Context, *ai.Model, ai.DeferredHandle, ai.DeferredCancelOptions) error {
				called = true
				return nil
			}
		})
		if err := runtime.CancelDeferred(t.Context(), model, handle); err == nil || !strings.Contains(err.Error(), "provider is not configured: ext-deferred") || called {
			t.Errorf("unconfigured provider: err=%v called=%v", err, called)
		}
	})
}
