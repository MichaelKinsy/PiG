// Ports packages/agent/src/types.ts.
package agent

import (
	"fmt"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Clone copies the message envelope and mutable top-level slices/maps. A streaming assistant becomes an independently owned current observation.
func (m AgentMessage) Clone() AgentMessage {
	clone := AgentMessage{}
	switch {
	case m.System != nil:
		clone.System = ai.GetInitialSystemMessage([]ai.Message{*m.System})
	case m.User != nil:
		value := *m.User
		if blocks, ok := m.User.Content.(ai.UserContentBlocks); ok {
			value.Content = slices.Clone(blocks)
		}
		clone.User = &value
	case m.Assistant != nil:
		value := *m.Assistant.Observe()
		clone.Assistant = &value
	case m.ToolResult != nil:
		value := *m.ToolResult
		value.Content = slices.Clone(m.ToolResult.Content)
		if m.ToolResult.Usage != nil {
			usage := *m.ToolResult.Usage
			value.Usage = &usage
		}
		clone.ToolResult = &value
	case m.Custom != nil:
		clone.Custom = maps.Clone(m.Custom)
	}
	return clone
}

// MarshalJSON observes retained streaming messages at the serialization boundary.
func (m AssistantMessage) MarshalJSON() ([]byte, error) {
	type plain AssistantMessage
	return json.Marshal((*plain)(m.Observe()))
}

// MarshalJSON emits the upstream flat role-discriminated AgentMessage union. User strings and text blocks retain their JavaScript UTF-16 units.
func (m AgentMessage) MarshalJSON() ([]byte, error) {
	variants := 0
	if m.System != nil {
		variants++
	}
	if m.User != nil {
		variants++
	}
	if m.Assistant != nil {
		variants++
	}
	if m.ToolResult != nil {
		variants++
	}
	if m.Custom != nil {
		variants++
	}
	if variants != 1 {
		return nil, fmt.Errorf("AgentMessage must contain exactly one variant, got %d", variants)
	}

	switch {
	case m.System != nil:
		return json.Marshal(m.System)
	case m.User != nil:
		var content any
		switch value := m.User.Content.(type) {
		case ai.UserText:
			content = value
		case ai.UserContentBlocks:
			encoded, err := marshalAgentContent(value)
			if err != nil {
				return nil, err
			}
			content = encoded
		case nil:
			content = []any{}
		}
		return json.Marshal(struct {
			Role      string `json:"role"`
			Content   any    `json:"content"`
			Timestamp int64  `json:"timestamp"`
		}{Role: RoleUser, Content: content, Timestamp: m.User.Timestamp})
	case m.Assistant != nil:
		message := m.Assistant.Observe()
		content, err := marshalAgentContent(message.Content)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Role                  string                          `json:"role"`
			Content               []any                           `json:"content"`
			API                   ai.API                          `json:"api"`
			Provider              string                          `json:"provider"`
			Model                 string                          `json:"model"`
			ResponseModel         string                          `json:"responseModel,omitempty"`
			ResponseID            string                          `json:"responseId,omitempty"`
			ProviderThinkingLevel string                          `json:"providerThinkingLevel,omitempty"`
			Diagnostics           []ai.AssistantMessageDiagnostic `json:"diagnostics,omitempty"`
			Usage                 usageWire                       `json:"usage"`
			StopReason            ai.StopReason                   `json:"stopReason"`
			Deferred              *ai.DeferredHandle              `json:"deferred,omitempty"`
			ErrorMessage          string                          `json:"errorMessage,omitempty"`
			RawStopReason         string                          `json:"rawStopReason,omitempty"`
			EndTurn               *bool                           `json:"endTurn,omitempty"`
			Timestamp             int64                           `json:"timestamp"`
			// thinkingLevel is assigned to the finished response after the provider built it, so it follows the provider's own keys.
			ThinkingLevel ai.ModelThinkingLevel `json:"thinkingLevel,omitempty"`
		}{
			Role: RoleAssistant, Content: content, API: message.API,
			Provider: message.Provider, Model: message.ModelID,
			ResponseModel: message.ResponseModel, ResponseID: message.ResponseID,
			ProviderThinkingLevel: message.ProviderThinkingLevel,
			Diagnostics:           message.Diagnostics, Usage: usageToWire(message.Usage),
			StopReason: message.StopReason, Deferred: message.Deferred,
			ErrorMessage: message.ErrorMessage, RawStopReason: message.RawStopReason,
			EndTurn: message.EndTurn, Timestamp: message.Timestamp, ThinkingLevel: message.ThinkingLevel,
		})
	case m.ToolResult != nil:
		content, err := marshalAgentContent(m.ToolResult.Content)
		if err != nil {
			return nil, err
		}
		message := m.ToolResult
		return json.Marshal(struct {
			Role       string     `json:"role"`
			ToolCallID string     `json:"toolCallId"`
			ToolName   string     `json:"toolName"`
			Content    []any      `json:"content"`
			Details    any        `json:"details,omitempty"`
			Usage      *usageWire `json:"usage,omitempty"`
			IsError    bool       `json:"isError"`
			Timestamp  int64      `json:"timestamp"`
			// nestedCalls follows timestamp, where upstream's agent session sets it on the message after creating it (agent-session.ts:1065-1066).
			NestedCalls *ai.NestedToolCalls `json:"nestedCalls,omitempty"`
		}{
			Role: RoleToolResult, ToolCallID: message.ToolCallID, ToolName: message.ToolName,
			Content: content, Details: message.Details, Usage: optionalUsageToWire(message.Usage),
			IsError: message.IsError, Timestamp: message.Timestamp, NestedCalls: message.NestedCalls,
		})
	default:
		role, ok := m.Custom["role"].(string)
		if !ok || role == "" {
			return nil, fmt.Errorf("custom AgentMessage requires a non-empty string role")
		}
		return marshalCustomMessage(role, m.Custom)
	}
}

// customMessageMemberOrder is the order Pi builds the members of each custom role's object in, which JSON.stringify keeps. A member outside the list follows, sorted; a role outside the table is sorted entirely. Content blocks are written in Pi's member order too (packages/ai/src/types.ts TextContent, ImageContent).
// upstream: coding-agent/src/core/messages.ts:29-67 (shapes), 100-137 (createBranchSummaryMessage, createCompactionSummaryMessage, createCustomMessage), agent-session.ts:3781-3792 (recordBashResult)
var customMessageMemberOrder = map[string][]string{
	RoleCustom:            {"role", "customType", "content", "display", "details", "timestamp"},
	"bashExecution":       {"role", "command", "output", "exitCode", "cancelled", "truncated", "fullOutputPath", "timestamp", "excludeFromContext"},
	RoleBranchSummary:     {"role", "summary", "fromId", "timestamp"},
	RoleCompactionSummary: {"role", "summary", "tokensBefore", "timestamp"},
}

// contentBlockMemberOrder is the order Pi's tools and extensions write a TextContent or ImageContent in (packages/ai/src/types.ts).
var contentBlockMemberOrder = []string{"type", "text", "textSignature", "data", "mimeType"}

func marshalCustomMessage(role string, members map[string]any) ([]byte, error) {
	if blocks, ok := members["content"].([]any); ok {
		ordered, err := orderedjson.MarshalArray(blocks, contentBlockMemberOrder...)
		if err != nil {
			return nil, err
		}
		members = maps.Clone(members)
		members["content"] = json.RawMessage(ordered)
	}
	return orderedjson.MarshalMap(members, customMessageMemberOrder[role]...)
}

// UnmarshalJSON decodes the upstream flat role-discriminated AgentMessage union and retains lone UTF-16 units in user content as WTF-8.
func (m *AgentMessage) UnmarshalJSON(data []byte) error {
	var wire agentMessageWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("AgentMessage: %w", err)
	}
	if wire.Role == "" {
		return fmt.Errorf("AgentMessage requires a non-empty string role")
	}

	*m = AgentMessage{}
	switch wire.Role {
	case "system":
		message, err := decodeSystemMessage(data)
		if err != nil {
			return err
		}
		m.System = message
	case RoleUser:
		content, err := unmarshalUserContent(wire.Content)
		if err != nil {
			return err
		}
		m.User = &UserMessage{Role: wire.Role, Content: content, Timestamp: wire.Timestamp}
	case RoleAssistant:
		content, err := unmarshalAssistantContent(wire.Content)
		if err != nil {
			return err
		}
		m.Assistant = &AssistantMessage{
			Role: wire.Role, Content: content, API: wire.API, Provider: wire.Provider, ModelID: wire.Model,
			ResponseModel: wire.ResponseModel, ResponseID: wire.ResponseID,
			ProviderThinkingLevel: wire.ProviderThinkingLevel, Diagnostics: wire.Diagnostics,
			Usage: wire.Usage.toAI(), StopReason: wire.StopReason, Deferred: wire.Deferred,
			ErrorMessage: wire.ErrorMessage, RawStopReason: wire.RawStopReason,
			EndTurn: wire.EndTurn, Timestamp: wire.Timestamp, ThinkingLevel: wire.ThinkingLevel,
		}
	case RoleToolResult:
		content, err := unmarshalToolResultContent(wire.Content)
		if err != nil {
			return err
		}
		m.ToolResult = &ToolResultMessage{
			Role: wire.Role, ToolCallID: wire.ToolCallID, ToolName: wire.ToolName, Content: content,
			Details: orderedjson.Value(wire.Details), Usage: wire.Usage.toAI(), NestedCalls: wire.NestedCalls,
			IsError: wire.IsError, Timestamp: wire.Timestamp,
		}
	default:
		var custom map[string]any
		if err := json.Unmarshal(data, &custom); err != nil {
			return err
		}
		// `details` is the opaque value an extension wrote; keep its member order.
		if _, present := custom["details"]; present {
			custom["details"] = orderedjson.Value(wire.Details)
		}
		m.Custom = custom
	}
	return nil
}

type agentMessageWire struct {
	Role                  string                          `json:"role"`
	Content               json.RawMessage                 `json:"content"`
	Timestamp             int64                           `json:"timestamp"`
	API                   ai.API                          `json:"api"`
	Provider              string                          `json:"provider"`
	Model                 string                          `json:"model"`
	ResponseModel         string                          `json:"responseModel"`
	ResponseID            string                          `json:"responseId"`
	ProviderThinkingLevel string                          `json:"providerThinkingLevel"`
	ThinkingLevel         ai.ModelThinkingLevel           `json:"thinkingLevel"`
	Diagnostics           []ai.AssistantMessageDiagnostic `json:"diagnostics"`
	Usage                 *usageWire                      `json:"usage"`
	StopReason            ai.StopReason                   `json:"stopReason"`
	Deferred              *ai.DeferredHandle              `json:"deferred"`
	ErrorMessage          string                          `json:"errorMessage"`
	RawStopReason         string                          `json:"rawStopReason"`
	EndTurn               *bool                           `json:"endTurn"`
	ToolCallID            string                          `json:"toolCallId"`
	ToolName              string                          `json:"toolName"`
	Details               json.RawMessage                 `json:"details"`
	NestedCalls           *ai.NestedToolCalls             `json:"nestedCalls"`
	IsError               bool                            `json:"isError"`
}

type usageWire struct {
	Input        int          `json:"input"`
	Output       int          `json:"output"`
	CacheRead    int          `json:"cacheRead"`
	CacheWrite   int          `json:"cacheWrite"`
	CacheWrite1h *int         `json:"cacheWrite1h,omitempty"`
	Reasoning    *int         `json:"reasoning,omitempty"`
	TotalTokens  int          `json:"totalTokens"`
	Cost         ai.UsageCost `json:"cost"`
}

func usageToWire(usage *ai.Usage) usageWire {
	if usage == nil {
		return usageWire{Cost: ai.UsageCost{}}
	}
	return usageWire{
		Input: usage.Input, Output: usage.Output, CacheRead: usage.CacheRead,
		CacheWrite: usage.CacheWrite, CacheWrite1h: usage.CacheWrite1h,
		Reasoning: usage.Reasoning, TotalTokens: usage.TotalTokens, Cost: usage.Cost,
	}
}

func optionalUsageToWire(usage *ai.Usage) *usageWire {
	if usage == nil {
		return nil
	}
	wire := usageToWire(usage)
	return &wire
}

func (wire *usageWire) toAI() *ai.Usage {
	if wire == nil {
		return nil
	}
	return &ai.Usage{
		Input: wire.Input, Output: wire.Output, CacheRead: wire.CacheRead,
		CacheWrite: wire.CacheWrite, CacheWrite1h: wire.CacheWrite1h,
		Reasoning: wire.Reasoning, TotalTokens: wire.TotalTokens, Cost: wire.Cost,
	}
}

func marshalAgentContent[T any](content []T) ([]any, error) {
	items := make([]any, 0, len(content))
	for i, item := range content {
		block, ok := any(item).(ai.ContentBlock)
		if !ok {
			return nil, fmt.Errorf("AgentMessage content[%d]: unsupported content block %T", i, item)
		}
		encoded, err := marshalAgentContentBlock(block)
		if err != nil {
			return nil, fmt.Errorf("AgentMessage content[%d]: %w", i, err)
		}
		items = append(items, encoded)
	}
	return items, nil
}

func marshalAgentContentBlock(block ai.ContentBlock) (any, error) {
	switch value := block.(type) {
	case ai.TextContent:
		return value, nil
	case ai.ImageContent:
		return struct {
			Type     string `json:"type"`
			Data     string `json:"data"`
			MIMEType string `json:"mimeType"`
		}{Type: "image", Data: value.Data, MIMEType: value.MimeType}, nil
	case ai.ThinkingContent:
		return value, nil
	case ai.ToolCall:
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported content block %T", block)
	}
}

func unmarshalUserContent(data []byte) (ai.UserContent, error) {
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return nil, err
		}
		return ai.UserText(text), nil
	}
	blocks, err := unmarshalAgentContent(data)
	if err != nil {
		return nil, err
	}
	content := make(ai.UserContentBlocks, len(blocks))
	for i, block := range blocks {
		value, ok := block.(ai.UserContentBlock)
		if !ok {
			return nil, fmt.Errorf("AgentMessage user content[%d]: unsupported content block %T", i, block)
		}
		content[i] = value
	}
	return content, nil
}

func unmarshalAssistantContent(data []byte) ([]ai.AssistantContentBlock, error) {
	blocks, err := unmarshalAgentContent(data)
	if err != nil {
		return nil, err
	}
	content := make([]ai.AssistantContentBlock, len(blocks))
	for i, block := range blocks {
		value, ok := block.(ai.AssistantContentBlock)
		if !ok {
			return nil, fmt.Errorf("AgentMessage assistant content[%d]: unsupported content block %T", i, block)
		}
		content[i] = value
	}
	return content, nil
}

func unmarshalToolResultContent(data []byte) ([]ai.ToolResultMessageContent, error) {
	blocks, err := unmarshalAgentContent(data)
	if err != nil {
		return nil, err
	}
	content := make([]ai.ToolResultMessageContent, len(blocks))
	for i, block := range blocks {
		value, ok := block.(ai.ToolResultMessageContent)
		if !ok {
			return nil, fmt.Errorf("AgentMessage tool result content[%d]: unsupported content block %T", i, block)
		}
		content[i] = value
	}
	return content, nil
}

func unmarshalAgentContent(data []byte) ([]ai.ContentBlock, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	if data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return nil, err
		}
		return []ai.ContentBlock{ai.TextContent{Text: text}}, nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("AgentMessage content: %w", err)
	}
	content := make([]ai.ContentBlock, 0, len(raws))
	for i, raw := range raws {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("AgentMessage content[%d]: %w", i, err)
		}
		var block ai.ContentBlock
		switch probe.Type {
		case "text":
			var value ai.TextContent
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			block = value
		case "image":
			var value struct {
				Data     string `json:"data"`
				MIMEType string `json:"mimeType"`
			}
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			block = ai.ImageContent{Data: value.Data, MimeType: value.MIMEType}
		case "thinking":
			var value ai.ThinkingContent
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			block = value
		case "toolCall":
			var value ai.ToolCall
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			block = value
		case "tool_use":
			// Persisted PiG sessions before the closed transcript contract used
			// Anthropic's provider spelling. Decode it at the persistence boundary.
			var value struct {
				ID               string        `json:"id"`
				Name             string        `json:"name"`
				Input            ai.JsonObject `json:"input"`
				ThoughtSignature string        `json:"thoughtSignature"`
			}
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			block = ai.ToolCall{ID: value.ID, Name: value.Name, Arguments: value.Input, ThoughtSignature: value.ThoughtSignature}
		case "tool_result":
			// Persisted tool-result wrapper blocks are reduced to the closed
			// ToolResultMessage content union; message fields carry call identity.
			var value struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			block = ai.TextContent{Text: value.Content}
		default:
			value, err := ai.UnmarshalContentBlock(raw)
			if err != nil {
				return nil, fmt.Errorf("AgentMessage content[%d]: %w", i, err)
			}
			block = value
		}
		content = append(content, block)
	}
	return content, nil
}
