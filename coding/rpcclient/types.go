package rpcclient

import (
	"encoding/json"
)

// RpcClientOptions mirrors upstream RpcClientOptions. CliPath names the pig
// executable to run (upstream runs `node <cliPath>`); it defaults to "pig",
// resolved through PATH.
type RpcClientOptions struct {
	// CliPath is the agent executable. Default: "pig".
	CliPath string
	// Cwd is the working directory for the agent.
	Cwd string
	// Env adds or overrides environment variables on top of the current
	// process environment.
	Env map[string]string
	// Provider is passed as --provider when non-empty.
	Provider string
	// Model is passed as --model when non-empty.
	Model string
	// Args are appended after the mode, provider, and model arguments.
	Args []string
}

// ModelInfo mirrors upstream ModelInfo.
type ModelInfo struct {
	Provider      string `json:"provider"`
	ID            string `json:"id"`
	ContextWindow int    `json:"contextWindow"`
	Reasoning     bool   `json:"reasoning"`
}

// ThinkingLevel is a pi-agent-core thinking level ("off", "minimal", "low",
// "medium", "high", "xhigh", "max").
type ThinkingLevel string

// ImageContent is a pi-ai image block; it marshals with type "image".
type ImageContent struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// MarshalJSON emits the pi-ai wire shape {type:"image", data, mimeType}.
func (c ImageContent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type     string `json:"type"`
		Data     string `json:"data"`
		MimeType string `json:"mimeType"`
	}{"image", c.Data, c.MimeType})
}

// RpcResponse mirrors upstream RpcResponse. Data holds the raw success
// payload; Error holds the failure message.
type RpcResponse struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// JsonAgentSessionEvent is one stdout record that is not a response to a
// pending request: an agent session event, an extension UI request, or an
// unmatched response. Type is its "type" field; Raw is the complete record.
type JsonAgentSessionEvent struct {
	Type string
	Raw  json.RawMessage `json:"-"`
}

// RpcEventListener mirrors upstream RpcEventListener.
type RpcEventListener func(event JsonAgentSessionEvent)

// Model is the model object RPC responses carry (pi-ai Model). The common
// fields are decoded; Raw keeps the complete object.
type Model struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	API           string          `json:"api"`
	Provider      string          `json:"provider"`
	BaseURL       string          `json:"baseUrl"`
	Reasoning     bool            `json:"reasoning"`
	Input         []string        `json:"input"`
	ContextWindow int             `json:"contextWindow"`
	MaxTokens     int             `json:"maxTokens"`
	Raw           json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes the common fields and retains the raw object.
func (m *Model) UnmarshalJSON(data []byte) error {
	type plain Model
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*m = Model(decoded)
	m.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// RpcSessionState mirrors upstream RpcSessionState.
type RpcSessionState struct {
	Model                 *Model        `json:"model,omitempty"`
	ThinkingLevel         ThinkingLevel `json:"thinkingLevel"`
	IsStreaming           bool          `json:"isStreaming"`
	IsCompacting          bool          `json:"isCompacting"`
	SteeringMode          string        `json:"steeringMode"`
	FollowUpMode          string        `json:"followUpMode"`
	SessionFile           *string       `json:"sessionFile,omitempty"`
	SessionID             string        `json:"sessionId"`
	SessionName           *string       `json:"sessionName,omitempty"`
	AutoCompactionEnabled bool          `json:"autoCompactionEnabled"`
	MessageCount          int           `json:"messageCount"`
	PendingMessageCount   int           `json:"pendingMessageCount"`
}

// RpcSourceInfo mirrors the sourceInfo carried by RpcSlashCommand.
type RpcSourceInfo struct {
	Path    string `json:"path"`
	Source  string `json:"source"`
	Scope   string `json:"scope"`
	Origin  string `json:"origin"`
	BaseDir string `json:"baseDir,omitempty"`
}

// RpcSlashCommand mirrors upstream RpcSlashCommand.
type RpcSlashCommand struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Source      string        `json:"source"`
	SourceInfo  RpcSourceInfo `json:"sourceInfo"`
}

// BashResult mirrors upstream BashResult. ExitCode is nil when the command
// was killed or cancelled.
type BashResult struct {
	Output         string `json:"output"`
	ExitCode       *int   `json:"exitCode,omitempty"`
	Cancelled      bool   `json:"cancelled"`
	Truncated      bool   `json:"truncated"`
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

// CompactionResult mirrors upstream CompactionResult; Usage and Details stay
// raw JSON.
type CompactionResult struct {
	Summary              string          `json:"summary"`
	FirstKeptEntryID     string          `json:"firstKeptEntryId"`
	TokensBefore         int             `json:"tokensBefore"`
	EstimatedTokensAfter *int            `json:"estimatedTokensAfter,omitempty"`
	Usage                json.RawMessage `json:"usage,omitempty"`
	Details              json.RawMessage `json:"details,omitempty"`
}

// SessionStatsTokens mirrors SessionStats.tokens.
type SessionStatsTokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Total      int `json:"total"`
}

// ContextUsage mirrors upstream ContextUsage.
type ContextUsage struct {
	Tokens        *int     `json:"tokens"`
	ContextWindow int      `json:"contextWindow"`
	Percent       *float64 `json:"percent"`
}

// SessionStats mirrors upstream SessionStats.
type SessionStats struct {
	SessionFile       *string            `json:"sessionFile,omitempty"`
	SessionID         string             `json:"sessionId"`
	UserMessages      int                `json:"userMessages"`
	AssistantMessages int                `json:"assistantMessages"`
	ToolCalls         int                `json:"toolCalls"`
	ToolResults       int                `json:"toolResults"`
	TotalMessages     int                `json:"totalMessages"`
	Tokens            SessionStatsTokens `json:"tokens"`
	Cost              float64            `json:"cost"`
	ContextUsage      *ContextUsage      `json:"contextUsage,omitempty"`
}

// SessionEntry is one session entry. The identity fields are decoded; Raw
// keeps the complete entry.
type SessionEntry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Raw       json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes the identity fields and retains the raw entry.
func (e *SessionEntry) UnmarshalJSON(data []byte) error {
	type plain SessionEntry
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*e = SessionEntry(decoded)
	e.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// SessionTreeNode mirrors upstream SessionTreeNode.
type SessionTreeNode struct {
	Entry          SessionEntry      `json:"entry"`
	Children       []SessionTreeNode `json:"children"`
	Label          *string           `json:"label,omitempty"`
	LabelTimestamp *string           `json:"labelTimestamp,omitempty"`
}

// AgentMessage is one agent message; Role is decoded and Raw keeps the
// complete message.
type AgentMessage struct {
	Role string          `json:"role"`
	Raw  json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes the role and retains the raw message.
func (m *AgentMessage) UnmarshalJSON(data []byte) error {
	type plain AgentMessage
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*m = AgentMessage(decoded)
	m.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// ModelReference is the {provider, id} pair upstream setModel and cycleModel
// declare.
type ModelReference struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// CycleModelResult mirrors upstream cycleModel's non-null result.
type CycleModelResult struct {
	Model         ModelReference `json:"model"`
	ThinkingLevel ThinkingLevel  `json:"thinkingLevel"`
	IsScoped      bool           `json:"isScoped"`
}

// CycleThinkingLevelResult mirrors upstream cycleThinkingLevel's non-null
// result.
type CycleThinkingLevelResult struct {
	Level ThinkingLevel `json:"level"`
}

// ClearQueueResult mirrors upstream clearQueue's result.
type ClearQueueResult struct {
	Steering []string `json:"steering"`
	FollowUp []string `json:"followUp"`
}

// CancelledResult mirrors the {cancelled} result of session replacement.
type CancelledResult struct {
	Cancelled bool `json:"cancelled"`
}

// ForkResult mirrors upstream fork's result.
type ForkResult struct {
	Text      string `json:"text"`
	Cancelled bool   `json:"cancelled"`
}

// ForkMessage mirrors one getForkMessages entry.
type ForkMessage struct {
	EntryID string `json:"entryId"`
	Text    string `json:"text"`
}

// ExportHtmlResult mirrors upstream exportHtml's result.
type ExportHtmlResult struct {
	Path string `json:"path"`
}

// GetEntriesResult mirrors upstream getEntries' result.
type GetEntriesResult struct {
	Entries []SessionEntry `json:"entries"`
	LeafID  *string        `json:"leafId"`
}

// GetTreeResult mirrors upstream getTree's result.
type GetTreeResult struct {
	Tree   []SessionTreeNode `json:"tree"`
	LeafID *string           `json:"leafId"`
}
