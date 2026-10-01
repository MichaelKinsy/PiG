package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Message is the closed provider-facing transcript union.
type Message interface {
	messageRole() string
	cloneMessage() Message
}

// SystemContent is the closed content union for system messages.
type SystemContent interface {
	isSystemContent()
	cloneSystemContent() SystemContent
}

// SystemText carries system instruction text.
type SystemText string

func (SystemText) isSystemContent()                       {}
func (text SystemText) cloneSystemContent() SystemContent { return text }

// SystemTextBlocks carries system instruction text blocks.
type SystemTextBlocks []TextContent

func (SystemTextBlocks) isSystemContent() {}
func (blocks SystemTextBlocks) cloneSystemContent() SystemContent {
	return append(SystemTextBlocks(nil), blocks...)
}

// UserContent is the closed content union for user messages.
type UserContent interface {
	isUserContent()
	cloneUserContent() UserContent
}

// UserText carries a plain user message.
type UserText string

func (UserText) isUserContent()                     {}
func (text UserText) cloneUserContent() UserContent { return text }

// UserContentBlock is valid inside a user message.
type UserContentBlock interface {
	ContentBlock
	isUserContentBlock()
}

func (TextContent) isUserContentBlock()  {}
func (ImageContent) isUserContentBlock() {}

// UserContentBlocks carries text and image user content.
type UserContentBlocks []UserContentBlock

func (UserContentBlocks) isUserContent() {}
func (blocks UserContentBlocks) cloneUserContent() UserContent {
	out := make(UserContentBlocks, len(blocks))
	for i, block := range blocks {
		out[i] = cloneUserContentBlock(block)
	}
	return out
}

// AssistantContentBlock is valid inside an assistant message.
type AssistantContentBlock interface {
	ContentBlock
	isAssistantContentBlock()
}

func (TextContent) isAssistantContentBlock()     {}
func (ThinkingContent) isAssistantContentBlock() {}
func (ToolCall) isAssistantContentBlock()        {}

// ToolResultMessageContent is valid inside a tool-result message.
type ToolResultMessageContent interface {
	ContentBlock
	isToolResultMessageContent()
}

func (TextContent) isToolResultMessageContent()  {}
func (ImageContent) isToolResultMessageContent() {}

// SystemMessage changes instructions and tool state at one transcript position.
type SystemMessage struct {
	Content      SystemContent   `json:"content"`
	Sections     OrderedSections `json:"sections,omitempty"`
	Timestamp    int64           `json:"timestamp"`
	ToolsAdded   []ToolSchema    `json:"toolsAdded,omitempty"`
	ToolsRemoved []ToolReference `json:"toolsRemoved,omitempty"`
}

func (SystemMessage) messageRole() string { return "system" }
func (message SystemMessage) cloneMessage() Message {
	message.Content = cloneSystemContent(message.Content)
	message.Sections = cloneSections(message.Sections)
	message.ToolsAdded = cloneTools(message.ToolsAdded)
	message.ToolsRemoved = append([]ToolReference(nil), message.ToolsRemoved...)
	return message
}

// UserMessage is one user turn.
type UserMessage struct {
	Content   UserContent `json:"content"`
	Timestamp int64       `json:"timestamp"`
}

func (UserMessage) messageRole() string { return "user" }
func (message UserMessage) cloneMessage() Message {
	message.Content = cloneUserContent(message.Content)
	return message
}

// AssistantMessage is a complete or partial model response. A stream event delivers a partial whose exported fields equal the stream state at delivery; they do not change behind readers. Observe and RefreshEvent read the current state of a partial retained past its delivery.
type AssistantMessage struct {
	observation           *assistantMessageObservation
	Content               []AssistantContentBlock      `json:"content"`
	API                   API                          `json:"api"`
	Provider              string                       `json:"provider"`
	Model                 string                       `json:"model"`
	ResponseModel         string                       `json:"responseModel,omitempty"`
	ResponseID            string                       `json:"responseId,omitempty"`
	ProviderThinkingLevel string                       `json:"providerThinkingLevel,omitempty"`
	Diagnostics           []AssistantMessageDiagnostic `json:"diagnostics,omitempty"`
	Usage                 Usage                        `json:"usage"`
	StopReason            StopReason                   `json:"stopReason"`
	Deferred              *DeferredHandle              `json:"deferred,omitempty"`
	ErrorMessage          string                       `json:"errorMessage,omitempty"`
	RawStopReason         string                       `json:"rawStopReason,omitempty"`
	EndTurn               *bool                        `json:"endTurn,omitempty"`
	Timestamp             int64                        `json:"timestamp"`
	// ThinkingLevel is the thinking level the agent loop requested for this response. Absent outside the agent loop and for legacy responses. Mirrors upstream AssistantMessage.thinkingLevel (packages/ai/src/types.ts:557).
	ThinkingLevel ModelThinkingLevel `json:"thinkingLevel,omitempty"`
}

func (AssistantMessage) messageRole() string { return "assistant" }
func (message AssistantMessage) cloneMessage() Message {
	return *message.Observe()
}

func cloneAssistantMessage(message AssistantMessage) AssistantMessage {
	message.observation = nil
	message.Content = cloneAssistantContent(message.Content)
	message.Usage = cloneUsage(message.Usage)
	message.Diagnostics = cloneDiagnostics(message.Diagnostics)
	message.Deferred = cloneDeferredHandle(message.Deferred)
	if message.EndTurn != nil {
		message.EndTurn = new(*message.EndTurn)
	}
	return message
}

// NestedToolCallStatus is the state of a [NestedToolCallRecord]. `unfinished` means the call was still running when the calling tool finished.
//
// upstream: .upstream/v0.99.1/packages/ai/src/types.ts:573-586 (NestedToolCallRecord.status)
type NestedToolCallStatus string

// The statuses of upstream's NestedToolCallRecord.status union.
const (
	NestedToolCallOK         NestedToolCallStatus = "ok"
	NestedToolCallError      NestedToolCallStatus = "error"
	NestedToolCallUnfinished NestedToolCallStatus = "unfinished"
)

// NestedToolCallRecord is a tool call that another tool made while it ran, for example from a codemode script. Arguments is nil when it was over the size limits; ArgumentsBytes then gives their size.
//
// upstream: .upstream/v0.99.1/packages/ai/src/types.ts:573-586 (NestedToolCallRecord)
type NestedToolCallRecord struct {
	ID     string               `json:"id"`
	Name   string               `json:"name"`
	Status NestedToolCallStatus `json:"status"`
	// Arguments is omitted when over the size limits.
	Arguments JsonObject `json:"arguments,omitempty"`
	// ArgumentsBytes is the UTF-8 size of the arguments as JSON, set when Arguments is omitted.
	ArgumentsBytes *int   `json:"argumentsBytes,omitempty"`
	DurationMs     *int64 `json:"durationMs,omitempty"`
	// Error is the error text, truncated.
	Error string `json:"error,omitempty"`

	// argumentOrder is the member order of Arguments, as ToolCall keeps it.
	argumentOrder schemaObjectOrder
}

// SetArgumentsJSON decodes a complete JSON object into Arguments and keeps its member order.
func (record *NestedToolCallRecord) SetArgumentsJSON(raw []byte) error {
	call := ToolCall{}
	if err := call.SetArgumentsJSON(raw); err != nil {
		return err
	}
	record.Arguments, record.argumentOrder = call.Arguments, call.argumentOrder
	return nil
}

// MarshalJSON keeps an empty `arguments` object, which differs from omitted arguments (over the size limits): only a nil Arguments is left out.
func (record NestedToolCallRecord) MarshalJSON() ([]byte, error) {
	wire := struct {
		ID             string               `json:"id"`
		Name           string               `json:"name"`
		Status         NestedToolCallStatus `json:"status"`
		Arguments      json.RawMessage      `json:"arguments,omitempty"`
		ArgumentsBytes *int                 `json:"argumentsBytes,omitempty"`
		DurationMs     *int64               `json:"durationMs,omitempty"`
		Error          string               `json:"error,omitempty"`
	}{ID: record.ID, Name: record.Name, Status: record.Status, ArgumentsBytes: record.ArgumentsBytes, DurationMs: record.DurationMs, Error: record.Error}
	if record.Arguments != nil {
		arguments, err := ToolCall{Arguments: record.Arguments, argumentOrder: record.argumentOrder}.ArgumentsJSON()
		if err != nil {
			return nil, err
		}
		wire.Arguments = arguments
	}
	return json.Marshal(wire)
}

// UnmarshalJSON decodes the record and keeps the member order of its arguments.
func (record *NestedToolCallRecord) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	type plain NestedToolCallRecord
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var members struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	if len(members.Arguments) > 0 {
		order, err := readSchemaObjectOrder(members.Arguments)
		if err != nil {
			return err
		}
		decoded.argumentOrder = order
	}
	*record = NestedToolCallRecord(decoded)
	return nil
}

// NestedToolCalls is the bounded record of the nested calls a tool made. Results are not recorded. Complete is false when calls were dropped, arguments omitted, or calls had not finished.
//
// upstream: .upstream/v0.99.1/packages/ai/src/types.ts:588-592 (NestedToolCalls)
type NestedToolCalls struct {
	Calls    []NestedToolCallRecord `json:"calls"`
	Complete bool                   `json:"complete"`
}

// ToolResultMessage carries one tool execution result.
type ToolResultMessage struct {
	ToolCallID string                     `json:"toolCallId"`
	ToolName   string                     `json:"toolName"`
	Content    []ToolResultMessageContent `json:"content"`
	Details    any                        `json:"details,omitempty"`
	Usage      *Usage                     `json:"usage,omitempty"`
	// NestedCalls are the calls this tool made to other tools. Kept for the session record; not sent to the model.
	// upstream: .upstream/v0.99.1/packages/ai/src/types.ts:604
	NestedCalls *NestedToolCalls `json:"nestedCalls,omitempty"`
	IsError     bool             `json:"isError"`
	Timestamp   int64            `json:"timestamp"`
}

func (ToolResultMessage) messageRole() string { return "toolResult" }
func (message ToolResultMessage) cloneMessage() Message {
	message.Content = cloneToolResultMessageContent(message.Content)
	message.Details = cloneJSONValue(message.Details)
	if message.Usage != nil {
		message.Usage = new(cloneUsage(*message.Usage))
	}
	message.NestedCalls = cloneNestedToolCalls(message.NestedCalls)
	return message
}

func cloneNestedToolCalls(calls *NestedToolCalls) *NestedToolCalls {
	if calls == nil {
		return nil
	}
	cloned := &NestedToolCalls{Calls: make([]NestedToolCallRecord, len(calls.Calls)), Complete: calls.Complete}
	for i, call := range calls.Calls {
		if call.Arguments != nil {
			call.Arguments = cloneJSONValue(map[string]any(call.Arguments)).(map[string]any)
		}
		if call.ArgumentsBytes != nil {
			call.ArgumentsBytes = new(*call.ArgumentsBytes)
		}
		if call.DurationMs != nil {
			call.DurationMs = new(*call.DurationMs)
		}
		cloned.Calls[i] = call
	}
	return cloned
}

func marshalMessage(role string, value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	// Role is inserted before the message fields without decoding nested objects into maps.
	prefix := []byte(fmt.Sprintf(`{"role":%q`, role))
	if len(body) > 2 {
		prefix = append(prefix, ',')
	}
	return append(prefix, body[1:]...), nil
}

func (message SystemMessage) MarshalJSON() ([]byte, error) {
	type plain SystemMessage
	return marshalMessage(message.messageRole(), plain(message))
}

func (message UserMessage) MarshalJSON() ([]byte, error) {
	type plain UserMessage
	return marshalMessage(message.messageRole(), plain(message))
}

func (message AssistantMessage) MarshalJSON() ([]byte, error) {
	type plain AssistantMessage
	if message.observation != nil {
		message = *message.Observe()
	}
	return marshalMessage(message.messageRole(), plain(message))
}

func (message ToolResultMessage) MarshalJSON() ([]byte, error) {
	type plain ToolResultMessage
	return marshalMessage(message.messageRole(), plain(message))
}

func cloneSystemContent(content SystemContent) SystemContent {
	if content == nil {
		return SystemText("")
	}
	return content.cloneSystemContent()
}

func cloneUserContent(content UserContent) UserContent {
	if content == nil {
		return UserText("")
	}
	return content.cloneUserContent()
}

func cloneUserContentBlock(block UserContentBlock) UserContentBlock {
	switch value := block.(type) {
	case TextContent:
		return value
	case ImageContent:
		return value
	default:
		panic(fmt.Sprintf("unsupported user content block %T", block))
	}
}

func cloneAssistantContent(blocks []AssistantContentBlock) []AssistantContentBlock {
	if blocks == nil {
		return nil
	}
	out := make([]AssistantContentBlock, len(blocks))
	for i, block := range blocks {
		switch value := block.(type) {
		case TextContent:
			out[i] = value
		case ThinkingContent:
			out[i] = value
		case ToolCall:
			if value.Arguments != nil {
				value.Arguments = JsonObject(cloneJSONValue(value.Arguments).(map[string]any))
			}
			out[i] = value
		default:
			panic(fmt.Sprintf("unsupported assistant content block %T", block))
		}
	}
	return out
}

func cloneToolResultMessageContent(blocks []ToolResultMessageContent) []ToolResultMessageContent {
	if blocks == nil {
		return nil
	}
	out := make([]ToolResultMessageContent, len(blocks))
	for i, block := range blocks {
		switch value := block.(type) {
		case TextContent:
			out[i] = value
		case ImageContent:
			out[i] = value
		default:
			panic(fmt.Sprintf("unsupported tool result content block %T", block))
		}
	}
	return out
}

func cloneDiagnostics(diagnostics []AssistantMessageDiagnostic) []AssistantMessageDiagnostic {
	if diagnostics == nil {
		return nil
	}
	out := make([]AssistantMessageDiagnostic, len(diagnostics))
	for i, diagnostic := range diagnostics {
		out[i] = diagnostic
		if diagnostic.Error != nil {
			errorInfo := *diagnostic.Error
			errorInfo.Code = cloneJSONValue(errorInfo.Code)
			out[i].Error = &errorInfo
		}
		if diagnostic.Details != nil {
			out[i].Details = cloneJSONValue(diagnostic.Details).(map[string]any)
		}
	}
	return out
}

func cloneDeferredHandle(handle *DeferredHandle) *DeferredHandle {
	if handle == nil {
		return nil
	}
	out := *handle
	if handle.ExpiresAt != nil {
		out.ExpiresAt = new(*handle.ExpiresAt)
	}
	if handle.PollAfterMS != nil {
		out.PollAfterMS = new(*handle.PollAfterMS)
	}
	out.Data = cloneJSONValue(handle.Data)
	return &out
}
