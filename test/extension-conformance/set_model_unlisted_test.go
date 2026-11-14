package extensionconformance

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The string form of an SDK's set-model (PiG's surface; Pi's pi.setModel takes an object) names a model of the registry. For an id no catalog lists there is no model, as there is none for Pi's RPC set_model
// ('Model not found: p/id', rpc-mode.ts:495): the SDK answers that error, the session keeps its model, and no request model with no api or endpoint is made for a provider that has credentials.
func TestSetModelNamedRefusesAnUnlistedIDAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Setenv("ANTHROPIC_API_KEY", "conformance-key")
	t.Setenv("OPENAI_API_KEY", "conformance-key")
	for _, sdk := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if strings.Contains(sdk.name, "node") {
			continue // the Node runtime has no string form: Pi's pi.setModel takes the object
		}
		t.Run(sdk.name, func(t *testing.T) {
			h := sdk.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Skip("no host bridge for " + sdk.name)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			start := ai.GetBuiltinModel("anthropic", "claude-sonnet-4-5")
			session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true, Model: start})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			coding.BindSessionExtensionActions(nil, h.bridge, func() *coding.Session { return session }, extension.ContextActions{}, nil)
			command, ok := findCommand(h.runner, "set_model")
			if !ok {
				t.Fatal("the fixture has no set_model command")
			}
			for _, spec := range []string{"anthropic/claude-nonesuch-9", "openai/not-listed-anywhere"} {
				err := command.Handler(context.Background(), spec)
				if err == nil || !strings.Contains(err.Error(), "Model not found: "+spec) {
					t.Errorf("set_model %s: error %v, want 'Model not found: %s'", spec, err, spec)
				}
				if m := session.Model(); m.ID != "claude-sonnet-4-5" || m.ProviderMeta.API == "" {
					t.Errorf("after set_model %s the session model is %s/%s api %q; it must keep claude-sonnet-4-5", spec, m.ProviderMeta.ProviderID, m.ID, m.ProviderMeta.API)
				}
			}
		})
	}
}
