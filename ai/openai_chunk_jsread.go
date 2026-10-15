package ai

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// A chunk of an openai-completions stream that is valid JSON but holds a member of an unexpected type is read by Pi's JavaScript member by member, so the turn
// continues, ignores the member, coerces it with `+=`, or throws, depending on the expression that reads it (openai-completions.ts:558-700). normalizeOpenAIChunkJS
// rewrites such a chunk into the shape the typed decoder expects for exactly the cases whose JS result it reproduces. A member Pi stores or adds as the raw value
// (an id or name, a completion or cache count that is not a number, a prompt count whose Number() is not an integer, a non-integer tool call index) is not
// rewritten, so the typed decode fails again and the turn ends with the decode error.
// upstream: openai-completions.ts:558-700

// notIterableToolCalls is the TypeError V8 throws for `for (const toolCall of choice.delta.tool_calls)` when the value is not iterable (:640-641).
const notIterableToolCalls = "choice.delta.tool_calls is not iterable"

func jsTruthy(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case json.Number:
		return jsnumber.FromJSON([]byte(value)) != 0
	default:
		return true
	}
}

// jsToString is String(value) for a parsed JSON value.
func jsToString(value any) string {
	switch value := value.(type) {
	case nil:
		return ""
	case bool:
		if value {
			return "true"
		}
		return "false"
	case string:
		return value
	case json.Number:
		return jsnumber.StringFromJSON([]byte(value))
	case []any:
		parts := make([]string, len(value))
		for i, element := range value {
			parts[i] = jsToString(element)
		}
		return strings.Join(parts, ",")
	default:
		return "[object Object]"
	}
}

// openAIChunkRead is what the JS read of a mistyped chunk yields beyond the rewritten chunk.
type openAIChunkRead struct {
	// thrown is the message of a TypeError Pi throws after the chunk's content and reasoning were processed.
	thrown string
	// finishReason is String(finish_reason) for a truthy finish_reason that is not a string: mapStopReason's switch matches no case and records
	// `Provider finish_reason: ${reason}` (openai-completions.ts:1562-1585).
	finishReason *string
}

// normalizeOpenAIChunkJS returns the chunk as the typed decoder reads it and what the JS read yields beyond it. ok is false when a mistyped member is not one
// of the modeled cases.
func normalizeOpenAIChunkJS(data []byte) (normalized []byte, read openAIChunkRead, ok bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, read, false
	}
	chunk, isObject := value.(map[string]any)
	if !isObject {
		// `if (!chunk || typeof chunk !== "object") continue` and an array has none of the members read below.
		return []byte("{}"), read, true
	}
	if !normalizeOpenAIChunkMembers(chunk, &read) {
		return nil, read, false
	}
	// JSON.parse keeps the chunk's member order; the rewritten chunk keeps it too.
	out, err := orderedjson.MarshalInSourceOrder(chunk, data)
	if err != nil {
		return nil, read, false
	}
	return out, read, true
}

func normalizeOpenAIChunkMembers(chunk map[string]any, read *openAIChunkRead) bool {
	if !stringOrDrop(chunk, "id") {
		return false
	}
	if !normalizeOpenAIUsageMember(chunk) {
		return false
	}
	choices, isArray := chunk["choices"].([]any)
	if !isArray || len(choices) == 0 {
		delete(chunk, "choices")
		return true
	}
	choice, isObject := choices[0].(map[string]any)
	if !isObject {
		// A falsy first choice ends the chunk; any other value has none of the members read below.
		chunk["choices"] = []any{}
		return true
	}
	chunk["choices"] = []any{choice}
	delete(choice, "index")
	if reason, present := choice["finish_reason"]; present {
		if _, isString := reason.(string); !isString && reason != nil {
			if jsTruthy(reason) {
				text := jsToString(reason)
				read.finishReason = &text
			}
			delete(choice, "finish_reason")
		}
	}
	if !normalizeOpenAIUsageMember(choice) {
		return false
	}
	delta, present := choice["delta"]
	if !present {
		return true
	}
	deltaObject, isObject := delta.(map[string]any)
	if !isObject {
		// A falsy delta is skipped; a truthy non-object reads every member below as undefined.
		delete(choice, "delta")
		return true
	}
	return normalizeOpenAIDelta(deltaObject, &read.thrown)
}

// normalizeOpenAIUsageMember rewrites the usage member of a chunk or a choice as parseChunkUsage reads it (openai-completions.ts:1519-1560). A falsy usage is
// absent; any other value that is not an object reads every count as undefined, so it is an empty usage that resets the counts to 0.
func normalizeOpenAIUsageMember(owner map[string]any) bool {
	usage, present := owner["usage"]
	if !present || usage == nil {
		return true
	}
	usageObject, isObject := usage.(map[string]any)
	if !isObject {
		if jsTruthy(usage) {
			owner["usage"] = map[string]any{}
		} else {
			delete(owner, "usage")
		}
		return true
	}
	// `rawUsage.prompt_tokens || 0` is subtracted from, so a truthy value that is not a number counts as its Number() when that is an integer.
	if prompt, present := usageObject["prompt_tokens"]; present && prompt != nil {
		if _, isNumber := prompt.(json.Number); !isNumber {
			if !jsTruthy(prompt) {
				delete(usageObject, "prompt_tokens")
			} else {
				number, ok := jsIntegerNumber(prompt)
				if !ok {
					return false
				}
				usageObject["prompt_tokens"] = number
			}
		}
	}
	if !numberOrFalsy(usageObject, "completion_tokens") {
		return false
	}
	details, _ := usageObject["prompt_tokens_details"].(map[string]any)
	if details == nil {
		// `prompt_tokens_details?.x` of a value that is not an object is undefined.
		delete(usageObject, "prompt_tokens_details")
	} else if !numberOrFalsy(details, "cache_write_tokens") {
		return false
	}
	// `details?.cached_tokens ?? prompt_cache_hit_tokens ?? cached_tokens ?? 0` takes the first that is not null or undefined, whatever its type.
	readCached := false
	for _, member := range []struct {
		object map[string]any
		key    string
	}{{details, "cached_tokens"}, {usageObject, "prompt_cache_hit_tokens"}, {usageObject, "cached_tokens"}} {
		value, present := member.object[member.key]
		if !present || value == nil {
			continue
		}
		if readCached {
			delete(member.object, member.key)
			continue
		}
		if _, isNumber := value.(json.Number); !isNumber {
			return false
		}
		readCached = true
	}
	completion, _ := usageObject["completion_tokens_details"].(map[string]any)
	if completion == nil {
		delete(usageObject, "completion_tokens_details")
		return true
	}
	return numberOrFalsy(completion, "reasoning_tokens")
}

// jsIntegerNumber is Number(value) as a JSON number when it is an integer a Go int holds exactly.
func jsIntegerNumber(value any) (json.Number, bool) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	number := jsnumber.FromJSON(raw)
	if number != math.Trunc(number) || math.Abs(number) > 1<<53 {
		return "", false
	}
	return json.Number(strconv.FormatInt(int64(number), 10)), true
}

// stringOrDrop keeps a string or null member, drops a falsy member that is not one, and reports false for any other value.
func stringOrDrop(object map[string]any, key string) bool {
	value, present := object[key]
	if !present {
		return true
	}
	if _, isString := value.(string); isString || value == nil {
		return true
	}
	if jsTruthy(value) {
		return false
	}
	delete(object, key)
	return true
}

func normalizeOpenAIDelta(delta map[string]any, thrown *string) bool {
	// The stream never reads delta.role.
	if _, isString := delta["role"].(string); !isString {
		delete(delta, "role")
	}
	if content, present := delta["content"]; present {
		switch content := content.(type) {
		case string, nil:
		case []any:
			// `content.length > 0`, then `block.text += content` appends String(content).
			if len(content) > 0 {
				delta["content"] = jsToString(content)
			} else {
				delete(delta, "content")
			}
		case map[string]any:
			if length, hasLength := content["length"]; hasLength && jsGreaterThanZero(length) {
				delta["content"] = jsToString(content)
			} else {
				delete(delta, "content")
			}
		default:
			// A number or boolean has no length, so `content.length > 0` is false.
			delete(delta, "content")
		}
	}
	for _, field := range []string{"reasoning_content", "reasoning", "reasoning_text"} {
		if _, isString := delta[field].(string); !isString {
			delete(delta, field)
		}
	}
	if _, isArray := delta["reasoning_details"].([]any); !isArray {
		delete(delta, "reasoning_details")
	}
	toolCalls, present := delta["tool_calls"]
	if !present {
		return true
	}
	if !jsTruthy(toolCalls) {
		delete(delta, "tool_calls")
		return true
	}
	items, isArray := toolCalls.([]any)
	if text, isString := toolCalls.(string); isString {
		// for-of iterates a string's code points, and every member of a one-character string reads undefined, as for an empty object.
		items, isArray = make([]any, 0, len(text)), true
		for range text {
			items = append(items, map[string]any{})
		}
		delta["tool_calls"] = items
	}
	if !isArray {
		// The for-of throws after the reasoning, reasoning_details and content of this chunk (Pi 63035734 moved reasoning_details before the tool calls).
		*thrown = notIterableToolCalls
		delete(delta, "tool_calls")
		return true
	}
	for i, item := range items {
		if item == nil {
			return false
		}
		toolCall, isObject := item.(map[string]any)
		if !isObject {
			// Every member of a primitive or an array element reads undefined, as for an empty object.
			items[i] = map[string]any{}
			continue
		}
		if !normalizeOpenAIToolCall(toolCall) {
			return false
		}
	}
	return true
}

func normalizeOpenAIToolCall(toolCall map[string]any) bool {
	if index, present := toolCall["index"]; present {
		if number, isNumber := index.(json.Number); isNumber {
			if _, err := number.Int64(); err != nil {
				return false
			}
		} else {
			// `typeof toolCall.index === "number" ? toolCall.index : undefined`
			delete(toolCall, "index")
		}
	}
	if !stringOrDrop(toolCall, "id") {
		return false
	}
	// The stream never reads toolCall.type.
	if _, isString := toolCall["type"].(string); !isString {
		delete(toolCall, "type")
	}
	if custom, present := toolCall["custom"]; present {
		if _, isObject := custom.(map[string]any); !isObject {
			if jsTruthy(custom) {
				return false
			}
			// `toolCall.custom?.name` and `if (toolCall.custom && ...)` read a falsy custom as absent.
			delete(toolCall, "custom")
		}
	}
	function, present := toolCall["function"]
	if !present {
		return true
	}
	functionObject, isObject := function.(map[string]any)
	if !isObject {
		// `toolCall.function?.name` of a primitive or an array reads undefined; a falsy function is absent.
		delete(toolCall, "function")
		return true
	}
	if name, present := functionObject["name"]; present && name != nil {
		if _, isString := name.(string); !isString {
			return false
		}
	}
	if arguments, present := functionObject["arguments"]; present && arguments != nil {
		if _, isString := arguments.(string); !isString {
			if jsTruthy(arguments) {
				// `block.partialArgs = (block.partialArgs ?? "") + toolCall.function.arguments`
				functionObject["arguments"] = jsToString(arguments)
			} else {
				delete(functionObject, "arguments")
			}
		}
	}
	return true
}

// jsGreaterThanZero is `value > 0` for a parsed JSON value.
func jsGreaterThanZero(value any) bool {
	raw, err := json.Marshal(value)
	return err == nil && jsnumber.FromJSON(raw) > 0
}
