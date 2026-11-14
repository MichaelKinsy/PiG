package coding

// pi: packages/coding-agent/src/core/provider-composer.ts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func unknownAPIListener(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"listener says no"}}`))
	}))
	t.Cleanup(server.Close)
	return server.URL + "/v1", requests
}

func unknownAPIStream(t *testing.T, provider ai.Provider) error {
	t.Helper()
	stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{SystemPrompt: "be brief", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), ai.StreamOptions{})
	if err == nil {
		message := stream.Result()
		t.Fatalf("the stream started: %+v", message)
	}
	return err
}

// composeModelProvider's stream looks the model's api up in the API registry when the request starts and throws
// "No API provider registered for api: <api>" when nothing is registered (provider-composer.ts:603-604). The registry is a Map
// (compat.ts:102, getApiProvider at :142-143), so a member name of Object.prototype is an unknown name like any other. The
// provider is built without an error (the model stays selectable and listed); no request is built for the endpoint.
func TestUnknownAPINameIsRefusedBeforeAnyRequest(t *testing.T) {
	for _, name := range []string{"nosuchapi", "constructor", "__proto__", "toString", ""} {
		shown := name
		if name == "" {
			// A model object with no api: Pi reads api as undefined (compat.ts:244-247).
			shown = "undefined"
		}
		for _, shape := range []struct {
			label                 string
			resolvedAuth, apiLeaf bool
			explicitKey           string
		}{
			{label: "build-model"},
			{label: "request-resolved-auth", resolvedAuth: true, explicitKey: "k"},
			{label: "api-leaf", resolvedAuth: true, apiLeaf: true, explicitKey: "k"},
		} {
			t.Run(name+"/"+shape.label, func(t *testing.T) {
				baseURL, requests := unknownAPIListener(t)
				provider, err := buildProviderForEntry("odd", "m", ai.API(name), icodingagent.ModelEntry{
					ProviderID: "odd", ModelID: "m", BaseURL: baseURL, APIKey: "k", API: name, Input: []string{"text"},
				}, newRuntimeTestServices(t), shape.explicitKey, shape.resolvedAuth, shape.apiLeaf)
				if err != nil {
					t.Fatalf("building the provider failed (Pi refuses at the request, not when the model is built): %v", err)
				}
				t.Cleanup(func() { _ = provider.Close() })
				err = unknownAPIStream(t, provider)
				if want := "No API provider registered for api: " + shown; err.Error() != want {
					t.Errorf("error = %q, want %q", err, want)
				}
				if got := requests.Load(); got != 0 {
					t.Errorf("requests at the endpoint = %d, want 0", got)
				}
			})
		}
	}
}

// An api registered in the API registry is known: the model's request runs its implementation, and once it is unregistered the same
// provider refuses, because the lookup happens when the request starts (provider-composer.ts:603 inside lazyStream).
func TestRegisteredAPINameRunsItsImplementationUntilUnregistered(t *testing.T) {
	const api, source = ai.API("unknown-api-test-registered"), "unknown-api-test"
	reached := errors.New("registered implementation reached")
	implementation := func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		return nil, reached
	}
	ai.RegisterAPIProvider(ai.APIProvider{API: api, Stream: implementation, StreamSimple: implementation}, source)
	t.Cleanup(func() { ai.UnregisterAPIProviders(source) })
	baseURL, requests := unknownAPIListener(t)
	provider, err := buildProviderForEntry("odd", "m", api, icodingagent.ModelEntry{
		ProviderID: "odd", ModelID: "m", BaseURL: baseURL, APIKey: "k", API: string(api), Input: []string{"text"},
	}, newRuntimeTestServices(t), "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	if err := unknownAPIStream(t, provider); !strings.Contains(err.Error(), reached.Error()) {
		t.Errorf("registered api: error = %q, want the implementation's %q", err, reached)
	}
	ai.UnregisterAPIProviders(source)
	if err, want := unknownAPIStream(t, provider), "No API provider registered for api: "+string(api); err.Error() != want {
		t.Errorf("after unregistering: error = %q, want %q", err, want)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("requests at the endpoint = %d, want 0", got)
	}
}

// A model that no catalog, models.json or extension lists is built bare, with no api and no endpoint (the fallback of buildModel). Pi takes such an object to the stream and
// refuses it there with the api undefined; the request goes nowhere.
func TestBareModelIsRefusedAtItsRequest(t *testing.T) {
	services := newRuntimeTestServices(t)
	model, err := buildModel("bare-provider-nobody-lists/x", services, "k")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Provider.Close() })
	err = unknownAPIStream(t, model.Provider)
	if want := "No API provider registered for api: undefined"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// The Copilot provider serves anthropic-messages, openai-responses and openai-completions; a Copilot model whose api is none of them, or has none, is refused like any other
// model with no registered implementation, and nothing reaches the endpoint.
func TestCopilotModelWithNoCopilotAPIIsRefused(t *testing.T) {
	for _, name := range []string{"nosuchapi", ""} {
		t.Run("api="+name, func(t *testing.T) {
			baseURL, requests := unknownAPIListener(t)
			shown := name
			if name == "" {
				shown = "undefined"
			}
			provider, err := buildProviderForEntry("github-copilot", "m", ai.API(name), icodingagent.ModelEntry{
				ProviderID: "github-copilot", ModelID: "m", BaseURL: baseURL, APIKey: "k", API: name, Input: []string{"text"},
			}, newRuntimeTestServices(t), "", false, false)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = provider.Close() })
			err = unknownAPIStream(t, provider)
			if want := "No API provider registered for api: " + shown; err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
			if got := requests.Load(); got != 0 {
				t.Errorf("requests at the endpoint = %d, want 0", got)
			}
		})
	}
}
