package extension

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
)

// ExtensionActions is the host-side injection of agent-loop callbacks
// available to extensions via `ctx.actions.*` (the runtime side; this
// struct is consumed by [host.Runner.BindCore]). Mirrors upstream
// `ExtensionActions` (types.ts:2147-2163). Each field has the shape of the
// upstream handler type named beside it; Go handlers return the error
// upstream's handler would throw.
//
// upstream: types.ts:2147-2163
type ExtensionActions struct {
	// upstream: types.ts:2064: SendMessageHandler
	SendMessage func(message CustomMessageRef, options *SendMessageOptions) error
	// upstream: types.ts:2069: SendUserMessageHandler
	SendUserMessage SendUserMessageHandler
	// upstream: types.ts:2074: AppendEntryHandler
	AppendEntry func(customType string, data any) error
	// upstream: types.ts:2076: SetSessionNameHandler
	SetSessionName func(name string) error
	// upstream: types.ts:2078: GetSessionNameHandler
	GetSessionName func() string
	// upstream: types.ts:2106: SetLabelHandler
	SetLabel func(entryID string, label *string) error
	// upstream: types.ts:2080: GetActiveToolsHandler
	GetActiveTools func() []string
	// upstream: types.ts:2090: GetAllToolsHandler
	GetAllTools func() []ToolInfo
	// upstream: types.ts:2092: GetSettingsHandler
	GetSettings func() Settings
	// upstream: types.ts:2096: SetActiveToolsHandler
	SetActiveTools func(toolNames []string)
	// upstream: types.ts:2098: RefreshToolsHandler
	RefreshTools func() error
	// upstream: types.ts:2094: GetCommandsHandler
	GetCommands func() []SlashCommandInfo
	// upstream: types.ts:2100: SetModelHandler. ctx carries the calling extension's call lifetime (D3).
	SetModel func(ctx context.Context, model Model) (bool, error)
	// upstream: types.ts:2102: GetThinkingLevelHandler
	GetThinkingLevel func() ThinkingLevel
	// upstream: types.ts:2104: SetThinkingLevelHandler
	SetThinkingLevel func(level ThinkingLevel)
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

// ProviderActions supplies synchronous provider registration callbacks. Registration errors are reported per queued entry during binding and returned directly for post-bind calls.
// upstream: packages/coding-agent/src/core/extensions/runner.ts:bindCore
type ProviderActions struct {
	RegisterProvider   func(name string, config ProviderConfig) error
	UnregisterProvider func(name string)
	// RegisterNativeProvider applies a native provider registration of Pi's Provider object; the call's context carries the registering
	// call's initiation. A carrier registered by a subprocess extension reaches it as the object assembled from the carrier.
	// upstream: runner.ts:410-418, 481-490 (providerActions.registerNativeProvider(provider: Provider))
	RegisterNativeProvider func(ctx context.Context, provider *ai.ModelsProvider) error
	// RegisterNativeProviderCarrier is RegisterNativeProvider for the registration carrier, which keeps each member's context.Context and
	// error; a registry that serves subprocess extensions binds it. When both are set, the carrier form applies.
	RegisterNativeProviderCarrier func(ctx context.Context, provider *NativeProvider) error
	// RegisterVirtualModel and UnregisterVirtualModel apply virtual-model registrations; the model registry's own ones apply when unset.
	// upstream: runner.ts:413-417 (providerActions.registerVirtualModel, unregisterVirtualModel)
	RegisterVirtualModel   func(definition VirtualModelDefinition) error
	UnregisterVirtualModel func(provider, id string)
}

// bindsNativeProvider reports whether either native provider registration form is bound.
func (a ProviderActions) bindsNativeProvider() bool {
	return a.RegisterNativeProvider != nil || a.RegisterNativeProviderCarrier != nil
}

// registerNative applies one native provider registration: the carrier form when set, else the Provider-object form with the registered
// object itself.
func (a ProviderActions) registerNative(ctx context.Context, provider *ai.ModelsProvider, carrier *NativeProvider) error {
	if a.RegisterNativeProviderCarrier != nil {
		return a.RegisterNativeProviderCarrier(ctx, carrier)
	}
	return a.RegisterNativeProvider(ctx, provider)
}
