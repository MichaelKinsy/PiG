package extensionconformance

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// providerServicesRegistryAPI is the Go reference for extension.API.RegisterProvider and UnregisterProvider: the production model registry that every mode's host
// callbacks write to (cmd/pig bindProviderRegistry).
type providerServicesRegistryAPI struct {
	extension.API
	services *coding.AgentSessionServices
}

func (a providerServicesRegistryAPI) RegisterProvider(name string, config extension.ProviderConfig) {
	_ = a.services.Registry().RegisterExtensionProvider(name, config)
}

func (a providerServicesRegistryAPI) UnregisterProvider(name string) {
	a.services.Registry().UnregisterProvider(name)
}

// TestConformance_ExtensionAPIRegisterProvider pins Pi's pi.registerProvider and pi.unregisterProvider (packages/coding-agent/src/core/extensions/types.ts:1596-1631,
// model-registry.ts registerProvider). Every SDK registers a provider with one model after it connected and unregisters it again; the host applies each
// call to a real model registry through extension.API, which then lists the model with the base URL the SDK gave, and no longer lists it afterwards.
//
// The model's window, output limit, prices, input kinds and reasoning flag are values no SDK and no host default has (probeModelLimits), so an SDK
// that drops one on the way, or a host that fills in its own, shows: the registry holds the registered values and the request the host builds
// for the model asks for the registered output limit, the same from every SDK, isolated, packed and fused.
func TestConformance_ExtensionAPIRegisterProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.host == nil {
				t.Skip("no subprocess host for " + tc.name)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			var api extension.API = providerServicesRegistryAPI{services: services}
			h.host.SetProviderCallbacks(func(name string, config extension.ProviderConfig) error {
				api.RegisterProvider(name, config)
				return nil
			}, func(name string) { api.UnregisterProvider(name) })

			runConformanceCommand(t, h, "provider_probe_register")
			pollUntilConformance(t, "the registry never held the provider the SDK registered", func() bool {
				model := services.Registry().Find("conformance-probe", "probe-model")
				return model != nil && model.ProviderMeta.BaseURL == "https://probe.invalid/v1"
			})
			requireProbeModelLimits(t, services)
			runConformanceCommand(t, h, "provider_probe_unregister")
			pollUntilConformance(t, "the registry still held the provider the SDK unregistered", func() bool {
				return services.Registry().Find("conformance-probe", "probe-model") == nil
			})
		})
	}
}

// probeModelLimits are the members provider_probe_register gives its model in every SDK fixture. None is a value an SDK or the host fills in for a
// model that states none: 128000 for the window, 4096 or 16384 for the output limit, zero prices, text input, no reasoning.
const probeModelLimits = `{"contextWindow":54321,"cost":{"cacheRead":0.35,"cacheWrite":0.45,"input":3.5,"output":4.5},"input":["text","image"],"maxTokens":4321,"reasoning":true}`

// requireProbeModelLimits asserts what the host holds and sends for the model an SDK registered: the registry's model has the registered window,
// output limit, prices, input kinds and reasoning flag, and the request the host builds for it asks for the registered output limit. The request is
// read at the payload hook and ended there, so nothing is sent to the fixture's base URL.
func requireProbeModelLimits(t *testing.T, services *coding.AgentSessionServices) {
	t.Helper()
	raw, err := extension.ModelObject(services.Registry().Find("conformance-probe", "probe-model"))
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	held, err := json.Marshal(map[string]any{"contextWindow": object["contextWindow"], "maxTokens": object["maxTokens"], "cost": object["cost"],
		"input": object["input"], "reasoning": object["reasoning"]})
	if err != nil {
		t.Fatal(err)
	}
	if string(held) != probeModelLimits {
		t.Fatalf("the registry's model of conformance-probe/probe-model:\n got %s\nwant %s", held, probeModelLimits)
	}

	runtime := services.ModelRuntime()
	if refreshed := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted {
		t.Fatalf("refresh=%v", refreshed)
	}
	model := runtime.GetModel("conformance-probe", "probe-model")
	if model == nil {
		t.Fatal("the model runtime lists no conformance-probe/probe-model")
	}
	captured := errors.New("request captured")
	var sent map[string]any
	message := runtime.CompleteSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{
		OnPayload: func(payload any, _ *ai.Model) (any, error) {
			body, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(body, &sent); err != nil {
				return nil, err
			}
			return nil, captured
		},
	})
	if sent == nil {
		t.Fatalf("the host built no request for conformance-probe/probe-model: %+v", message)
	}
	if sent["model"] != "probe-model" || sent["max_completion_tokens"] != float64(4321) {
		t.Fatalf("request of conformance-probe/probe-model: model %v, max_completion_tokens %v, max_tokens %v; want probe-model, 4321, absent", sent["model"], sent["max_completion_tokens"], sent["max_tokens"])
	}
}
