package coding

import (
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestSessionCacheWasteUsesRegisteredModelPrice(t *testing.T) {
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	if err := services.Registry().RegisterProvider("test", extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "https://example.invalid", APIKey: "test", Models: []extension.ProviderModelConfig{{ID: "test-model", Name: "Test", Cost: extension.ProviderModelCost{CacheRead: .3}}}}); err != nil {
		t.Error(err)
	}
	session, err := NewSession(services, SessionOptions{Model: &ai.Model{ID: "faux-1", Provider: &scriptedProvider{}}, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	for _, usage := range []ai.Usage{{CacheWrite: 100000, Cost: ai.UsageCost{CacheWrite: .375}}, {CacheRead: 100000, CacheWrite: 5000, Cost: ai.UsageCost{CacheRead: .03, CacheWrite: .019}}, {CacheWrite: 110000, Cost: ai.UsageCost{CacheWrite: .4125}}} {
		if _, err := session.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Provider: "test", ModelID: "test-model", Usage: &usage}}); err != nil {
			t.Fatal(err)
		}
	}
	waste := session.Inner().Accounting().CacheWaste
	if waste.MissedTokens != 105000 || math.Abs(waste.MissedCost-.36225) >= .000005 {
		t.Fatalf("cache waste = %+v", waste)
	}
}
