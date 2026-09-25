package parity

import (
	"reflect"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// eventTypeRegistry maps upstream event type names (as they appear in
// `.upstream/current/.../types.ts` `export interface XxxEvent {…}` and
// `XxxEventResult {…}` declarations) to the corresponding Go reflect.Type.
//
// Used by:
//
//   - TestEventTypes_AllUpstreamEventTypesExistInGo
//   - TestEventResults_AllUpstreamEventResultsExistInGo
//   - TestEventFields_AllUpstreamFieldsHaveGoCounterpart
//   - TestJSONTags_AllEventFieldsAreCamelCase
//   - TestJSONTags_NoExtraGoFieldsBeyondUpstream
//   - TestEventFields_NoLegacyInitialismCasing (drift-prevention)
//
// Entries are hand-maintained. When a new event lands upstream (caught by
// Test*_AllUpstream*), add it here with the same camelCase upstream name as
// key and its Go reflect.Type as value.
//
// Type aliases (e.g. `BeforeProviderRequestEventResult = any`) are stored
// via `(*T)(nil)` indirection so the registry compiles. Tests that walk
// fields must guard on `t.Kind() == reflect.Struct` because alias-to-any
// has Kind == reflect.Interface with no fields to check.
var eventTypeRegistry = map[string]reflect.Type{
	// ─── *Event types (42) ──────────────────────────────────────────────
	"ProjectTrustEvent":          reflect.TypeFor[extension.ProjectTrustEvent](),
	"ResourcesDiscoverEvent":     reflect.TypeFor[extension.ResourcesDiscoverEvent](),
	"SessionStartEvent":          reflect.TypeFor[extension.SessionStartEvent](),
	"SessionInfoChangedEvent":    reflect.TypeFor[extension.SessionInfoChangedEvent](),
	"SessionBeforeSwitchEvent":   reflect.TypeFor[extension.SessionBeforeSwitchEvent](),
	"SessionBeforeForkEvent":     reflect.TypeFor[extension.SessionBeforeForkEvent](),
	"SessionBeforeCompactEvent":  reflect.TypeFor[extension.SessionBeforeCompactEvent](),
	"SessionCompactEvent":        reflect.TypeFor[extension.SessionCompactEvent](),
	"SessionCompactFailedEvent":  reflect.TypeFor[extension.SessionCompactFailedEvent](),
	"SessionShutdownEvent":       reflect.TypeFor[extension.SessionShutdownEvent](),
	"SessionBeforeTreeEvent":     reflect.TypeFor[extension.SessionBeforeTreeEvent](),
	"SessionTreeEvent":           reflect.TypeFor[extension.SessionTreeEvent](),
	"ContextEvent":               reflect.TypeFor[extension.ContextEvent](),
	"ContextWithSystemEvent":     reflect.TypeFor[extension.ContextWithSystemEvent](),
	"BeforeProviderRequestEvent": reflect.TypeFor[extension.BeforeProviderRequestEvent](),
	"AfterProviderResponseEvent": reflect.TypeFor[extension.AfterProviderResponseEvent](),
	"BeforeProviderHeadersEvent": reflect.TypeFor[extension.BeforeProviderHeadersEvent](),
	"BeforeAgentStartEvent":      reflect.TypeFor[extension.BeforeAgentStartEvent](),
	"AgentStartEvent":            reflect.TypeFor[extension.AgentStartEvent](),
	"AgentEndEvent":              reflect.TypeFor[extension.AgentEndEvent](),
	"AgentBeforeSettleEvent":     reflect.TypeFor[extension.AgentBeforeSettleEvent](),
	"AgentSettledEvent":          reflect.TypeFor[extension.AgentSettledEvent](),
	"UIPromptStartEvent":         reflect.TypeFor[extension.UIPromptStartEvent](),
	"UIPromptEndEvent":           reflect.TypeFor[extension.UIPromptEndEvent](),
	"TurnStartEvent":             reflect.TypeFor[extension.TurnStartEvent](),
	"TurnEndEvent":               reflect.TypeFor[extension.TurnEndEvent](),
	"MessageStartEvent":          reflect.TypeFor[extension.MessageStartEvent](),
	"MessageUpdateEvent":         reflect.TypeFor[extension.MessageUpdateEvent](),
	"MessageEndEvent":            reflect.TypeFor[extension.MessageEndEvent](),
	"ToolExecutionStartEvent":    reflect.TypeFor[extension.ToolExecutionStartEvent](),
	"ToolExecutionUpdateEvent":   reflect.TypeFor[extension.ToolExecutionUpdateEvent](),
	"ToolExecutionEndEvent":      reflect.TypeFor[extension.ToolExecutionEndEvent](),
	"ModelSelectEvent":           reflect.TypeFor[extension.ModelSelectEvent](),
	"ThinkingLevelSelectEvent":   reflect.TypeFor[extension.ThinkingLevelSelectEvent](),
	"UserBashEvent":              reflect.TypeFor[extension.UserBashEvent](),
	"InputEvent":                 reflect.TypeFor[extension.InputEvent](),
	"BashToolCallEvent":          reflect.TypeFor[extension.BashToolCallEvent](),
	"PowerShellToolCallEvent":    reflect.TypeFor[extension.PowerShellToolCallEvent](),
	"ReadToolCallEvent":          reflect.TypeFor[extension.ReadToolCallEvent](),
	"EditToolCallEvent":          reflect.TypeFor[extension.EditToolCallEvent](),
	"WriteToolCallEvent":         reflect.TypeFor[extension.WriteToolCallEvent](),
	"GrepToolCallEvent":          reflect.TypeFor[extension.GrepToolCallEvent](),
	"FindToolCallEvent":          reflect.TypeFor[extension.FindToolCallEvent](),
	"LsToolCallEvent":            reflect.TypeFor[extension.LsToolCallEvent](),
	"CustomToolCallEvent":        reflect.TypeFor[extension.CustomToolCallEvent](),
	"BashToolResultEvent":        reflect.TypeFor[extension.BashToolResultEvent](),
	"PowerShellToolResultEvent":  reflect.TypeFor[extension.PowerShellToolResultEvent](),
	"ReadToolResultEvent":        reflect.TypeFor[extension.ReadToolResultEvent](),
	"EditToolResultEvent":        reflect.TypeFor[extension.EditToolResultEvent](),
	"WriteToolResultEvent":       reflect.TypeFor[extension.WriteToolResultEvent](),
	"GrepToolResultEvent":        reflect.TypeFor[extension.GrepToolResultEvent](),
	"FindToolResultEvent":        reflect.TypeFor[extension.FindToolResultEvent](),
	"LsToolResultEvent":          reflect.TypeFor[extension.LsToolResultEvent](),
	"CustomToolResultEvent":      reflect.TypeFor[extension.CustomToolResultEvent](),

	// ─── *EventResult types (5 parser-visible) ──────────────────────────
	"ContextEventResult":          reflect.TypeFor[extension.ContextEventResult](),
	"ProjectTrustEventResult":     reflect.TypeFor[extension.ProjectTrustEventResult](),
	"ToolCallEventResult":         reflect.TypeFor[extension.ToolCallEventResult](),
	"UserBashEventResult":         reflect.TypeFor[extension.UserBashEventResult](),
	"ToolResultEventResult":       reflect.TypeFor[extension.ToolResultEventResult](),
	"MessageEndEventResult":       reflect.TypeFor[extension.MessageEndEventResult](),
	"BeforeAgentStartEventResult": reflect.TypeFor[extension.BeforeAgentStartEventResult](),
	// BeforeProviderRequestEventResult is `= any` upstream (`unknown`); the
	// alias resolves to interface{} which has no fields. Wrapped via
	// indirection so the registry compiles. The fields-test guards on
	// Kind() != Struct.
	"BeforeProviderRequestEventResult": reflect.TypeFor[extension.BeforeProviderRequestEventResult](),
}
