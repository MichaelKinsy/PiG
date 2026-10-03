package extension

// Ports packages/coding-agent/src/core/extensions/types.ts (ToolLoadout, ToolLoadoutChanges).

// ToolLoadout is the tools of a session as [ToolDefinition.PrepareLoadout] sees them.
//
// Go mechanic (not a divergence): upstream's getExposure and getNamespace methods are function-valued fields, so a test double is a struct literal.
//
// upstream: types.ts:541-551 (ToolLoadout)
type ToolLoadout struct {
	// Declared are the tools declared to the model (the active tools), in order, with their original descriptions.
	Declared []AgentTool
	// Callable are the tools callable through [ToolContext.ExecuteTool].
	Callable []AgentTool
	// Registered is every registered tool.
	Registered   []AgentTool
	GetExposure  func(name string) ToolExposure
	GetNamespace func(name string) *ToolNamespace
}

// ToolLoadoutChanges are the changes [ToolDefinition.PrepareLoadout] makes to what the model sees.
//
// upstream: types.ts:554-563 (ToolLoadoutChanges)
type ToolLoadoutChanges struct {
	// Descriptions are the model-facing descriptions of declared tools, by tool name.
	Descriptions map[string]string `json:"descriptions,omitempty"`
	// HiddenDeclarations names declared tools whose declarations requests leave out. They stay active and callable, and the transcript still declares them, so the active set survives `/tree` and resume.
	HiddenDeclarations []string `json:"hiddenDeclarations,omitempty"`
}

// ToolPrepareLoadoutFunc mirrors upstream ToolDefinition.prepareLoadout. A nil result leaves the loadout as it is (upstream returns undefined). Upstream's hook is synchronous and reports a failure by throwing; a Go hook panics instead, and the session recovers the panic and reports it as a `prepare_loadout` extension error (agent-session.ts:1520-1533).
type ToolPrepareLoadoutFunc = func(loadout ToolLoadout) *ToolLoadoutChanges
