package subprocess

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Child agent sessions: Pi's `createAgentSession` (core/sdk.ts) as an extension calls it. The Node runtime serves Pi's own implementation in the extension process (runtime-node/shims/independent-session.mjs); the Go, Python and Rust SDKs reach the host, which owns each session for the extension's connection and tears it down with the connection. The shapes follow the conventions of protocol.go: one current wire, no version field, every SDK speaks them.
//
// The extension chooses the session's id, so the tools it serves and the events it receives name the session before the create call returns. Calls run in the order the extension sent them. `childSession.create`, `.prompt`, `.queue`, `.bindExtensions` and `.abort` are Promise-shaped upstream: they run on their own goroutine after the call's lane advanced, so a later call (an abort from a signal listener) never waits for them.

// Child session calls (ext→host). Args and results are the types named by each constant.
const (
	// CallChildSessionCreate creates a session. Args is a ChildSessionCreate and the result a ChildSessionInfo. A closed connection refuses it with ChildSessionClosedMessage.
	CallChildSessionCreate = "childSession.create"
	// CallChildSessionPrompt runs AgentSession.prompt. Args is a ChildSessionPrompt and the result a ChildSessionPromptResult. It returns when the run has settled, as Pi's Promise does, and every event of the run has been sent before the result.
	CallChildSessionPrompt = "childSession.prompt"
	// CallChildSessionQueue runs AgentSession.steer or followUp. Args is a ChildSessionQueue.
	CallChildSessionQueue = "childSession.queue"
	// CallChildSessionAbort runs AgentSession.abort, which returns once the session is idle. Args is a ChildSessionRef.
	CallChildSessionAbort = "childSession.abort"
	// CallChildSessionDispose runs AgentSession.dispose. Args is a ChildSessionRef. A disposed session is gone: later calls on it fail with ChildSessionUnknownMessage, and no later event is sent.
	CallChildSessionDispose = "childSession.dispose"
	// CallChildSessionBindExtensions runs AgentSession.bindExtensions({}) for a session whose resources loaded extensions: they start headless (no UI) and receive session_start. Args is a ChildSessionRef.
	CallChildSessionBindExtensions = "childSession.bindExtensions"
	// CallChildSessionState reads the session's current state. Args is a ChildSessionRef and the result a ChildSessionState.
	CallChildSessionState = "childSession.state"
	// CallChildSessionMessages reads AgentSession.messages. Args is a ChildSessionRef and the result is the array of messages in Pi's shape.
	CallChildSessionMessages = "childSession.messages"
)

// NotifyChildSessionEvent (host→ext) carries one AgentSession event to the listeners of a session. Args is a ChildSessionEvent. Events of a session are numbered from 1 in the order the session emitted them, and the notification of event n precedes every frame the host writes after the listener returned, so a prompt's result follows the notifications of its run.
const NotifyChildSessionEvent = "childSession.event"

// Messages the host answers with, which every SDK raises as the call's error.
const (
	// ChildSessionClosedMessage is Node's message for a connection that closed (independent-session-owner.mjs).
	ChildSessionClosedMessage = "Extension connection is closed"
	// ChildSessionClosedWhileCreatingMessage is Node's message for a connection that closed while a session was being created.
	ChildSessionClosedWhileCreatingMessage = "Extension connection closed while creating a Session"
	// ChildSessionUnknownMessage answers a call that names no live session of the connection.
	ChildSessionUnknownMessage = "Unknown or disposed child session"
)

// ChildSessionRef names a session of the connection.
type ChildSessionRef struct {
	ID string `json:"id"`
}

// ChildSessionCreate is the CallChildSessionCreate argument: the members of Pi's CreateAgentSessionOptions (core/sdk.ts:48-106) an extension can pass across a process boundary. A member that is absent is Pi's undefined.
//
// The session shares the parent's model runtime (Pi's `modelRuntime: ctx.modelRegistry.runtime`), so every provider the parent's extensions registered is available to it. It never prompts or replaces the enclosing Session.
type ChildSessionCreate struct {
	// ID is the extension's name for the session; unique within the connection.
	ID string `json:"id"`
	// Cwd is the working directory for project-local discovery. Absent is the session manager's cwd, which is the host's.
	Cwd string `json:"cwd,omitempty"`
	// AgentDir is the global config directory. Absent is the host's.
	AgentDir string `json:"agentDir,omitempty"`
	// Model is an object whose provider and id name a model, as Pi's option takes a Model. Absent selects Pi's default (settings, then the first available model).
	Model json.RawMessage `json:"model,omitempty"`
	// ThinkingLevel is the requested level, clamped to the model. Absent is Pi's default.
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
	// Tools is the allowlist of tool names or `*` patterns, or a list of only `+name` and `-name` entries. Absent is Pi's undefined, an empty list is an allowlist of nothing.
	Tools *[]string `json:"tools,omitempty"`
	// ExcludeTools is the denylist applied after Tools.
	//portlint:allow emptydrop PiG wire field read by the host as a list: an empty list and an absent one mean the same (nothing to exclude or add)
	ExcludeTools []string `json:"excludeTools,omitempty"`
	// NoTools is "all" or "builtin".
	NoTools string `json:"noTools,omitempty"`
	// CustomTools are tools the extension serves: the host asks it to run one with a tool_call request that names the session in RequestPayload.ChildSession, and the result becomes the session's tool result.
	//portlint:allow emptydrop PiG wire field read by the host as a list: an empty list and an absent one mean the same (nothing to exclude or add)
	CustomTools []ToolDecl `json:"customTools,omitempty"`
	// Settings are in-memory settings, Pi's `SettingsManager.inMemory(settings)`: the session reads no settings file. Absent is `SettingsManager.create(cwd, agentDir)`, the settings files.
	Settings json.RawMessage `json:"settings,omitempty"`
	// InMemory is `SessionManager.inMemory(cwd)`: the session log is never written. Absent is `SessionManager.create(cwd)`.
	InMemory bool `json:"inMemory,omitempty"`
	// Resources selects what the session's resource loader loads: Pi's DefaultResourceLoader options.
	Resources *ChildSessionResources `json:"resources,omitempty"`
	// SessionManager, SettingsManager, ResourceLoader and ModelRuntime name session graph objects the extension built on this connection (CallSessionGraphNew): Pi's `sessionManager`, `settingsManager`, `resourceLoader` and `modelRuntime` options of createAgentSession. The session uses the very objects. Each replaces the option that builds its own (InMemory, Settings, Resources; the parent's model runtime), so naming both is an error.
	SessionManager  string `json:"sessionManager,omitempty"`
	SettingsManager string `json:"settingsManager,omitempty"`
	ResourceLoader  string `json:"resourceLoader,omitempty"`
	ModelRuntime    string `json:"modelRuntime,omitempty"`
	// ScopedModels is Pi's `scopedModels`: the models the session cycles through.
	//portlint:allow emptydrop PiG wire field read by the host as a list: an empty list and an absent one mean the same (nothing to exclude or add)
	ScopedModels []ChildScopedModel `json:"scopedModels,omitempty"`
}

// ChildScopedModel is one entry of Pi's scopedModels option: a model object and an optional thinking level.
type ChildScopedModel struct {
	Model         json.RawMessage `json:"model"`
	ThinkingLevel string          `json:"thinkingLevel,omitempty"`
}

// ChildSessionResources are the DefaultResourceLoader options of Pi (resource-loader.ts) that select resources.
type ChildSessionResources struct {
	NoExtensions      bool `json:"noExtensions,omitempty"`
	NoSkills          bool `json:"noSkills,omitempty"`
	NoPromptTemplates bool `json:"noPromptTemplates,omitempty"`
	NoThemes          bool `json:"noThemes,omitempty"`
	NoContextFiles    bool `json:"noContextFiles,omitempty"`
	// AdditionalExtensionPaths, AdditionalSkillPaths and AdditionalPromptTemplatePaths are loaded after the resolved resources.
	//portlint:allow emptydrop PiG wire field read by the host as a list: an empty list and an absent one mean the same (nothing to exclude or add)
	AdditionalExtensionPaths []string `json:"additionalExtensionPaths,omitempty"`
	//portlint:allow emptydrop PiG wire field read by the host as a list: an empty list and an absent one mean the same (nothing to exclude or add)
	AdditionalSkillPaths []string `json:"additionalSkillPaths,omitempty"`
	//portlint:allow emptydrop PiG wire field read by the host as a list: an empty list and an absent one mean the same (nothing to exclude or add)
	AdditionalPromptTemplatePaths []string `json:"additionalPromptTemplatePaths,omitempty"`
	// ExtensionNames keeps only the extensions whose path contains one of the names, compared without case: Pi's `extensionsOverride`. Absent keeps all, an empty list none.
	ExtensionNames *[]string `json:"extensionNames,omitempty"`
	// SkillNames keeps only the skills named exactly by one of the names: Pi's `skillsOverride`. Absent keeps all, an empty list none.
	SkillNames *[]string `json:"skillNames,omitempty"`
	// SystemPrompt replaces SYSTEM.md discovery; AppendSystemPrompt replaces APPEND_SYSTEM.md discovery. Each is text or the path of a file holding it.
	SystemPrompt       *string   `json:"systemPrompt,omitempty"`
	AppendSystemPrompt *[]string `json:"appendSystemPrompt,omitempty"`
}

// ChildSessionInfo is the CallChildSessionCreate result: what Pi's createAgentSession returns and the session shows right after creating it.
type ChildSessionInfo struct {
	// SessionID and SessionFile are the session's (`session.sessionId`, `session.sessionFile`); the file is absent for an in-memory session.
	SessionID   string `json:"sessionId"`
	SessionFile string `json:"sessionFile,omitempty"`
	// Model is the session's model as an extension model object, or null without one.
	Model json.RawMessage `json:"model"`
	// ThinkingLevel is the clamped level the session holds.
	ThinkingLevel string `json:"thinkingLevel"`
	// ActiveTools is `session.getActiveToolNames()`.
	ActiveTools []string `json:"activeTools"`
	// SystemPrompt is `session.systemPrompt`.
	SystemPrompt string `json:"systemPrompt"`
	// ModelFallbackMessage is CreateAgentSessionResult.modelFallbackMessage.
	ModelFallbackMessage string `json:"modelFallbackMessage,omitempty"`
	// ExtensionErrors are the errors of CreateAgentSessionResult.extensionsResult.errors, as "path: error".
	//portlint:allow emptydrop PiG wire field every SDK reads as a list, an absent one as empty
	ExtensionErrors []string `json:"extensionErrors,omitempty"`
}

// ChildSessionImage is an image of a prompt.
type ChildSessionImage struct {
	Type     string `json:"type,omitempty"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// ChildSessionPrompt is the CallChildSessionPrompt argument: AgentSession.prompt(text, options).
type ChildSessionPrompt struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	//portlint:allow emptydrop PiG wire field: an empty image list and an absent one send the same prompt
	Images []ChildSessionImage `json:"images,omitempty"`
	// ExpandPromptTemplates is Pi's option; absent is true.
	ExpandPromptTemplates *bool `json:"expandPromptTemplates,omitempty"`
	// StreamingBehavior is "steer" or "followUp"; it is required to prompt a session that is running.
	StreamingBehavior string `json:"streamingBehavior,omitempty"`
	// Source is the input source ("interactive", "rpc" or "extension"); absent is Pi's "interactive".
	Source string `json:"source,omitempty"`
}

// ChildSessionPromptResult is the CallChildSessionPrompt result. Events is the number of the session's last event when the run settled: the SDK resolves the prompt once its listeners have run through that event.
type ChildSessionPromptResult struct {
	Events uint64 `json:"events"`
}

// ChildSessionQueue is the CallChildSessionQueue argument: AgentSession.steer or followUp.
type ChildSessionQueue struct {
	ID       string `json:"id"`
	Behavior string `json:"behavior"` // "steer" | "followUp"
	Text     string `json:"text"`
	//portlint:allow emptydrop PiG wire field: an empty image list and an absent one send the same message
	Images []ChildSessionImage `json:"images,omitempty"`
}

// ChildSessionEvent is the NotifyChildSessionEvent argument. Event is the AgentSessionEvent in Pi's JSON shape, the one print mode's JSON stream writes.
type ChildSessionEvent struct {
	ID    string          `json:"id"`
	Seq   uint64          `json:"seq"`
	Event json.RawMessage `json:"event,omitempty"`
	// Stream numbers a message_update's payload in the session's sequence (EventStream): with Event it is a keyframe the extension keeps; without, it carries Ops that turn the payload numbered Base into the event, and the extension hands its listeners the full event either way. Seq is the event's number, so Base is the number of the previous message_update of the session. Absent, the event travels whole and leaves the extension's copy as it is.
	Stream *EventStream `json:"stream,omitempty"`
}

// ChildSessionState is the CallChildSessionState result.
type ChildSessionState struct {
	SessionID     string          `json:"sessionId"`
	SessionFile   string          `json:"sessionFile,omitempty"`
	Model         json.RawMessage `json:"model"`
	IsStreaming   bool            `json:"isStreaming"`
	IsIdle        bool            `json:"isIdle"`
	ThinkingLevel string          `json:"thinkingLevel"`
	ActiveTools   []string        `json:"activeTools"`
	SystemPrompt  string          `json:"systemPrompt"`
	// PendingMessageCount is AgentSession.pendingMessageCount.
	PendingMessageCount int `json:"pendingMessageCount"`
	// LastAssistantText is getLastAssistantText(), absent when there is none.
	LastAssistantText *string `json:"lastAssistantText,omitempty"`
}
