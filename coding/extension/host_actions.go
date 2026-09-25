package extension

// ExtensionActions is the host-side injection of agent-loop callbacks
// available to extensions via `ctx.actions.*` (the runtime side; this
// struct is consumed by [host.Runner.BindCore]). Mirrors upstream
// `ExtensionActions` (types.ts:1588-1603).
//
// Most fields are opaque-typed (`any`) at this boundary because their
// concrete handler types live in upstream files not yet ported
// (session-manager, agent-runtime, tool-registry). Field names and upstream
// line cites are fixed here so a concrete handler type can replace `any`
// without renaming.
//
// upstream: types.ts:1589-1603
type ExtensionActions struct {
	// upstream: types.ts:1590: SendMessageHandler
	SendMessage any
	// upstream: types.ts:1591: SendUserMessageHandler
	SendUserMessage SendUserMessageHandler
	// upstream: types.ts:1591: AppendEntryHandler
	AppendEntry any
	// upstream: types.ts:1592: SetSessionNameHandler
	SetSessionName any
	// upstream: types.ts:1593: GetSessionNameHandler
	GetSessionName any
	// upstream: types.ts:1594: SetLabelHandler
	SetLabel any
	// upstream: types.ts:1595: GetActiveToolsHandler
	GetActiveTools any
	// upstream: types.ts:1596: GetAllToolsHandler
	GetAllTools any
	// upstream: types.ts:1597: SetActiveToolsHandler
	SetActiveTools any
	// upstream: types.ts:1598: RefreshToolsHandler
	RefreshTools any
	// upstream: types.ts:1599: GetCommandsHandler
	GetCommands any
	// upstream: types.ts:1600: SetModelHandler
	SetModel any
	// upstream: types.ts:1601: GetThinkingLevelHandler
	GetThinkingLevel any
	// upstream: types.ts:1602: SetThinkingLevelHandler
	SetThinkingLevel any
}

// SendUserMessageHandler injects a user message into the agent loop.
// content is upstream's `string | (TextContent | ImageContent)[]` union;
// current in-process callers use strings, while unsupported content shapes
// should fail at the host boundary.
//
// upstream: types.ts:1525-1528
// Go returns error so hosts can surface invalid content/delivery modes loudly
// instead of dropping extension calls on the floor.
type SendUserMessageHandler func(content any, options *SendUserMessageOptions) error

// ProviderActions is the optional injection of provider-registration
// callbacks. Mirrors upstream's inline structural type at
// runner.ts:264 (third argument to bindCore).
//
// Fields are pointers-to-func so absence is `nil` (matches upstream's
// optional `?` modifier on each field).
//
// upstream: runner.ts:264 (`providerActions?: { registerProvider?, unregisterProvider? }`)
type ProviderActions struct {
	RegisterProvider   func(name string, config ProviderConfig)
	UnregisterProvider func(name string)
}
