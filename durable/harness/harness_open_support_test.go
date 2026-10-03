package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

type openHarnessOptions struct {
	registry Registry
	onReport func(error)
}

// openHarness opens a Harness with a fresh registry holding the named tools (harness-support.ts:33-48).
func openHarness(t *testing.T, storage durable.Storage, toolNames []string, options ...openHarnessOptions) (Harness, Registry) {
	t.Helper()
	var option openHarnessOptions
	if len(options) > 0 {
		option = options[0]
	}
	registry := option.registry
	if registry == nil {
		registry = CreateRegistry()
	}
	if len(toolNames) > 0 {
		tools := []*durable.ToolRegistration{}
		for _, name := range toolNames {
			tools = append(tools, supportTool(name))
		}
		mustInstall(t, registry, new(durable.Extension{Name: "tools", Tools: tools}))
	}
	harness, err := OpenHarness(testContext, storage, HarnessOptions{Models: ai.CreateModels(), Registry: registry, OnReport: option.onReport})
	if err != nil {
		t.Fatal(err)
	}
	return harness, registry
}
