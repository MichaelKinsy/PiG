package sdk

import (
	"errors"
	"math"
)

// SessionEntryToContextMessages is Pi's sessionEntryToContextMessages (core/session-manager.ts, exported from index.ts): the LLM and runtime messages a session entry
// contributes to the context. A message entry is its message, where a null or missing content reads as the empty one Pi supplies (an empty string for a system message, an empty
// list for a user, assistant or toolResult one). A custom_message entry is a custom message, a branch_summary entry with a summary a branchSummary message, and a compaction
// entry a compactionSummary message after the system message it carries, if any. Every other entry (a label, a model change, a plain custom entry) is no message. The entry's
// timestamp string becomes the epoch milliseconds of Date.parse, which the host reads (as [ParseDate] does), and a timestamp Date.parse cannot read is null in the message, as
// JSON.stringify writes the NaN Pi holds. A message entry with a null or missing message is Pi's TypeError.
func SessionEntryToContextMessages(entry map[string]any) ([]any, error) {
	switch kind, _ := entry["type"].(string); kind {
	case "message":
		message, isObject := entry["message"].(map[string]any)
		if entry["message"] == nil {
			return nil, errors.New("TypeError: Cannot read properties of undefined (reading 'role')")
		}
		if !isObject {
			// A message that is not an object (a hand-edited file) has no role and no content: Pi returns it as it is.
			return []any{entry["message"]}, nil
		}
		role, _ := message["role"].(string)
		if message["content"] == nil {
			switch role {
			case "system":
				return []any{withContent(message, "")}, nil
			case "user", "assistant", "toolResult":
				return []any{withContent(message, []any{})}, nil
			}
		}
		return []any{message}, nil
	case "custom_message":
		timestamp, err := entryTimestamp(entry)
		if err != nil {
			return nil, err
		}
		content := entry["content"]
		if content == nil {
			content = []any{}
		}
		message := map[string]any{"role": "custom", "content": content, "timestamp": timestamp}
		for _, key := range []string{"customType", "display", "details"} {
			if value, present := entry[key]; present {
				message[key] = value
			}
		}
		return []any{message}, nil
	case "branch_summary":
		summary, _ := entry["summary"].(string)
		if summary == "" {
			return []any{}, nil
		}
		timestamp, err := entryTimestamp(entry)
		if err != nil {
			return nil, err
		}
		message := map[string]any{"role": "branchSummary", "summary": summary, "timestamp": timestamp}
		if value, present := entry["fromId"]; present {
			message["fromId"] = value
		}
		return []any{message}, nil
	case "compaction":
		timestamp, err := entryTimestamp(entry)
		if err != nil {
			return nil, err
		}
		summary := map[string]any{"role": "compactionSummary", "summary": entry["summary"], "timestamp": timestamp}
		if value, present := entry["tokensBefore"]; present {
			summary["tokensBefore"] = value
		}
		if system, ok := entry["systemMessage"].(map[string]any); ok && jsTruthy(system) {
			return []any{system, summary}, nil
		}
		return []any{summary}, nil
	}
	return []any{}, nil
}

// entryTimestamp is `new Date(entry.timestamp).getTime()`: Date.parse of a string, a number's own time value (clipped as TimeClip does), and null for the NaN Pi holds when it
// is neither.
func entryTimestamp(entry map[string]any) (any, error) {
	switch value := entry["timestamp"].(type) {
	case string:
		ms, ok, err := ParseDate(value)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		return ms, nil
	case float64:
		if math.IsNaN(value) || math.Abs(value) > 8.64e15 {
			return nil, nil
		}
		return math.Trunc(value) + 0, nil
	}
	return nil, nil
}
