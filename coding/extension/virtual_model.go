package extension

// Ports packages/coding-agent/src/core/virtual-models.ts (the routing types).
// Ports packages/coding-agent/src/core/extensions/types.ts (ExtensionVirtualModel).

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
)

// VirtualModelAPI is the API id of virtual catalog entries. Requests for it fail unless routed first.
//
// upstream: virtual-models.ts:29 (VIRTUAL_MODEL_API)
const VirtualModelAPI = "pi-virtual"

// VirtualModelStateEntry is the custom entry type that stores router state on the session branch.
//
// upstream: virtual-models.ts:32 (VIRTUAL_MODEL_STATE_ENTRY)
const VirtualModelStateEntry = "pi.virtual-model-state"

// VirtualModelStateData is the data of a `pi.virtual-model-state` custom entry. State is the router's JSON state.
//
// upstream: virtual-models.ts:35-39 (VirtualModelStateData)
type VirtualModelStateData struct {
	Provider string          `json:"provider"`
	ModelID  string          `json:"modelId"`
	State    json.RawMessage `json:"state"`
}

// ModelRouteReason says why a request is being routed.
//
//   - user: first request after a message the user wrote (prompt, steering, or follow-up)
//   - continuation: any other request in the agent loop, e.g. after tool results or extension messages
//   - retry: automatic retry after a failed request, including after compaction for a context overflow
//   - direct: a request outside the agent loop, e.g. a compaction summary or an extension call
//
// upstream: virtual-models.ts:53 (ModelRouteReason)
type ModelRouteReason string

// The reasons of upstream's ModelRouteReason union.
const (
	ModelRouteReasonUser         ModelRouteReason = "user"
	ModelRouteReasonContinuation ModelRouteReason = "continuation"
	ModelRouteReasonRetry        ModelRouteReason = "retry"
	ModelRouteReasonDirect       ModelRouteReason = "direct"
)

// ModelRoutePrevious is the physical model and thinking level of the latest successful response in a request's messages.
type ModelRoutePrevious struct {
	Model         *ai.Model
	ThinkingLevel ai.ModelThinkingLevel
}

// ModelRouteFailed is the failed request of a `retry`, which the request's messages no longer contain. Message carries its stop reason and error message.
type ModelRouteFailed struct {
	Model         *ai.Model
	ThinkingLevel ai.ModelThinkingLevel
	Message       ai.AssistantMessage
}

// ModelRouteRequest is what a router sees for one request.
//
// Go mechanic (not a divergence): upstream's `signal` is the context.Context passed to Route, and the TState of `state` is a JSON value (an absent state is nil).
//
// upstream: virtual-models.ts:55-72 (ModelRouteRequest)
type ModelRouteRequest struct {
	// Model is the selected virtual model.
	Model *ai.Model
	// ThinkingLevel is the selected thinking level. Its meaning is up to the router.
	ThinkingLevel ai.ModelThinkingLevel
	Reason        ModelRouteReason
	// Previous is the physical model and thinking level of the latest successful response in Messages.
	Previous *ModelRoutePrevious
	// Failed is the failed request of a retry. Absent when the router itself failed.
	Failed *ModelRouteFailed
	// State is the router state last returned on this session branch. Absent before the first state and for `direct` requests.
	State json.RawMessage
	// Messages is the conversation for this request, including system messages.
	Messages []ai.Message
}

// ModelRoute is the physical model and thinking level for one request.
//
// upstream: virtual-models.ts:75-85 (ModelRoute)
type ModelRoute struct {
	Model         *ai.Model
	ThinkingLevel ai.ModelThinkingLevel
	// State is the new router state, stored on the session branch unless it is the request's own state. Return the request's state or nil to keep the current state. Must be JSON. Ignored for `direct` requests.
	State json.RawMessage
}

// ModelRouteFunc picks the physical model, which must have credentials, and thinking level for one request. ctx carries the request's cancellation and, for an extension's virtual model, the extension [Context] ([FromContext]).
//
// upstream: virtual-models.ts:100 (VirtualModelDefinition.route), types.ts:1866 (ExtensionVirtualModel.route)
type ModelRouteFunc = func(ctx context.Context, request ModelRouteRequest) (ModelRoute, error)

// VirtualModelDefinition is a selectable catalog entry that routes each request to a physical model.
//
// upstream: virtual-models.ts:87-101 (VirtualModelDefinition)
type VirtualModelDefinition struct {
	// Provider is the provider the virtual model is listed under. May be a provider with physical models.
	Provider string
	// ID must not be the id of a physical model of Provider.
	ID   string
	Name string
	// ThinkingLevels are the thinking levels offered for selection. Defaults to `["off"]`.
	ThinkingLevels []ai.ModelThinkingLevel
	// ContextWindow and MaxTokens are the limits shown before the first response. Afterwards, the limits of the physical model that answered apply. Unset limits are unknown (0).
	ContextWindow int
	MaxTokens     int
	// Input are the input types accepted for selection. Defaults to text and images; routed models without image support get placeholders.
	Input []string
	Route ModelRouteFunc
}

// ExtensionVirtualModel is a virtual model registered through [API.RegisterVirtualModel]. Its Route runs with the extension [Context] in ctx.
//
// upstream: types.ts:1864-1867 (ExtensionVirtualModel)
type ExtensionVirtualModel = VirtualModelDefinition
