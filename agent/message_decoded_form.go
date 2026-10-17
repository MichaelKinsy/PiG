package agent

import (
	"encoding/json"
	"math"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// pig additive (D112): DecodedForm is the message that decoding m's JSON encoding yields, built without encoding or decoding.

// maxExactInteger is the largest magnitude a JavaScript number holds as an integer; decoding reads an integer member through a float64, so a larger one would not read back as written.
const maxExactInteger = 1<<53 - 1

// DecodedForm returns the message that json.Unmarshal of m's encoding yields, and true when that can be proven from m alone. It reports false for a message it cannot prove round-trips, and the caller decodes the encoding: a system or custom message, one that keeps the source it was decoded from, a streaming assistant, streaming scratch on a block, a tool call whose arguments are not an object of plain JSON values, a number that is not finite or is negative zero or beyond 2^53, text that is not valid UTF-8 (a lone surrogate included), diagnostics, a deferred handle, nested calls, or tool result details other than null, a scalar, or canonical raw JSON.
func (m AgentMessage) DecodedForm() (AgentMessage, bool) {
	if m.source != nil {
		return AgentMessage{}, false
	}
	switch {
	case m.User != nil && m.Assistant == nil && m.ToolResult == nil && m.System == nil && m.Custom == nil:
		return decodedUser(m.User)
	case m.Assistant != nil && m.User == nil && m.ToolResult == nil && m.System == nil && m.Custom == nil:
		return decodedAssistant(m.Assistant)
	case m.ToolResult != nil && m.User == nil && m.Assistant == nil && m.System == nil && m.Custom == nil:
		return decodedToolResult(m.ToolResult)
	}
	return AgentMessage{}, false
}

func validStrings(values ...string) bool {
	for _, value := range values {
		if !utf8.ValidString(value) {
			return false
		}
	}
	return true
}

func exactInt(n int64) bool { return n >= -maxExactInteger && n <= maxExactInteger }

func exactOptional(n *int64) bool { return n == nil || exactInt(*n) }

func decodedUser(m *UserMessage) (AgentMessage, bool) {
	if m.source != nil || !exactInt(m.Timestamp) {
		return AgentMessage{}, false
	}
	var content ai.UserContent
	switch value := m.Content.(type) {
	case ai.UserText:
		if !utf8.ValidString(string(value)) {
			return AgentMessage{}, false
		}
		content = value
	case ai.UserContentBlocks:
		content, ok := decodedUserBlocks(value)
		if !ok {
			return AgentMessage{}, false
		}
		return AgentMessage{User: &UserMessage{Role: RoleUser, Content: content, Timestamp: m.Timestamp}}, true
	case nil:
		content = ai.UserContentBlocks{}
	default:
		return AgentMessage{}, false
	}
	return AgentMessage{User: &UserMessage{Role: RoleUser, Content: content, Timestamp: m.Timestamp}}, true
}

func decodedUserBlocks(value ai.UserContentBlocks) (ai.UserContent, bool) {
	{
		blocks := make(ai.UserContentBlocks, len(value))
		for i, block := range value {
			form, ok := ai.DecodedFormBlock(block)
			if !ok {
				return nil, false
			}
			blocks[i], ok = form.(ai.UserContentBlock)
			if !ok {
				return nil, false
			}
		}
		return blocks, true
	}
}

func decodedAssistant(m *AssistantMessage) (AgentMessage, bool) {
	if m.source != nil || m.streamView != nil || m.Thinking != "" || m.ThinkingSignature != "" || len(m.Diagnostics) > 0 || m.Deferred != nil {
		return AgentMessage{}, false
	}
	if !exactInt(m.Timestamp) || !exactOptional(m.DurationMs) {
		return AgentMessage{}, false
	}
	if !validStrings(string(m.API), m.Provider, m.ModelID, m.ResponseModel, m.ResponseID, m.ProviderThinkingLevel, string(m.StopReason), m.ErrorMessage, m.RawStopReason, string(m.ThinkingLevel)) {
		return AgentMessage{}, false
	}
	usage := ai.Usage{}
	if m.Usage != nil {
		usage = *m.Usage
	}
	if !usageExact(usage) {
		return AgentMessage{}, false
	}
	usage, ok := ai.DecodedFormUsage(usage)
	if !ok {
		return AgentMessage{}, false
	}
	content := make([]ai.AssistantContentBlock, len(m.Content))
	for i, block := range m.Content {
		form, ok := ai.DecodedFormBlock(block)
		if !ok {
			return AgentMessage{}, false
		}
		if content[i], ok = form.(ai.AssistantContentBlock); !ok {
			return AgentMessage{}, false
		}
	}
	decoded := &AssistantMessage{
		Role: RoleAssistant, Content: content, API: m.API, Provider: m.Provider, ModelID: m.ModelID,
		ResponseModel: m.ResponseModel, ResponseID: m.ResponseID, ProviderThinkingLevel: m.ProviderThinkingLevel,
		Usage: &usage, StopReason: m.StopReason, ErrorMessage: m.ErrorMessage,
		ErrorBeforeTimestamp: m.ErrorBeforeTimestamp && m.ErrorMessage != "", RawStopReason: m.RawStopReason,
		Timestamp: m.Timestamp, ThinkingLevel: m.ThinkingLevel,
	}
	if m.EndTurn != nil {
		value := *m.EndTurn
		decoded.EndTurn = &value
	}
	if m.DurationMs != nil {
		value := *m.DurationMs
		decoded.DurationMs = &value
	}
	return AgentMessage{Assistant: decoded}, true
}

func usageExact(usage ai.Usage) bool {
	for _, n := range [...]int{usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite, usage.TotalTokens} {
		if int64(n) < -maxExactInteger || int64(n) > maxExactInteger {
			return false
		}
	}
	for _, p := range [...]*int{usage.CacheWrite1h, usage.Reasoning} {
		if p != nil && (int64(*p) < -maxExactInteger || int64(*p) > maxExactInteger) {
			return false
		}
	}
	return true
}

func decodedToolResult(m *ToolResultMessage) (AgentMessage, bool) {
	if m.source != nil || m.NestedCalls != nil || !exactInt(m.Timestamp) || !exactOptional(m.DurationMs) {
		return AgentMessage{}, false
	}
	if !validStrings(m.ToolCallID, m.ToolName) {
		return AgentMessage{}, false
	}
	decoded := &ToolResultMessage{Role: RoleToolResult, ToolCallID: m.ToolCallID, ToolName: m.ToolName, IsError: m.IsError, Timestamp: m.Timestamp}
	switch details := m.Details.(type) {
	case nil:
		decoded.DetailsNull = m.DetailsNull
	case bool:
		decoded.Details = details
	case string:
		if !utf8.ValidString(details) {
			return AgentMessage{}, false
		}
		decoded.Details = details
	case float64:
		if math.IsNaN(details) || math.IsInf(details, 0) || (details == 0 && math.Signbit(details)) {
			return AgentMessage{}, false
		}
		decoded.Details = details
	case json.RawMessage:
		// A container decodes to its canonical text; raw text that is already canonical is that value. A scalar spelling decodes as a number or string, so it is not proven here.
		if len(details) == 0 || (details[0] != '{' && details[0] != '[') || !jsonstringify.IsCanonical(details) {
			return AgentMessage{}, false
		}
		decoded.Details = details
	default:
		return AgentMessage{}, false
	}
	if m.Usage != nil {
		if !usageExact(*m.Usage) {
			return AgentMessage{}, false
		}
		usage, ok := ai.DecodedFormUsage(*m.Usage)
		if !ok {
			return AgentMessage{}, false
		}
		decoded.Usage = &usage
	}
	if m.DurationMs != nil {
		value := *m.DurationMs
		decoded.DurationMs = &value
	}
	decoded.Content = make([]ai.ToolResultMessageContent, len(m.Content))
	for i, block := range m.Content {
		form, ok := ai.DecodedFormBlock(block)
		if !ok {
			return AgentMessage{}, false
		}
		if decoded.Content[i], ok = form.(ai.ToolResultMessageContent); !ok {
			return AgentMessage{}, false
		}
	}
	return AgentMessage{ToolResult: decoded}, true
}
