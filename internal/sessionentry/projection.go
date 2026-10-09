package sessionentry

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// SessionTreeNode is a defensive copy of the session's branch
// structure for the /tree overlay (follow-up).
type SessionTreeNode struct {
	Entry    SessionEntry
	Children []*SessionTreeNode
	Label    string
	// LabelTimestamp is the on-disk timestamp of the LabelEntry that
	// set Label, in upstream's ISO-8601-ish wire format. Empty when
	// no label is set. Used by the /tree picker to render `hh:mm`
	// next to the label when the user toggles label timestamps on
	// (mirrors upstream tree-selector.ts:678-682).
	LabelTimestamp string
}

// SessionContextModel is the model identity last selected on a branch.
type SessionContextModel struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// ProjectedSessionEntry pairs a raw append-only entry with the model-visible
// messages it contributes after context edits (session-manager.ts
// ProjectedSessionEntry). Messages is empty for state-only entries and
// omissions.
type ProjectedSessionEntry struct {
	SourceEntry SessionEntry
	Messages    []agent.AgentMessage
}

// SessionProjection is the provenance-preserving, compaction-aware model
// context of one branch (session-manager.ts SessionProjection).
type SessionProjection struct {
	Entries  []ProjectedSessionEntry
	Messages []agent.AgentMessage
	// ThinkingLevel and Model are the latest settings on the projected branch; ThinkingLevel is "off" and Model nil when the branch has none.
	ThinkingLevel string
	Model         *SessionContextModel
}

// ContextEditableContent is the content a context edit may replace: a user, assistant, tool-result or custom message's content, a JSON string or block array. It stays raw JSON until the projection decodes it for the target's role.
// upstream: session-manager.ts:168 ContextEditableContent
type ContextEditableContent = json.RawMessage

// ContextEditReplacement is the non-null replacement of a context_edit entry
// (session-manager.ts ContextEditEntry.replacement). Content is the JSON
// string or content-block array that replaces the target's content
// (ContextEditableContent).
type ContextEditReplacement struct {
	Content ContextEditableContent `json:"content"`
}

// SessionContext contains model-visible messages and the settings on their full branch path.
type SessionContext struct {
	Messages      []agent.AgentMessage `json:"messages"`
	ThinkingLevel string               `json:"thinkingLevel"`
	Model         *SessionContextModel `json:"model"`
}

// NewSessionOptions are the options of Session.NewSession. A nil ID generates one.
// Mirrors upstream NewSessionOptions (core/session-manager.ts:52).
type NewSessionOptions struct {
	ID            *string
	ParentSession string
}
