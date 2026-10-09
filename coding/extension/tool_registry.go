package extension

// Ports packages/coding-agent/src/core/extensions/types.ts.

import (
	"maps"
	"slices"
	"sync"
)

type toolRegistry struct {
	mu    sync.RWMutex
	tools map[string]RegisteredTool
	order []string
}

// InitializeToolRegistry enables synchronized runtime registration before an Extension is shared with runners.
func (e *Extension) InitializeToolRegistry() {
	if e.toolState == nil {
		e.toolState = &toolRegistry{tools: maps.Clone(e.Tools), order: slices.Clone(e.ToolOrder)}
	}
}

// SetRegisteredTool replaces a definition in place or appends a new name in registration order.
func (e *Extension) SetRegisteredTool(tool RegisteredTool) {
	e.InitializeToolRegistry()
	state := e.toolState
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.tools == nil {
		state.tools = make(map[string]RegisteredTool)
	}
	name := tool.Definition.Name
	if _, exists := state.tools[name]; !exists {
		state.order = append(state.order, name)
	}
	state.tools[name] = tool
}

// ReplaceRegisteredTools replaces the shared registry when the owning runtime restarts.
func (e *Extension) ReplaceRegisteredTools(source *Extension) {
	tools := source.RegisteredTools()
	e.InitializeToolRegistry()
	state := e.toolState
	state.mu.Lock()
	defer state.mu.Unlock()
	state.tools = make(map[string]RegisteredTool, len(tools))
	state.order = make([]string, 0, len(tools))
	for _, tool := range tools {
		name := tool.Definition.Name
		state.tools[name] = tool
		state.order = append(state.order, name)
	}
}

// RegisteredTool looks up the current definition without copying the registry.
func (e *Extension) RegisteredTool(name string) (RegisteredTool, bool) {
	if state := e.toolState; state != nil {
		state.mu.RLock()
		defer state.mu.RUnlock()
		tool, ok := state.tools[name]
		return tool, ok
	}
	tool, ok := e.Tools[name]
	return tool, ok
}

// RegisteredToolNames are the keys of the extension's tools (upstream `extension.tools.keys()`): registration order, then any name
// the order does not list, sorted. It includes tools registered after load.
func (e *Extension) RegisteredToolNames() []string {
	names, _ := e.registeredTools()
	return names
}

// RegisteredTools returns an ordered snapshot, including tools registered after load.
func (e *Extension) RegisteredTools() []RegisteredTool {
	names, tools := e.registeredTools()
	out := make([]RegisteredTool, 0, len(names))
	for _, name := range names {
		out = append(out, tools[name])
	}
	return out
}

func (e *Extension) registeredTools() ([]string, map[string]RegisteredTool) {
	tools, order := e.Tools, e.ToolOrder
	if state := e.toolState; state != nil {
		state.mu.RLock()
		defer state.mu.RUnlock()
		tools, order = state.tools, state.order
	}
	names := make([]string, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, name := range order {
		if _, ok := tools[name]; !ok {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(seen) != len(tools) {
		for _, name := range slices.Sorted(maps.Keys(tools)) {
			if _, exists := seen[name]; !exists {
				names = append(names, name)
			}
		}
	}
	// The caller reads tools after the lock is released, so hand back a copy.
	snapshot := make(map[string]RegisteredTool, len(tools))
	maps.Copy(snapshot, tools)
	return names, snapshot
}
