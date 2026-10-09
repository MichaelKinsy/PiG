package ai

import (
	"math"
	"reflect"
	"strings"
	"unicode/utf8"
)

// cloneAssistantMessageFrom is cloneAssistantMessage for the next snapshot of a message whose previous snapshot is previous. A tool call whose arguments equal the previous snapshot's shares its immutable copy instead of copying again, so publishing an event copies only what changed since the last event.
func cloneAssistantMessageFrom(message AssistantMessage, previous []AssistantContentBlock) AssistantMessage {
	content := message.Content
	message = cloneAssistantMessage(withoutContent(message))
	message.Content = cloneAssistantContentFrom(content, previous)
	return message
}

// withoutContent is message with no content, so cloneAssistantMessage skips the copy the caller makes itself.
func withoutContent(message AssistantMessage) AssistantMessage {
	message.Content = nil
	return message
}

func cloneAssistantContentFrom(blocks, previous []AssistantContentBlock) []AssistantContentBlock {
	if blocks == nil || len(previous) == 0 {
		return cloneAssistantContent(blocks)
	}
	out := make([]AssistantContentBlock, len(blocks))
	for i, block := range blocks {
		switch value := block.(type) {
		case TextContent:
			out[i] = block
			if i < len(previous) {
				if earlier, ok := previous[i].(TextContent); ok && earlier == value {
					out[i] = previous[i]
				}
			}
		case ThinkingContent:
			out[i] = block
			if i < len(previous) {
				if earlier, ok := previous[i].(ThinkingContent); ok && earlier == value {
					out[i] = previous[i]
				}
			}
		case ToolCall:
			var earlier ToolCall
			hasEarlier := false
			if i < len(previous) {
				earlier, hasEarlier = previous[i].(ToolCall)
			}
			if value.Arguments != nil {
				value.Arguments = cloneArgumentsFrom(value.Arguments, earlier.Arguments)
			}
			out[i] = value
			if hasEarlier && sameToolCall(earlier, value) {
				out[i] = previous[i]
			}
		default:
			out[i] = cloneAssistantContent([]AssistantContentBlock{block})[0]
		}
	}
	return out
}

// cloneArgumentsFrom returns what cloneJSONValue returns for arguments, reusing the members of earlier, an immutable copy of an earlier state of the same arguments, that have not changed. It copies the whole tree the usual way unless every member is plain JSON.
func cloneArgumentsFrom(arguments, earlier JsonObject) JsonObject {
	if earlier == nil {
		return JsonObject(cloneJSONValue(arguments).(map[string]any))
	}
	if len(arguments) == len(earlier) && identicalPlainJSON(map[string]any(arguments), map[string]any(earlier)) {
		return earlier
	}
	out := make(map[string]any, len(arguments))
	for key, value := range arguments {
		if !utf8.ValidString(key) {
			return JsonObject(cloneJSONValue(arguments).(map[string]any))
		}
		previous, found := earlier[key]
		switch {
		case found && identicalPlainJSON(value, previous):
			out[key] = previous
		case found && extendsValidString(value, previous):
			out[key] = value
		default:
			copied, ok := clonePlainJSON(value, &plainWalk{})
			if !ok {
				return JsonObject(cloneJSONValue(arguments).(map[string]any))
			}
			out[key] = copied
		}
	}
	return out
}

// extendsValidString reports whether value is the valid UTF-8 string previous with more bytes appended; only the new bytes need checking.
func extendsValidString(value, previous any) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	earlier, ok := previous.(string)
	// Strings that share a buffer compare equal without reading it.
	if !ok || len(earlier) == 0 || !strings.HasPrefix(text, earlier) {
		return false
	}
	return utf8.ValidString(text[len(earlier):])
}

// identicalPlainJSON reports whether two plain JSON trees are identical in shape, type and value. It is false for any value it does not know, so a false answer only costs a copy.
func identicalPlainJSON(left, right any) bool {
	switch left := left.(type) {
	case nil:
		return right == nil
	case bool:
		other, ok := right.(bool)
		return ok && left == other
	case string:
		other, ok := right.(string)
		return ok && left == other
	case float64:
		other, ok := right.(float64)
		return ok && math.Float64bits(left) == math.Float64bits(other)
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok || (left == nil) != (other == nil) || len(left) != len(other) {
			return false
		}
		for key, value := range left {
			counterpart, found := other[key]
			if !found || !identicalPlainJSON(value, counterpart) {
				return false
			}
		}
		return true
	case []any:
		other, ok := right.([]any)
		if !ok || (left == nil) != (other == nil) || len(left) != len(other) {
			return false
		}
		for index := range left {
			if !identicalPlainJSON(left[index], other[index]) {
				return false
			}
		}
		return true
	}
	return false
}

// sameToolCall reports whether the snapshot of a tool call is the same value as an earlier snapshot, which holds when its arguments and member order are the very objects the earlier one holds.
func sameToolCall(earlier, call ToolCall) bool {
	return earlier.scratch == call.scratch && earlier.ID == call.ID && earlier.Name == call.Name &&
		earlier.ThoughtSignature == call.ThoughtSignature && earlier.Namespace == call.Namespace &&
		reflect.ValueOf(earlier.argumentOrder).Pointer() == reflect.ValueOf(call.argumentOrder).Pointer() &&
		(earlier.Arguments == nil) == (call.Arguments == nil) &&
		reflect.ValueOf(earlier.Arguments).Pointer() == reflect.ValueOf(call.Arguments).Pointer()
}
