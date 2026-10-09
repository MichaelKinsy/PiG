package ai

import (
	"math"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Context-size estimation for provider transcripts. Mirrors upstream
// packages/ai/src/utils/estimate.ts, which sizes a request by the most recent
// applicable assistant usage plus a chars/3.5 estimate of the messages after it.
// Lengths are JavaScript string lengths (UTF-16 code units).

const (
	charsPerToken       = 3.5
	estimatedImageChars = 4800
)

// ContextUsageEstimate is an estimated context size. LastUsageIndex is the
// index of the message whose usage the estimate starts from, or -1 for
// upstream's null.
type ContextUsageEstimate struct {
	Tokens         int
	UsageTokens    int
	TrailingTokens int
	LastUsageIndex int
}

// CalculateContextTokens returns the context size a usage block reports: the
// provider total, else the sum of its components.
func CalculateContextTokens(usage Usage) int {
	if usage.TotalTokens != 0 {
		return usage.TotalTokens
	}
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}

func ceilDiv(chars int) int {
	return int(math.Ceil(float64(chars) / charsPerToken))
}

// JSONStringifyLength returns the JavaScript length (UTF-16 code units) of
// JSON.stringify(value), or of "[unserializable]" when value cannot be
// encoded (estimate.ts safeJsonStringify). It counts the text JSON.stringify
// writes: no HTML escaping, negative zero as 0, line separators literally.
func JSONStringifyLength(value any) int {
	encoded, err := jsstring.MarshalJSON(value)
	if err != nil {
		return len("[unserializable]")
	}
	text := string(encoded)
	length := jsstring.Length(text)
	// A byte that is not UTF-8 is U+FFFD in a JavaScript string, one unit that JSON.stringify writes raw; the encoder writes it as the six-character escape \ufffd.
	for i := 0; i < len(text); i++ {
		if text[i] != '\\' {
			continue
		}
		if strings.HasPrefix(text[i:], `\ufffd`) {
			length -= len(`\ufffd`) - 1
			i += len(`\ufffd`) - 1
			continue
		}
		i++
	}
	return length
}

// EstimateTextTokens estimates text at 3.5 characters per token.
func EstimateTextTokens(text string) int {
	return ceilDiv(utf16Length(text))
}

// EstimateTextAndImageContentTokens estimates string or block content at 3.5 characters per token, counting each image as 4800 characters (estimate.ts estimateTextAndImageContentTokens).
func EstimateTextAndImageContentTokens(content UserContent) int {
	switch content := content.(type) {
	case UserText:
		return EstimateTextTokens(string(content))
	case UserContentBlocks:
		return ceilDiv(contentBlockChars(content))
	}
	return 0
}

func contentBlockChars[T ContentBlock](blocks []T) int {
	chars := 0
	for _, block := range blocks {
		if text, ok := any(block).(TextContent); ok {
			chars += utf16Length(text.Text)
		} else {
			chars += estimatedImageChars
		}
	}
	return chars
}

// GetSystemMessageText renders a system message as its content followed by its
// non-removed sections (upstream getSystemMessageText).
func GetSystemMessageText(message SystemMessage) string {
	var parts []string
	switch content := message.Content.(type) {
	case SystemText:
		parts = append(parts, string(content))
	case SystemTextBlocks:
		texts := make([]string, len(content))
		for i, block := range content {
			texts[i] = block.Text
		}
		parts = append(parts, strings.Join(texts, "\n"))
	}
	for _, section := range message.Sections {
		if section.Value != nil {
			parts = append(parts, *section.Value)
		}
	}
	nonEmpty := parts[:0]
	for _, part := range parts {
		if part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, "\n\n")
}

func estimateToolsTokens[T any](tools []T) int {
	if len(tools) == 0 {
		return 0
	}
	return ceilDiv(JSONStringifyLength(tools))
}

// EstimateMessageTokens estimates one transcript message: system text with
// its tool changes, text and image content, or assistant text, thinking, and
// tool calls.
func EstimateMessageTokens(message Message) int {
	switch message := message.(type) {
	case SystemMessage:
		return EstimateTextTokens(GetSystemMessageText(message)) + estimateToolsTokens(message.ToolsAdded) + estimateToolsTokens(message.ToolsRemoved)
	case UserMessage:
		return EstimateTextAndImageContentTokens(message.Content)
	case ToolResultMessage:
		return ceilDiv(contentBlockChars(message.Content))
	case AssistantMessage:
		chars := 0
		for _, block := range message.Content {
			switch block := block.(type) {
			case TextContent:
				chars += utf16Length(block.Text)
			case ThinkingContent:
				chars += utf16Length(block.Thinking)
			case ToolCall:
				chars += utf16Length(block.Name) + JSONStringifyLength(block.Arguments)
			}
		}
		return ceilDiv(chars)
	}
	return 0
}

func messageTimestamp(message Message) int64 {
	switch message := message.(type) {
	case SystemMessage:
		return message.Timestamp
	case UserMessage:
		return message.Timestamp
	case AssistantMessage:
		return message.Timestamp
	case ToolResultMessage:
		return message.Timestamp
	}
	return 0
}

// lastAssistantUsageIndex returns the index of the last assistant message
// whose usage still describes its prefix, or -1. A response is skipped when it
// was aborted or errored, reports no usage, or is older than a message before
// it, as when a compaction summary is inserted ahead of retained history.
func lastAssistantUsageIndex(messages []Message) int {
	index := -1
	latestPrefixTimestamp := int64(math.MinInt64)
	for i, message := range messages {
		if assistant, ok := message.(AssistantMessage); ok &&
			assistant.Timestamp >= latestPrefixTimestamp &&
			assistant.StopReason != StopReasonAborted && assistant.StopReason != StopReasonError &&
			CalculateContextTokens(assistant.Usage) > 0 {
			index = i
		}
		latestPrefixTimestamp = max(latestPrefixTimestamp, messageTimestamp(message))
	}
	return index
}

// ContextTokensSource is the input of EstimateContextTokens: a transcript or its messages (estimate.ts `TranscriptContext | readonly Message[]`).
type ContextTokensSource interface {
	[]Message | TranscriptContext
}

// EstimateContextTokens estimates a transcript's context size from the last
// applicable assistant usage plus the messages after it, or from every
// message when no usage applies.
func EstimateContextTokens[T ContextTokensSource](context T) ContextUsageEstimate {
	var messages []Message
	switch source := any(context).(type) {
	case TranscriptContext:
		messages = source.Messages()
	case []Message:
		messages = source
	}
	if index := lastAssistantUsageIndex(messages); index >= 0 {
		usageTokens := CalculateContextTokens(messages[index].(AssistantMessage).Usage)
		trailing := 0
		for _, message := range messages[index+1:] {
			trailing += EstimateMessageTokens(message)
		}
		return ContextUsageEstimate{Tokens: usageTokens + trailing, UsageTokens: usageTokens, TrailingTokens: trailing, LastUsageIndex: index}
	}
	tokens := 0
	for _, message := range messages {
		tokens += EstimateMessageTokens(message)
	}
	return ContextUsageEstimate{Tokens: tokens, TrailingTokens: tokens, LastUsageIndex: -1}
}
