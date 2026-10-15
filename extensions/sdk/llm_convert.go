package sdk

import (
	"math"
	"strconv"
	"strings"
)

// Pi core/messages.ts COMPACTION_SUMMARY_PREFIX/SUFFIX and BRANCH_SUMMARY_PREFIX/SUFFIX.
const (
	compactionSummaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	compactionSummarySuffix = "\n</summary>"
	branchSummaryPrefix     = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
	branchSummarySuffix     = "</summary>"
)

// ConvertToLlm is Pi's convertToLlm (core/messages.ts:148, exported from index.ts): it turns the coding agent's custom message roles (bashExecution,
// custom, branchSummary, compactionSummary) into user messages and returns the system, user, assistant and toolResult messages as they are. A
// bashExecution message with excludeFromContext set, and a message with any other role, is dropped. A message is the flat role-discriminated JSON
// object the wire carries. A nil entry has no role and is dropped; Pi throws a TypeError on null there, which a typed slice cannot express.
func ConvertToLlm(messages []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if converted, ok := convertToLlmMessage(message); ok {
			out = append(out, converted)
		}
	}
	return out
}

func convertToLlmMessage(message map[string]any) (map[string]any, bool) {
	role, _ := message["role"].(string)
	switch role {
	case "bashExecution":
		if jsTruthy(message["excludeFromContext"]) {
			return nil, false
		}
		return userTextMessage(message, bashExecutionToText(message)), true
	case "custom":
		content, present := message["content"]
		if text, isString := content.(string); isString {
			content = []any{map[string]any{"type": "text", "text": text}}
		}
		converted := map[string]any{"role": "user"}
		if present {
			converted["content"] = content
		}
		copyTimestamp(converted, message)
		return converted, true
	case "branchSummary":
		return userTextMessage(message, branchSummaryPrefix+jsToString(message, "summary")+branchSummarySuffix), true
	case "compactionSummary":
		return userTextMessage(message, compactionSummaryPrefix+jsToString(message, "summary")+compactionSummarySuffix), true
	case "system", "user", "assistant", "toolResult":
		return message, true
	}
	return nil, false
}

func userTextMessage(source map[string]any, text string) map[string]any {
	converted := map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}
	copyTimestamp(converted, source)
	return converted
}

// copyTimestamp carries m.timestamp over; an undefined timestamp is absent from the JSON a message travels as.
func copyTimestamp(converted, source map[string]any) {
	if timestamp, present := source["timestamp"]; present {
		converted["timestamp"] = timestamp
	}
}

// bashExecutionToText is Pi's bashExecutionToText (core/messages.ts:76-92).
func bashExecutionToText(message map[string]any) string {
	text := "Ran `" + jsToString(message, "command") + "`\n"
	if jsTruthy(message["output"]) {
		text += "```\n" + jsToString(message, "output") + "\n```"
	} else {
		text += "(no output)"
	}
	if jsTruthy(message["cancelled"]) {
		text += "\n\n(command cancelled)"
	} else if exitCode, present := message["exitCode"]; present && exitCode != nil && !isNumberZero(exitCode) {
		text += "\n\nCommand exited with code " + jsToString(message, "exitCode")
	}
	if jsTruthy(message["truncated"]) && jsTruthy(message["fullOutputPath"]) {
		text += "\n\n[Output truncated. Full output: " + jsToString(message, "fullOutputPath") + "]"
	}
	return text
}

func isNumberZero(value any) bool {
	number, ok := value.(float64)
	return ok && number == 0
}

// jsToString is the string a template literal or `+` makes of message[key]: JavaScript's ToString of a value decoded from JSON, or "undefined" for an
// absent key.
func jsToString(message map[string]any, key string) string {
	value, present := message[key]
	if !present {
		return "undefined"
	}
	return jsValueString(value)
}

func jsValueString(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case float64:
		return jsNumberToString(value)
	case []any:
		parts := make([]string, len(value))
		for i, element := range value {
			if element != nil {
				parts[i] = jsValueString(element)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// jsNumberToString is Number::toString, which spells a non-finite number as NaN, Infinity or -Infinity where JSON.stringify writes null.
func jsNumberToString(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	}
	return jsNumberString(value)
}
