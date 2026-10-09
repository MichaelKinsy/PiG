package extension

import (
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// SessionHeader is the session log's header line (session-manager.ts SessionHeader).
type SessionHeader = sessionentry.SessionHeader

// SessionTreeNode is one node of the session tree (session-manager.ts SessionTreeNode).
type SessionTreeNode = sessionentry.SessionTreeNode

// SessionProjection is the compaction-aware model context of one branch (session-manager.ts SessionProjection).
type SessionProjection = sessionentry.SessionProjection

// ReadonlySessionManager is the read-only view of the session log that ctx.sessionManager exposes to extensions (session-manager.ts:245
// ReadonlySessionManager, a Pick of SessionManager). Optional TypeScript results (`string | undefined`, `SessionEntry | undefined`) are a
// second bool or a nil pointer, as in the host's session log.
type ReadonlySessionManager interface {
	GetCwd() string
	GetSessionDir() string
	GetSessionId() string
	GetSessionFile() *string
	GetLeafID() *string
	GetLeafEntry() (SessionEntry, bool)
	GetEntry(id string) (SessionEntry, bool)
	GetLabel(id string) (string, bool)
	GetBranch(fromID ...string) []SessionEntry
	BuildContextEntries() []SessionEntry
	BuildSessionProjection() SessionProjection
	GetHeader() SessionHeader
	GetEntries() []SessionEntry
	GetTree() []*SessionTreeNode
	GetSessionName() string
}

// SessionContext is the model-visible messages and settings on a branch (session-manager.ts SessionContext).
type SessionContext = sessionentry.SessionContext

// ContextEditReplacement is the replacement of a context_edit entry (session-manager.ts ContextEditEntry["replacement"]).
type ContextEditReplacement = sessionentry.ContextEditReplacement

// SessionManagerNewSessionOptions are the options of [SessionManager.NewSession] (session-manager.ts:52 NewSessionOptions); [NewSessionOptions] is the
// parameter object of the command context's newSession.
type SessionManagerNewSessionOptions = sessionentry.NewSessionOptions

// UsageEntry is model-attributed usage that does not enter model context (session-manager.ts UsageEntry).
type UsageEntry = sessionentry.UsageEntry

// SessionManager is the session log an extension's newSession setup callback receives (session-manager.ts:987 SessionManager): the read-only
// view plus every operation that changes the log. A TypeScript `string | undefined` or `void` result that can fail is an error as in the host's
// session log.
// SessionMessage is the union session-manager.ts appendMessage takes (Message | CustomMessage | BashExecutionMessage): an
// agent.AgentMessage, a CustomMessage or a BashExecutionMessage. MessageRole is the role of the message the entry stores.
type SessionMessage interface {
	MessageRole() string
}

type SessionManager interface {
	ReadonlySessionManager
	SetSessionFile(sessionFile string) error
	NewSession(options *SessionManagerNewSessionOptions) (string, error)
	IsPersisted() bool
	UsesDefaultSessionDir() bool
	AppendMessage(message SessionMessage) (string, error)
	AppendThinkingLevelChange(thinkingLevel string) (string, error)
	AppendModelChange(provider, modelID string) (string, error)
	AppendUsage(kind, provider, model string, usage ai.Usage, note string) (UsageEntry, error)
	AppendCompaction(summary, firstKeptEntryID string, tokensBefore int, details any, fromHook bool, usage *ai.Usage) (string, error)
	AppendCustomEntry(customType string, data any) (string, error)
	AppendSessionInfo(name string) (string, error)
	AppendCustomMessageEntry(customType string, content any, display bool, details any) (string, error)
	AppendContextEdit(targetID string, replacement *ContextEditReplacement) (string, error)
	GetChildren(parentID string) []SessionEntry
	AppendLabelChange(targetID string, label *string) (string, error)
	BuildSessionContext() SessionContext
	GetEntryCount() int
	Branch(branchFromID string) error
	ResetLeaf()
	BranchWithSummary(parentID *string, summary string, details any, fromHook bool, usage *ai.Usage) (string, error)
	CreateBranchedSession(leafID string) (string, error)
}
