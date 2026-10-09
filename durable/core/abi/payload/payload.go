// SPDX-License-Identifier: MIT

// Package payload holds the JSON shapes of ABI 1 event and effect payloads as hosts send and read them
// (ABI sections 5-7). It is host tooling: it uses encoding/json, and core packages never import it; the core reads and
// writes the same shapes with its own scanner and encoder. Field names follow the ABI document and Pi's own names.
package payload

import "encoding/json"

// SidecarMode selects the cold-open path of ADR D6 (BAKEOFF hypothesis H-cold).
type SidecarMode string

// Sidecar modes.
const (
	SidecarOff      SidecarMode = "off"
	SidecarIndex    SidecarMode = "index"
	SidecarSnapshot SidecarMode = "snapshot"
)

// ModelRef names a model the way Pi's agent document does.
type ModelRef struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// ToolDefinition is a registry tool: name, JSON schema and replay policy.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Replay      string          `json:"replay"`
}

// SectionDefinition is a system prompt section.
type SectionDefinition struct {
	Name string `json:"name"`
	Text string `json:"text"`
	Tag  bool   `json:"tag"`
}

// ExtensionSnapshot is one extension of the registry snapshot, in installation order.
type ExtensionSnapshot struct {
	Name     string              `json:"name"`
	Tools    []ToolDefinition    `json:"tools"`
	Sections []SectionDefinition `json:"sections"`
}

// OpenOptions is the payload of an open event.
type OpenOptions struct {
	// Settings carries the Harness settings the slice uses: {"compaction":{"enabled":false}} and progress intervals.
	Settings json.RawMessage `json:"settings"`
	// Agent is the root conversation's model.
	Agent ModelRef `json:"agent"`
	// Models are the catalogue entries in use.
	Models []json.RawMessage `json:"models"`
	// Registry is the registry snapshot.
	Registry []ExtensionSnapshot `json:"registry"`
	// CoreID is the layout identity the sidecar must carry (empty: the core's own).
	CoreID string `json:"coreId,omitempty"`
	// Sidecar selects the cold-open mode.
	Sidecar SidecarMode `json:"sidecar"`
	// Delta lets the core send model_context effects in the delta form (ABI section 6, effect 3).
	Delta bool `json:"delta"`
	// Debug rebuilds from rows and compares on every cold open (RISKS R11).
	Debug bool `json:"debug,omitempty"`
}

// SubmitEvent is the payload of a submit event (ABI section 5, kind 3).
type SubmitEvent struct {
	ConversationID *int64          `json:"conversationId,omitempty"`
	Type           string          `json:"type"`
	Content        json.RawMessage `json:"content,omitempty"`
	Entry          json.RawMessage `json:"entry,omitempty"`
	RequestID      string          `json:"requestId"`
	WhenBusy       string          `json:"whenBusy,omitempty"`
}

// ModelContextEffect is the payload of a model_context effect. Context is {messages}: pi-durable 1.0.4 passes no other
// field, and the system prompt and tool declarations are pi.system messages inside it (generation.ts:402). In the delta
// form Extends names the effect whose message list this one continues exactly as sent, Append holds the new messages, and
// Context is absent. Options is the request's stream options without the signal.
type ModelContextEffect struct {
	Model   ModelRef        `json:"model"`
	Context json.RawMessage `json:"context"`
	Options json.RawMessage `json:"options,omitempty"`
	Extends *uint32         `json:"extends,omitempty"`
	Append  json.RawMessage `json:"append,omitempty"`
}

// ToolEffect is the payload of a tool effect.
type ToolEffect struct {
	TaskID         int64           `json:"taskId"`
	ConversationID int64           `json:"conversationId"`
	ToolName       string          `json:"toolName"`
	CallID         string          `json:"callId"`
	Arguments      json.RawMessage `json:"arguments"`
	OutputWindow   json.RawMessage `json:"outputWindow,omitempty"`
	Replay         string          `json:"replay"`
}

// ToolResult is the JSON of a tool_done event with outcome result: pi-ai tool result content plus details.
type ToolResult struct {
	Content []json.RawMessage `json:"content"`
	Details json.RawMessage   `json:"details,omitempty"`
}

// InspectQuery is the payload of an inspect event.
type InspectQuery struct {
	Query          string `json:"query"` // "fingerprint", "context", "index", "memory"
	ConversationID int64  `json:"conversationId"`
	RequestID      uint32 `json:"requestId"` // echoed in the api_result notice that answers it
}

// Fingerprints is the answer to a "fingerprint" inspect query: the JSON of an api_result notice (CONTRACT section 5.4).
type Fingerprints struct {
	Ctx   string `json:"ctx"`
	Bench string `json:"bench"`
}

// SubmissionSettlement reports a submission's status. "queued" and "placed" are not terminal.
type SubmissionSettlement struct {
	ID        int64  `json:"id,omitempty"`
	RequestID string `json:"requestId"`
	Status    string `json:"status"`
}

// TaskStatusChange reports a task's new status.
type TaskStatusChange struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

// Published is the JSON of a published notice (ABI section 7): what one step's commits changed that observers see.
// The shim waits on Submissions to resolve Submission.wait.
type Published struct {
	//portlint:allow emptydrop the host decodes a notice into slices, where an absent list and [] are both empty
	Submissions []SubmissionSettlement `json:"submissions,omitempty"`
	//portlint:allow emptydrop the host decodes a notice into slices, where an absent list and [] are both empty
	Entries []int64 `json:"entries,omitempty"`
	//portlint:allow emptydrop the host decodes a notice into slices, where an absent list and [] are both empty
	Tasks     []TaskStatusChange `json:"tasks,omitempty"`
	Documents json.RawMessage    `json:"documents,omitempty"`
}

// SectionEffect is the payload of a section effect: call PromptSection.render() of the indexed section.
type SectionEffect struct {
	ConversationID int64           `json:"conversationId"`
	TaskID         int64           `json:"taskId"`
	Section        int             `json:"section"`
	Input          json.RawMessage `json:"input"`
}

// DeferredEffect is the payload of a deferred effect: models.fetchDeferred or models.cancelDeferred.
type DeferredEffect struct {
	Op     string          `json:"op"` // "fetch" or "cancel"
	Model  ModelRef        `json:"model"`
	Handle json.RawMessage `json:"handle"`
}

// CallbackEffect is the payload of a callback effect: host code Pi runs inside an open commit callback.
type CallbackEffect struct {
	Name    string          `json:"name"`
	TxID    uint32          `json:"txId"`
	Payload json.RawMessage `json:"payload"`
}
