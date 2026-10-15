package main

import (
	"encoding/json"
	"os"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func main() {
	dir, err := os.MkdirTemp("", "cache-stats-")
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			panic(err)
		}
	}()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		panic(err)
	}
	if err := services.Registry().RegisterExtensionProvider("test", extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "https://example.invalid", Models: []extension.ProviderModelConfig{{ID: "test-model", Name: "Test", Cost: extension.ProviderModelCost{CacheRead: .3}}}}); err != nil {
		panic(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: &ai.Model{ID: "faux-1", Provider: ai.NewFauxProvider(ai.FauxConfig{})}, SkipBuiltinTools: true})
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			panic(err)
		}
	}()
	for i, usage := range []ai.Usage{{CacheWrite: 100000, Output: 10, Cost: ai.UsageCost{CacheWrite: .375}}, {CacheRead: 100000, CacheWrite: 5000, Output: 10, Cost: ai.UsageCost{CacheRead: .03, CacheWrite: .019}}, {CacheWrite: 110000, Output: 10, Cost: ai.UsageCost{CacheWrite: .4125}}} {
		if _, err := session.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", API: ai.APIAnthropicMessages, Provider: "test", ModelID: "test-model", Usage: &usage, Timestamp: int64(i) * 60000}}); err != nil {
			panic(err)
		}
	}
	waste := session.Inner().Accounting().CacheWaste
	if err := json.NewEncoder(os.Stdout).Encode([]any{waste.MissedTokens, waste.MissedCost, waste.MissCount}); err != nil {
		panic(err)
	}
}
