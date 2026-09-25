package ai

import (
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
	ToolsAdded   []ToolSchema    `json:"toolsAdded,omitempty"`
	ToolsRemoved []ToolReference `json:"toolsRemoved,omitempty"`
	Timestamp    int64           `json:"timestamp"`
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

// AssistantMessage is the authoritative complete or partial model response.
type AssistantMessage struct {
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
}

func (AssistantMessage) messageRole() string { return "assistant" }
func (message AssistantMessage) cloneMessage() Message {
	message.Content = cloneAssistantContent(message.Content)
	message.Diagnostics = cloneDiagnostics(message.Diagnostics)
	message.Deferred = cloneDeferredHandle(message.Deferred)
	if message.EndTurn != nil {
		message.EndTurn = new(*message.EndTurn)
	}
	return message
}

// ToolResultMessage carries one tool execution result.
type ToolResultMessage struct {
	ToolCallID string                     `json:"toolCallId"`
	ToolName   string                     `json:"toolName"`
	Content    []ToolResultMessageContent `json:"content"`
	Details    any                        `json:"details,omitempty"`
	Usage      *Usage                     `json:"usage,omitempty"`
	IsError    bool                       `json:"isError"`
	Timestamp  int64                      `json:"timestamp"`
}

func (ToolResultMessage) messageRole() string { return "toolResult" }
func (message ToolResultMessage) cloneMessage() Message {
	message.Content = cloneToolResultMessageContent(message.Content)
	message.Details = cloneJSONValue(message.Details)
	if message.Usage != nil {
		message.Usage = new(*message.Usage)
	}
	return message
}

func marshalMessage(role string, value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	fields["role"] = json.RawMessage(fmt.Sprintf("%q", role))
	return json.Marshal(fields)
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
			value.Arguments = JsonObject(cloneJSONValue(value.Arguments).(map[string]any))
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
