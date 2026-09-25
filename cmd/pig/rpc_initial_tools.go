package main

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding"
)

// rpcSetInitialActiveTools selects the startup loadout without shrinking the
// Session registry used by subsequent extension and Piglet tool selection.
func rpcSetInitialActiveTools(session *coding.Session, names []string) {
	registered := session.Tools()
	byName := make(map[string]agent.AgentTool, len(registered))
	for _, tool := range registered {
		byName[tool.Name()] = tool
	}
	selected := make([]agent.AgentTool, 0, len(names))
	for _, name := range names {
		if tool := byName[name]; tool != nil {
			selected = append(selected, tool)
			delete(byName, name)
		}
	}
	session.Agent().SetTools(selected)
}
