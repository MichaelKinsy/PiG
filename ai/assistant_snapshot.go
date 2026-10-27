package ai

import (
	"bytes"
	"math"
	"reflect"
	"strings"
	"unicode/utf8"
)

// cloneAssistantMessageFrom is cloneAssistantMessage for the next snapshot of a message whose previous snapshot is previous. A tool call whose arguments equal the previous snapshot's shares its immutable copy instead of copying again, so publishing an event copies only what changed since the last event.
func cloneAssistantMessageFrom(message AssistantMessage, previous []AssistantContentBlock) AssistantMessage {
	return cloneAssistantMessageSharing(message, previous, nil)
}

// cloneAssistantMessageSharing is cloneAssistantMessageFrom that keeps the streamed arguments snapshots of cloneAssistantContentSharing.
func cloneAssistantMessageSharing(message AssistantMessage, previous []AssistantContentBlock, streamed map[int]JsonObject) AssistantMessage {
	content := message.Content
	message = cloneAssistantMessage(withoutContent(message))
	message.Content = cloneAssistantContentSharing(content, previous, streamed)
	return message
}

// withoutContent is message with no content, so cloneAssistantMessage skips the copy the caller makes itself.
func withoutContent(message AssistantMessage) AssistantMessage {
	message.Content = nil
	return message
}

// cloneAssistantContentFrom copies blocks as cloneAssistantContent does, reusing what the previous snapshot, previous, already holds of a block that has not changed; a block it has to copy it boxes once, and a block it reuses it does not box at all.
func cloneAssistantContentFrom(blocks, previous []AssistantContentBlock) []AssistantContentBlock {
	return cloneAssistantContentSharing(blocks, previous, nil)
}

// cloneAssistantContentSharing is cloneAssistantContentFrom keeping, for a tool call whose arguments are streamed's snapshot at its
// index, that snapshot instead of a copy of it.
func cloneAssistantContentSharing(blocks, previous []AssistantContentBlock, streamed map[int]JsonObject) []AssistantContentBlock {
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
			if value.Arguments != nil && !sharesStreamedArguments(streamed, i, value) {
				value.Arguments = cloneArgumentsFrom(value.Arguments, earlier.Arguments)
			}
			if hasEarlier && sameToolCall(earlier, value) {
				out[i] = previous[i]
			} else {
				out[i] = value
			}
		default:
			out[i] = cloneAssistantContent([]AssistantContentBlock{block})[0]
		}
	}
	return out
}

// sharesStreamedArguments reports whether call's arguments are the immutable snapshot streamed names for its content index, still the
// map the producer gave the call: a message cell that publishes the call keeps that map as its copy. The parser built the snapshot with
// valid strings and finite numbers and never changes it, and a cell never hands out what it holds (a view, Observe and ShallowCopy copy
// it; ObserveProjection's arguments are read-only), so the copy would be equal and held by no one who can change it.
func sharesStreamedArguments(streamed map[int]JsonObject, index int, call ToolCall) bool {
	snapshot, ok := streamed[index]
	return ok && snapshot != nil && reflect.ValueOf(snapshot).Pointer() == reflect.ValueOf(call.Arguments).Pointer()
}

// cloneArgumentsFrom returns what cloneJSONValue returns for arguments, reusing the members of earlier, an immutable copy of an earlier state of the same arguments, that have not changed. It copies the whole tree the usual way unless every member is plain JSON.
func cloneArgumentsFrom(arguments, earlier JsonObject) JsonObject {
	if earlier == nil {
		return JsonObject(cloneArgumentsValue(arguments).(map[string]any))
	}
	if len(arguments) == len(earlier) && identicalPlainJSON(map[string]any(arguments), map[string]any(earlier)) {
		return earlier
	}
	out := make(map[string]any, len(arguments))
	for key, value := range arguments {
		if !utf8.ValidString(key) {
			return JsonObject(cloneArgumentsValue(arguments).(map[string]any))
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
				return JsonObject(cloneArgumentsValue(arguments).(map[string]any))
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
	return sameToolCallIdentity(earlier, call) &&
		reflect.ValueOf(earlier.argumentOrder).Pointer() == reflect.ValueOf(call.argumentOrder).Pointer() &&
		bytes.Equal(earlier.nonObjectArguments, call.nonObjectArguments) &&
		(earlier.Arguments == nil) == (call.Arguments == nil) &&
		reflect.ValueOf(earlier.Arguments).Pointer() == reflect.ValueOf(call.Arguments).Pointer()
}

// sameToolCallIdentity reports whether two tool calls agree in everything but their arguments, member order and deferred argument text.
func sameToolCallIdentity(a, b ToolCall) bool {
	return a.ID == b.ID && a.Name == b.Name && a.ThoughtSignature == b.ThoughtSignature &&
		a.Namespace == b.Namespace && a.ArgumentsError == b.ArgumentsError
}
