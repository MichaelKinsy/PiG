package vacation

// Ports packages/coding-agent/src/experimental/vacation/harness-setup.ts, packages/coding-agent/src/experimental/vacation/runtime.ts and packages/coding-agent/src/experimental/vacation/sessions.ts
//
// harness-setup.ts repeats the HTTP setup, the Harness settings and the initial model of the coding agent's; those are durableagent's. runtime.ts and sessions.ts are the coding agent's with the differences of Profile.

import (
	"context"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableagent"
)

// CreateVacationRegistry is a registry with the vacation planner and its research subagent's search. No coding tools, no pi prompt.
func CreateVacationRegistry() (harness.Registry, error) {
	registry := harness.CreateRegistry()
	if err := registry.Install(Vacation); err != nil {
		return nil, err
	}
	return registry, registry.Install(Search)
}

// Profile is the vacation planner: its own sessions, its registry, no execution environments, and a root conversation that has only the vacation planner; the research subagent selects search itself.
var Profile = durableagent.Profile{
	Kind: "vacation",
	Registry: func(*codingagent.SettingsManager, string) (harness.Registry, error) {
		return CreateVacationRegistry()
	},
	Environments: false,
	RootAgent: func(string) harness.AgentChange {
		return harness.AgentChange{Extensions: harness.SetTo(harness.ExtensionChange{Exact: true, List: []*durable.Extension{Vacation}})}
	},
}

// Open opens a vacation planner session.
func Open(ctx context.Context, options durableagent.OpenDurableOptions) (*durableagent.OpenDurableResult, error) {
	return durableagent.Open(ctx, options, Profile)
}
