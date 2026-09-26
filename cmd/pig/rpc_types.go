// Go types for the pinned Pi RPC protocol.
// Commands arrive as JSON lines on stdin; responses and events are emitted
// as JSON lines on stdout. Each object has a "type" discriminant field.
//
// Upstream reference:
//   .upstream/current/packages/coding-agent/src/modes/rpc/rpc-types.ts

package main

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	codingcompaction "github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// ─── Commands (stdin) ─────────────────────────────────────────────────────────

// RPCCommandEnvelope is the raw JSON wrapper. The Type field selects which
// concrete command struct to unmarshal into.
type RPCCommandEnvelope struct {
	ID   string          `json:"id,omitempty"`
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"` // full original bytes, set by parseRPCCommand
}

// RPCPromptCommand sends a message to the agent.
// Upstream: rpc-types.ts:25 { type: "prompt"; message: string }
type RPCImageContent struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

func (i RPCImageContent) imageContent() ai.ImageContent {
	return ai.ImageContent{Data: i.Data, MimeType: i.MimeType}
}

type RPCPromptCommand struct {
	ID                string            `json:"id,omitempty"`
	Type              string            `json:"type"`
	Message           string            `json:"message"`
	Images            []RPCImageContent `json:"images,omitempty"`
	StreamingBehavior string            `json:"streamingBehavior,omitempty"`
}

// RPCAbortCommand cancels the current operation.
// Upstream: rpc-types.ts:28 { type: "abort" }
type RPCAbortCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
}

// RPCClearQueueCommand removes queued steering and follow-up messages.
type RPCClearQueueCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
}

type RPCNewSessionCommand struct {
	ID            string `json:"id,omitempty"`
	Type          string `json:"type"`
	ParentSession string `json:"parentSession,omitempty"`
}

type RPCCycleModelCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
}

type RPCGetAvailableThinkingLevelsCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
}

type RPCAbortRetryCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
}

type RPCExportHTMLCommand struct {
	ID         string `json:"id,omitempty"`
	Type       string `json:"type"`
	OutputPath string `json:"outputPath,omitempty"`
}

type RPCSwitchSessionCommand struct {
	ID          string `json:"id,omitempty"`
	Type        string `json:"type"`
	SessionPath string `json:"sessionPath"`
}

type RPCForkCommand struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	EntryID string `json:"entryId"`
}

type RPCCloneCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
}

type RPCExtensionUIResponse struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Value     string `json:"value,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
}

// RPCGetStateCommand returns current session state.
// Upstream: rpc-types.ts:31 { type: "get_state" }
type RPCGetStateCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
}

// RPCGetCommandsCommand returns extension, prompt, and skill commands that are
// invokable through the RPC prompt path.
type RPCGetCommandsCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
}

// RPCSetModelCommand changes the active model.
// Upstream: rpc-types.ts { type: "set_model"; provider: string; modelId: string }
type RPCSetModelCommand struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// RPCCompactCommand triggers manual compaction.
// Upstream: rpc-types.ts { type: "compact"; customInstructions?: string }
type RPCCompactCommand struct {
	ID                 string `json:"id,omitempty"`
	Type               string `json:"type"`
	CustomInstructions string `json:"customInstructions,omitempty"`
}

// RPCSetAutoCompactionCommand enables/disables auto-compaction.
// Upstream: rpc-types.ts { type: "set_auto_compaction"; enabled: boolean }
type RPCSetAutoCompactionCommand struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
}

// RPCSetThinkingLevelCommand changes the thinking level.
// Upstream: rpc-types.ts { type: "set_thinking_level"; level: ThinkingLevel }
type RPCSetThinkingLevelCommand struct {
	ID    string `json:"id,omitempty"`
	Type  string `json:"type"`
	Level string `json:"level"` // "off"|"minimal"|"low"|"medium"|"high"|"xhigh"|"max"
}

// ─── Responses / Events (stdout) ─────────────────────────────────────────────

// RPCResponse wraps a command acknowledgement.
// Upstream: rpc-types.ts:110 { type: "response"; command: ...; success: bool; data? }
type RPCResponse struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`    // always "response"
	Command string `json:"command"` // mirrors the command type
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

type rpcNullResponse struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Command string `json:"command"`
	Success bool   `json:"success"`
	Data    any    `json:"data"`
}

type RPCClearQueueData struct {
	Steering []string `json:"steering"`
	FollowUp []string `json:"followUp"`
}

// RPCSourceInfo is upstream's SourceInfo on the RPC wire; extensions receive
// the same shape.
type RPCSourceInfo = codingagent.PiSourceInfo

// RPCSlashCommand is upstream's RpcSlashCommand (SlashCommandInfo).
type RPCSlashCommand = codingagent.PiSlashCommand

type RPCCancelledResult struct {
	Cancelled bool `json:"cancelled"`
}

type RPCForkResult struct {
	Text      string `json:"text"`
	Cancelled bool   `json:"cancelled"`
}

type RPCModelCycleResult struct {
	Model         *RPCModel        `json:"model"`
	ThinkingLevel ai.ThinkingLevel `json:"thinkingLevel"`
	IsScoped      bool             `json:"isScoped"`
}

type RPCGetCommandsData struct {
	Commands []RPCSlashCommand `json:"commands"`
}

type RPCModelCostTier struct {
	InputTokensAbove int     `json:"inputTokensAbove"`
	Input            float64 `json:"input"`
	Output           float64 `json:"output"`
	CacheRead        float64 `json:"cacheRead"`
	CacheWrite       float64 `json:"cacheWrite"`
}

type RPCModelCost struct {
	Input      float64            `json:"input"`
	Output     float64            `json:"output"`
	CacheRead  float64            `json:"cacheRead"`
	CacheWrite float64            `json:"cacheWrite"`
	Tiers      []RPCModelCostTier `json:"tiers,omitempty"`
}

type RPCModel struct {
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	API              ai.API               `json:"api"`
	Provider         string               `json:"provider"`
	BaseURL          string               `json:"baseUrl"`
	Reasoning        bool                 `json:"reasoning"`
	ThinkingLevelMap ai.ThinkingLevelMap  `json:"thinkingLevelMap,omitempty"`
	Input            []string             `json:"input"`
	Cost             RPCModelCost         `json:"cost"`
	PromptCache      ai.ModelPromptCache  `json:"promptCache,omitempty"`
	ContextWindow    int                  `json:"contextWindow"`
	MaxTokens        int                  `json:"maxTokens"`
	SamplingParams   map[string]any       `json:"samplingParams,omitempty"`
	Headers          map[string]string    `json:"headers,omitempty"`
	Compat           *ai.ModelCompat      `json:"compat,omitempty"`
	InputLimits      *ai.ModelInputLimits `json:"inputLimits,omitempty"`
}

// RPCSessionState is the payload for a get_state response.
type RPCSessionState struct {
	Model                 *RPCModel `json:"model"`
	ThinkingLevel         string    `json:"thinkingLevel"`
	IsStreaming           bool      `json:"isStreaming"`
	IsCompacting          bool      `json:"isCompacting"`
	SteeringMode          string    `json:"steeringMode"`
	FollowUpMode          string    `json:"followUpMode"`
	SessionFile           string    `json:"sessionFile,omitempty"`
	SessionID             string    `json:"sessionId"`
	SessionName           string    `json:"sessionName,omitempty"`
	AutoCompactionEnabled bool      `json:"autoCompactionEnabled"`
	MessageCount          int       `json:"messageCount"`
	PendingMessageCount   int       `json:"pendingMessageCount"`
}

type RPCQueueUpdateEvent struct {
	Type     string   `json:"type"`
	Steering []string `json:"steering"`
	FollowUp []string `json:"followUp"`
}

type RPCThinkingLevelChangedEvent struct {
	Type  string           `json:"type"`
	Level ai.ThinkingLevel `json:"level"`
}

// RPCMessageStartEvent is the legacy simplified event shape.
// RPC mode now forwards upstream-compatible AgentSessionEvent objects via
// rpcAgentEvent; this type remains only for older unit tests and callers
// that imported the concrete Go struct from package main.
type RPCMessageStartEvent struct {
	Type string `json:"type"` // "message_start"
}

// RPCTextDeltaEvent carries an incremental text chunk.
// Upstream: rpc-types.ts mirrors MessageUpdateEvent text delta.
type RPCTextDeltaEvent struct {
	Type    string `json:"type"`    // "text_delta"
	Content string `json:"content"` // incremental text
}

// RPCMessageEndEvent signals the end of an LLM response turn.
// Upstream: rpc-types.ts mirrors MessageEndEvent.
type RPCMessageEndEvent struct {
	Type string `json:"type"` // "message_end"
	Text string `json:"text"` // full accumulated text
}

// RPCToolUseEvent signals a tool call by the LLM.
// Upstream: rpc-types.ts mirrors ToolExecutionStartEvent.
type RPCToolUseEvent struct {
	Type string `json:"type"` // "tool_use"
	Name string `json:"name"`
	Args any    `json:"args"`
}

// RPCToolResultEvent signals a tool call result.
// Upstream: rpc-types.ts mirrors ToolExecutionEndEvent.
type RPCToolResultEvent struct {
	Type    string `json:"type"` // "tool_result"
	Name    string `json:"name"`
	Content string `json:"content"`
	IsError bool   `json:"isError"`
}

// RPCErrorEvent signals an error that is not tied to a specific command.
// Upstream: rpc-types.ts error response variant.
type RPCErrorEvent struct {
	Type    string `json:"type"` // "error"
	Message string `json:"message"`
}

// ─── Additional commands ────────────────────────────────────────────────

// RPCSteerCommand queues a steering message for the active agent turn.
type RPCSteerCommand struct {
	ID      string            `json:"id,omitempty"`
	Type    string            `json:"type"`
	Message string            `json:"message"`
	Images  []RPCImageContent `json:"images,omitempty"`
}

// RPCFollowUpCommand queues a message after the active turn settles.
type RPCFollowUpCommand struct {
	ID      string            `json:"id,omitempty"`
	Type    string            `json:"type"`
	Message string            `json:"message"`
	Images  []RPCImageContent `json:"images,omitempty"`
}

// RPCBashCommand executes a bash command outside the LLM agent loop.
// Upstream: rpc-types.ts { type: "bash"; command: string; excludeFromContext?: boolean }
type RPCBashCommand struct {
	ID                 string `json:"id,omitempty"`
	Type               string `json:"type"`
	Command            string `json:"command"`
	ExcludeFromContext bool   `json:"excludeFromContext,omitempty"`
}

// RPCSetAutoRetryCommand enables/disables auto-retry.
// Upstream: rpc-types.ts { type: "set_auto_retry"; enabled: boolean }
type RPCSetAutoRetryCommand struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
}

// RPCSetSessionNameCommand sets the current session name.
// Upstream: rpc-types.ts { type: "set_session_name"; name: string }
type RPCSetSessionNameCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	Name string `json:"name"`
}

// RPCGetEntriesCommand returns session entries, optionally after the supplied id.
// Upstream: rpc-types.ts { type: "get_entries"; since?: string }
type RPCGetEntriesCommand struct {
	ID    string `json:"id,omitempty"`
	Type  string `json:"type"`
	Since string `json:"since,omitempty"`
}

// RPCSetSteeringModeCommand sets the steering queue mode.
type RPCSetSteeringModeCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	Mode string `json:"mode"`
}

// RPCSetFollowUpModeCommand sets the follow-up queue mode.
type RPCSetFollowUpModeCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	Mode string `json:"mode"`
}

// RPCBashResult is the response payload for the bash command.
// Mirrors coding.BashResult.
type RPCBashResult struct {
	Output         string `json:"output"`
	ExitCode       int    `json:"exitCode"`
	Cancelled      bool   `json:"cancelled"`
	Truncated      bool   `json:"truncated"`
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

type RPCBashExecutionUpdate struct {
	Type  string `json:"type"`
	ID    string `json:"id,omitempty"`
	Delta string `json:"delta"`
}

type RPCCompactionResult struct {
	Summary              string         `json:"summary"`
	FirstKeptEntryID     string         `json:"firstKeptEntryId"`
	TokensBefore         int            `json:"tokensBefore"`
	EstimatedTokensAfter int            `json:"estimatedTokensAfter"`
	Usage                map[string]any `json:"usage,omitempty"`
	Details              any            `json:"details,omitempty"`
}

func rpcCompactionResult(result *coding.CompactionResult) RPCCompactionResult {
	var usage map[string]any
	if result.Usage != nil {
		usage = rpcUsage(result.Usage)
	}
	return RPCCompactionResult{
		Summary:              result.Summary,
		FirstKeptEntryID:     result.FirstKeptEntryID,
		TokensBefore:         result.TokensBefore,
		EstimatedTokensAfter: result.EstimatedTokensAfter,
		Usage:                usage,
		Details:              rpcOptionalCompactionDetails(result.Details),
	}
}

func rpcOptionalCompactionDetails(details any) any {
	if value, ok := details.(codingcompaction.CompactionDetails); ok && value.ReadFiles == nil && value.ModifiedFiles == nil {
		return nil
	}
	return details
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func rpcImages(images []RPCImageContent) []ai.ImageContent {
	out := make([]ai.ImageContent, len(images))
	for i, image := range images {
		out[i] = image.imageContent()
	}
	return out
}

// parseRPCCommand parses a JSON line from stdin into a typed command.
// Returns the envelope (with Type and ID) and any parse error.
func parseRPCCommand(line []byte) (RPCCommandEnvelope, error) {
	var env RPCCommandEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return RPCCommandEnvelope{}, err
	}
	env.Raw = append([]byte(nil), line...) // safe copy
	return env, nil
}

// rpcSuccess builds a success response for a command.
func rpcSuccess(id, command string, data any) RPCResponse {
	return RPCResponse{ID: id, Type: "response", Command: command, Success: true, Data: data}
}

func rpcSuccessNull(id, command string) rpcNullResponse {
	return rpcNullResponse{ID: id, Type: "response", Command: command, Success: true, Data: nil}
}

// rpcError builds an error response for a command.
func rpcError(id, command, message string) RPCResponse {
	return RPCResponse{ID: id, Type: "response", Command: command, Success: false, Error: message}
}

func rpcModelValue(model *ai.Model) *RPCModel {
	if model == nil {
		return &RPCModel{
			ID:       "unknown",
			Name:     "unknown",
			API:      ai.API("unknown"),
			Provider: "unknown",
			Input:    []string{},
		}
	}
	provider := model.ProviderMeta.ProviderID
	if provider == "" && model.Provider != nil {
		provider = model.Provider.ID()
	}
	input := []string{"text"}
	if model.Capabilities.SupportsImages {
		input = append(input, "image")
	}
	cost := RPCModelCost{
		Input:      model.Capabilities.InputCostPer1M,
		Output:     model.Capabilities.OutputCostPer1M,
		CacheRead:  model.Capabilities.CacheReadCostPer1M,
		CacheWrite: model.Capabilities.CacheWriteCostPer1M,
	}
	if len(model.Capabilities.CostTiers) > 0 {
		cost.Tiers = make([]RPCModelCostTier, len(model.Capabilities.CostTiers))
		for i, tier := range model.Capabilities.CostTiers {
			cost.Tiers[i] = RPCModelCostTier{
				InputTokensAbove: tier.InputTokensAbove,
				Input:            tier.InputCostPer1M,
				Output:           tier.OutputCostPer1M,
				CacheRead:        tier.CacheReadCostPer1M,
				CacheWrite:       tier.CacheWriteCostPer1M,
			}
		}
	}
	return &RPCModel{
		ID:               model.ID,
		Name:             model.DisplayName,
		API:              model.ProviderMeta.API,
		Provider:         provider,
		BaseURL:          model.ProviderMeta.BaseURL,
		Reasoning:        model.ProviderMeta.Reasoning || model.Capabilities.MaxThinking != "",
		ThinkingLevelMap: model.ThinkingLevelMap,
		Input:            input,
		Cost:             cost,
		PromptCache:      model.PromptCache,
		ContextWindow:    model.Capabilities.ContextWindow,
		MaxTokens:        model.Capabilities.MaxOutputTokens,
		SamplingParams:   model.SamplingParams,
		Headers:          model.ProviderMeta.Headers,
		Compat:           model.ProviderMeta.Compat,
		InputLimits:      model.InputLimits.Clone(),
	}
}

// RPCExtensionErrorEvent reports an extension error from the runner's error
// listener. Mirrors the object upstream rpc-mode.ts outputs from onError; the
// stack is not part of it.
type RPCExtensionErrorEvent struct {
	Type          string `json:"type"` // "extension_error"
	ExtensionPath string `json:"extensionPath"`
	Event         string `json:"event"`
	Error         string `json:"error"`
}

func rpcExtensionErrorEvent(err *extension.ExtensionError) RPCExtensionErrorEvent {
	return RPCExtensionErrorEvent{Type: "extension_error", ExtensionPath: err.ExtensionPath, Event: err.Event, Error: err.Error}
}

// RPCEntryAppendedEvent reports an extension-defined Session entry immediately
// after it is persisted.
type RPCEntryAppendedEvent struct {
	Type  string                `json:"type"`
	Entry RPCEntryAppendedEntry `json:"entry"`
}

// RPCEntryAppendedEntry keeps the property order produced by Pi's
// appendCustomEntry object construction.
type RPCEntryAppendedEntry struct {
	Type       string  `json:"type"`
	CustomType string  `json:"customType"`
	Data       any     `json:"data,omitempty"`
	ID         string  `json:"id"`
	ParentID   *string `json:"parentId"`
	Timestamp  string  `json:"timestamp"`
}

// RPCSessionInfoChangedEvent reports the effective Session name. Name is
// omitted when an extension clears the name with an empty value.
type RPCSessionInfoChangedEvent struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// rpcSessionInfoChanged constructs the session_info_changed event.
func rpcSessionInfoChanged(name string) RPCSessionInfoChangedEvent {
	return RPCSessionInfoChangedEvent{Type: "session_info_changed", Name: name}
}

// writeJSONLine serialises v to a JSON line (no HTML escaping) and writes
// it followed by a newline. A broken writer is silently ignored because a
// dead stdout means the client has gone away.
//
// Uses json.NewEncoder + SetEscapeHTML(false) per AGENTS.md rule: never use
// json.Marshal for strings that appear in display/wire output containing
// shell operators (&&, <, >, etc.).
func writeJSONLine(w io.Writer, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return // encoding should never fail for our well-typed structs
	}
	_, _ = w.Write(buf.Bytes()) // Encode already appends '\n'
}
