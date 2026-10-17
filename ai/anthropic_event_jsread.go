package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/jsjson"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// A message_start, content_block_start or message_delta record that is valid JSON but holds a member of an unexpected type is read by Pi's JavaScript member by
// member (anthropic-messages.ts:665-735, :815-857). normalizeAnthropicEventJS rewrites such a record into the shape the typed decoder expects for exactly the cases
// whose JS result it reproduces: a member the stream never reads, a member read through `?.` or `|| 0` that yields nothing, a non-object read as an empty object,
// `?? ""` on a text member, and a stop_reason that mapStopReason cannot match. A member Pi stores as the raw value (ids, names, a message_delta usage count, a
// non-numeric index) is not rewritten, so the typed decode fails again and the turn ends with the decode error.
// upstream: anthropic-messages.ts:665-857

// anthropicEventJS is what the JS read of a mistyped record yields beyond the rewritten record.
type anthropicEventJS struct {
	// unhandledStopReason is set for a truthy stop_reason that is not a string: mapStopReason's switch compares with ===, matches no case, and throws
	// `Unhandled stop reason: ${reason}` (:1617-1648). stopReasonText is String(reason).
	unhandledStopReason bool
	stopReasonText      string
	// responseModel is String(message.model) for a model that is not a string or null: it differs from model.id and matches no fallback model (:668-676).
	responseModel string
}

// decodeAnthropicEventJS decodes a message_start, content_block_start or message_delta record. A record the typed decode rejects for a member's type takes the JS
// read; a record whose mistyped member the JS read does not model ends the turn with the first decode error.
func decodeAnthropicEventJS(sse anthropicStreamEvent, target any) (anthropicEventJS, error) {
	var js anthropicEventJS
	text := jsjson.NormalizeIntegers([]byte(sse.JSON))
	err := json.Unmarshal(text, target)
	if _, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		if normalized, read, ok := normalizeAnthropicEventJS(sse.Type, text); ok && json.Unmarshal(normalized, target) == nil {
			js, err = read, nil
		}
	}
	if err != nil {
		return js, &anthropicSSEParseError{sse: sse.serverSentEvent, cause: err}
	}
	return js, nil
}

func normalizeAnthropicEventJS(kind string, data []byte) (normalized []byte, js anthropicEventJS, ok bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, js, false
	}
	record, isObject := value.(map[string]any)
	if !isObject {
		return nil, js, false
	}
	switch kind {
	case "message_start":
		ok = normalizeAnthropicMessageStart(record, &js)
	case "content_block_start":
		ok = normalizeAnthropicContentBlockStart(record)
	case "message_delta":
		ok = normalizeAnthropicMessageDelta(record, &js)
	}
	if !ok {
		return nil, js, false
	}
	// JSON.parse keeps the record's member order; the rewritten record keeps it too.
	out, err := orderedjson.MarshalInSourceOrder(record, data)
	if err != nil {
		return nil, js, false
	}
	return out, js, true
}

func normalizeAnthropicMessageStart(record map[string]any, js *anthropicEventJS) bool {
	message, isObject := record["message"].(map[string]any)
	if !isObject {
		// A message that is not an object has no id, model or usage; anthropicMessageStartReadErrors reports the read of its usage.
		record["message"] = map[string]any{}
		return true
	}
	if model, present := message["model"]; present {
		if _, isString := model.(string); !isString && model != nil {
			js.responseModel = jsToString(model)
			delete(message, "model")
		}
	}
	usage, present := message["usage"]
	if !present || usage == nil {
		return true
	}
	usageObject, isObject := usage.(map[string]any)
	if !isObject {
		// Every `event.message.usage.x || 0` reads undefined on a value that is not an object.
		delete(message, "usage")
		return true
	}
	for _, key := range []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
		if !numberOrFalsy(usageObject, key) {
			return false
		}
	}
	if cacheCreation, present := usageObject["cache_creation"]; present {
		cacheObject, isObject := cacheCreation.(map[string]any)
		if !isObject {
			delete(usageObject, "cache_creation")
			return true
		}
		return numberOrFalsy(cacheObject, "ephemeral_1h_input_tokens")
	}
	return true
}

// anthropicContentBlockMembers names the members each content_block_start case reads (:689-734); a type no case matches reads none.
var anthropicContentBlockMembers = map[string][]string{
	"text":              {"text"},
	"thinking":          {"thinking", "signature"},
	"redacted_thinking": {"data"},
	"tool_use":          {"id", "name", "input"},
	"fallback":          {},
}

func normalizeAnthropicContentBlockStart(record map[string]any) bool {
	block, present := record["content_block"]
	if !present || block == nil {
		return true
	}
	blockObject, isObject := block.(map[string]any)
	if !isObject {
		// `event.content_block.type` is undefined on a value that is not an object, so no case adds a block.
		record["content_block"] = map[string]any{}
		return true
	}
	kind, _ := blockObject["type"].(string)
	read := map[string]any{}
	if kind != "" {
		read["type"] = kind
	}
	for _, key := range anthropicContentBlockMembers[kind] {
		member, present := blockObject[key]
		if !present {
			continue
		}
		switch key {
		case "text", "thinking", "signature":
			// `?? ""` keeps any value that is not null or undefined, and the deltas append to it with `+=`.
			if member != nil {
				member = jsToString(member)
			}
		}
		read[key] = member
	}
	record["content_block"] = read
	return true
}

func normalizeAnthropicMessageDelta(record map[string]any, js *anthropicEventJS) bool {
	if delta, present := record["delta"]; present && delta != nil {
		deltaObject, isObject := delta.(map[string]any)
		if !isObject {
			// `event.delta.stop_reason` is undefined on a value that is not an object.
			record["delta"] = map[string]any{}
		} else if !normalizeAnthropicStopDelta(deltaObject, js) {
			return false
		}
	}
	usage, present := record["usage"]
	if !present || usage == nil {
		return true
	}
	usageObject, isObject := usage.(map[string]any)
	if !isObject {
		// `if (event.usage)` reads every member below as undefined on a value that is not an object.
		delete(record, "usage")
		return true
	}
	if cacheCreation, present := usageObject["cache_creation"]; present {
		if _, isObject := cacheCreation.(map[string]any); !isObject {
			delete(usageObject, "cache_creation")
		}
	}
	if details, present := usageObject["output_tokens_details"]; present {
		if _, isObject := details.(map[string]any); !isObject {
			delete(usageObject, "output_tokens_details")
		}
	}
	return true
}

func normalizeAnthropicStopDelta(delta map[string]any, js *anthropicEventJS) bool {
	reason, present := delta["stop_reason"]
	if present && reason != nil {
		if _, isString := reason.(string); !isString {
			if jsTruthy(reason) {
				js.unhandledStopReason = true
				js.stopReasonText = jsToString(reason)
			}
			delete(delta, "stop_reason")
		}
	}
	// stop_details is read only for a "refusal", as `stopDetails?.explanation || ...`.
	details, present := delta["stop_details"]
	if !present || details == nil {
		return true
	}
	detailsObject, isObject := details.(map[string]any)
	if text, _ := delta["stop_reason"].(string); !isObject || text != "refusal" {
		delete(delta, "stop_details")
		return true
	}
	delete(detailsObject, "type")
	if explanation, present := detailsObject["explanation"]; present {
		if _, isString := explanation.(string); !isString && explanation != nil {
			if jsTruthy(explanation) {
				return false
			}
			delete(detailsObject, "explanation")
		}
	}
	return true
}

// numberOrFalsy keeps a number or null member, drops a falsy member that is not one (`x || 0` reads 0), and reports false for any other value.
func numberOrFalsy(object map[string]any, key string) bool {
	value, present := object[key]
	if !present || value == nil {
		return true
	}
	if _, isNumber := value.(json.Number); isNumber {
		return true
	}
	if jsTruthy(value) {
		return false
	}
	delete(object, key)
	return true
}

// anthropicRecord is a record's members as Pi's JSON.parse gives them, or nil for a record that is not an object.
func anthropicRecord(text string) map[string]any {
	decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
	decoder.UseNumber()
	var record map[string]any
	if decoder.Decode(&record) != nil {
		return nil
	}
	return record
}

// jsReadError is the TypeError V8 throws for `object[key].property` when the member is null or absent, and nil when it holds a value. A value that is not an
// object reads property as undefined without throwing.
func jsReadError(object map[string]any, key, property string) error {
	value, present := object[key]
	switch {
	case !present:
		return fmt.Errorf("Cannot read properties of undefined (reading '%s')", property)
	case value == nil:
		return fmt.Errorf("Cannot read properties of null (reading '%s')", property)
	}
	return nil
}

// anthropicMessageStartReadErrors are the TypeErrors message_start throws (anthropic-messages.ts:666, :679): reading `event.message.id` before anything is
// recorded, and reading `event.message.usage.input_tokens` after the response id and model are.
func anthropicMessageStartReadErrors(text string) (atID, atUsage error) {
	record := anthropicRecord(text)
	if err := jsReadError(record, "message", "id"); err != nil {
		return err, nil
	}
	message, _ := record["message"].(map[string]any)
	return nil, jsReadError(message, "usage", "input_tokens")
}
