package extensionconformance

import (
	"testing"
	"time"

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
func TestConformance_ExtensionAPIRegisterProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range sdkHarnessCases() {
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
			pollUntilConformance(t, 5*time.Second, "the registry never held the provider the SDK registered", func() bool {
				model := services.Registry().Find("conformance-probe", "probe-model")
				return model != nil && model.ProviderMeta.BaseURL == "https://probe.invalid/v1"
			})
			runConformanceCommand(t, h, "provider_probe_unregister")
			pollUntilConformance(t, 5*time.Second, "the registry still held the provider the SDK unregistered", func() bool {
				return services.Registry().Find("conformance-probe", "probe-model") == nil
			})
		})
	}
}
