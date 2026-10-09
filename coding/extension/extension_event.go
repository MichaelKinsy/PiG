package extension

// ExtensionEvent is the union of every event an extension handler receives (types.ts:1377 ExtensionEvent). It is a closed union: only the
// event types of this package implement it, which is what the Pi union lists; the tool_call and tool_result members are the sealed
// ToolCallEvent and ToolResultEvent unions, whose variants implement it as well.
type ExtensionEvent interface{ extensionEvent() }

func (ProjectTrustEvent) extensionEvent()          {}
func (ResourcesDiscoverEvent) extensionEvent()     {}
func (McpServersChangeEvent) extensionEvent()      {}
func (SessionStartEvent) extensionEvent()          {}
func (SessionInfoChangedEvent) extensionEvent()    {}
func (SessionBeforeSwitchEvent) extensionEvent()   {}
func (SessionBeforeForkEvent) extensionEvent()     {}
func (SessionBeforeCompactEvent) extensionEvent()  {}
func (SessionCompactEvent) extensionEvent()        {}
func (SessionCompactFailedEvent) extensionEvent()  {}
func (SessionShutdownEvent) extensionEvent()       {}
func (SessionBeforeTreeEvent) extensionEvent()     {}
func (SessionTreeEvent) extensionEvent()           {}
func (ContextEvent) extensionEvent()               {}
func (ContextWithSystemEvent) extensionEvent()     {}
func (CacheWarmingDecisionEvent) extensionEvent()  {}
func (BeforeProviderRequestEvent) extensionEvent() {}
func (BeforeProviderHeadersEvent) extensionEvent() {}
func (AfterProviderResponseEvent) extensionEvent() {}
func (ProviderStreamEvent) extensionEvent()        {}
func (BeforeAgentStartEvent) extensionEvent()      {}
func (AgentStartEvent) extensionEvent()            {}
func (AgentEndEvent) extensionEvent()              {}
func (AgentBeforeSettleEvent) extensionEvent()     {}
func (AgentSettledEvent) extensionEvent()          {}
func (UIPromptStartEvent) extensionEvent()         {}
func (UIPromptEndEvent) extensionEvent()           {}
func (TurnStartEvent) extensionEvent()             {}
func (TurnEndEvent) extensionEvent()               {}
func (MessageStartEvent) extensionEvent()          {}
func (MessageUpdateEvent) extensionEvent()         {}
func (MessageEndEvent) extensionEvent()            {}
func (ToolExecutionStartEvent) extensionEvent()    {}
func (ToolExecutionUpdateEvent) extensionEvent()   {}
func (ToolExecutionEndEvent) extensionEvent()      {}
func (ModelSelectEvent) extensionEvent()           {}
func (ThinkingLevelSelectEvent) extensionEvent()   {}
func (UserBashEvent) extensionEvent()              {}
func (InputEvent) extensionEvent()                 {}
func (CustomToolCallEvent) extensionEvent()        {}
func (BashToolResultEvent) extensionEvent()        {}
func (PowerShellToolResultEvent) extensionEvent()  {}
func (ReadToolResultEvent) extensionEvent()        {}
func (EditToolResultEvent) extensionEvent()        {}
func (WriteToolResultEvent) extensionEvent()       {}
func (GrepToolResultEvent) extensionEvent()        {}
func (FindToolResultEvent) extensionEvent()        {}
func (LsToolResultEvent) extensionEvent()          {}
func (CustomToolResultEvent) extensionEvent()      {}

// SessionEvent is the union of the session lifecycle events (types.ts:848 SessionEvent): a member of [ExtensionEvent] that carries a
// session_* type.
type SessionEvent interface {
	ExtensionEvent
	sessionEvent()
}

func (SessionStartEvent) sessionEvent()         {}
func (SessionInfoChangedEvent) sessionEvent()   {}
func (SessionBeforeSwitchEvent) sessionEvent()  {}
func (SessionBeforeForkEvent) sessionEvent()    {}
func (SessionBeforeCompactEvent) sessionEvent() {}
func (SessionCompactEvent) sessionEvent()       {}
func (SessionCompactFailedEvent) sessionEvent() {}
func (SessionShutdownEvent) sessionEvent()      {}
func (SessionBeforeTreeEvent) sessionEvent()    {}
func (SessionTreeEvent) sessionEvent()          {}
