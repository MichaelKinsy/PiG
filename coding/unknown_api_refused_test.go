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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		scheme, key, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		t.Errorf("a request reached the listener: %s %s Authorization scheme %q, key starts %q (a fake key)", r.Method, r.URL.Path, scheme, key[:min(len(key), 6)])
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
// "No API provider registered for api: <api>" when nothing is registered (provider-composer.ts:605-606). The registry is a Map
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
// provider refuses, because the lookup happens when the request starts (provider-composer.ts:605 inside lazyStream).
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

// A model that no catalog, models.json or extension lists is no model: Pi's registry returns nothing for its provider and id (model-runtime.ts:454-455 getModel over ai/src/models.ts:498 getModel, which
// answers undefined; rpc-mode.ts:492-495 set_model answers "Model not found: provider/id"), so BuildModel and buildModel refuse it at the build with that text and no model, and nothing is built that
// could send. (Stale before: these two entries built a bare object and refused it at the stream; BuildModel has refused at the build since a1af4d7dd.)
func TestBareModelIsRefusedAtTheBuild(t *testing.T) {
	services := newRuntimeTestServices(t)
	for name, build := range map[string]func() (*ai.Model, error){
		"BuildModel":              func() (*ai.Model, error) { return BuildModel("bare-provider-nobody-lists/x", services) },
		"buildModel-explicit-key": func() (*ai.Model, error) { return buildModel("bare-provider-nobody-lists/x", services, "k") },
	} {
		model, err := build()
		if want := "Model not found: bare-provider-nobody-lists/x"; err == nil || err.Error() != want || model != nil {
			t.Errorf("%s: model %+v, error %v; want no model and %q", name, model, err, want)
		}
	}
}

// A model OBJECT with no api that an extension or an embedder hands over reaches a request without passing the build by id (buildRequestModel). Pi takes such an object to the stream and refuses it there,
// resolveApiProvider throwing the api undefined (compat.ts:244-247): the refusal is still at the request, and the endpoint the object names is never contacted.
func TestBareModelIsRefusedAtItsRequest(t *testing.T) {
	services := newRuntimeTestServices(t)
	baseURL, requests := unknownAPIListener(t)
	object := &ai.Model{ID: "x", ProviderMeta: ai.ProviderMetadata{ProviderID: "bare-provider-nobody-lists", BaseURL: baseURL}}
	model, err := buildRequestModel(object, services, "k")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Provider.Close() })
	err = unknownAPIStream(t, model.Provider)
	if want := "No API provider registered for api: undefined"; err.Error() != want {
		t.Errorf("model object: error = %q, want %q", err, want)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("requests at the endpoint = %d, want 0", got)
	}
}

// Pi routes a model to its provider own stream only for an api that provider lists (compat.ts:236-239); a Copilot model whose api is none of the apis the Copilot catalog lists, or has none, is refused
// at its request and no connection is made to the endpoint. The Copilot provider sent every such name as chat completions with the Copilot token before.
func TestCopilotModelWithNoCopilotAPIIsRefused(t *testing.T) {
	for _, api := range []string{"", "nosuchapi", "constructor"} {
		shown := api
		if api == "" {
			shown = "undefined"
		}
		t.Run("api="+api, func(t *testing.T) {
			baseURL, requests := unknownAPIListener(t)
			provider, err := buildProviderForEntry("github-copilot", "m", ai.API(api), icodingagent.ModelEntry{
				ProviderID: "github-copilot", ModelID: "m", BaseURL: baseURL, APIKey: "copilot-fake-token", API: api, Input: []string{"text"},
			}, newRuntimeTestServices(t), "", false, false)
			if err != nil {
				t.Fatalf("building the provider failed (Pi refuses at the request): %v", err)
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
