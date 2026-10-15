package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// normalizeMistralChunkJS rewrites a Mistral chunk the typed decode rejects for a member's type as consumeChatStream reads it
// (mistral-conversations.ts:597-607, getMistralCachedPromptTokens :541-560): `output.responseId ||= chunk.id` skips a falsy id, a truthy usage that is
// not an object reads every count as undefined, `prompt_tokens || 0` is only subtracted from and compared, so a truthy non-number counts as its
// Number() when that is an integer, and the cached-token `??` chain takes its first non-nullish member, which counts 0 unless it is a number. A truthy id,
// completion or total count that is not a number is stored raw, so such a chunk is not rewritten and still ends the turn with the decode error.
// upstream: mistral-conversations.ts:541-607
func normalizeMistralChunkJS(data []byte) ([]byte, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var chunk map[string]any
	if decoder.Decode(&chunk) != nil || chunk == nil {
		return nil, false
	}
	if id, present := chunk["id"]; present && id != nil && !isJSString(id) {
		if jsTruthy(id) {
			return nil, false
		}
		delete(chunk, "id")
	}
	if !normalizeMistralUsage(chunk) {
		return nil, false
	}
	// JSON.parse keeps the chunk's member order, and a tool call's object arguments reach JSON.stringify (mistral-conversations.ts:744-747) in it.
	out, err := orderedjson.MarshalInSourceOrder(chunk, data)
	return out, err == nil
}

func normalizeMistralUsage(chunk map[string]any) bool {
	usage, present := chunk["usage"]
	if !present || usage == nil {
		return true
	}
	usageObject, isObject := usage.(map[string]any)
	if !isObject {
		if jsTruthy(usage) {
			chunk["usage"] = map[string]any{}
		} else {
			delete(chunk, "usage")
		}
		return true
	}
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
	for _, key := range []string{"completion_tokens", "total_tokens"} {
		if !numberOrFalsy(usageObject, key) {
			return false
		}
	}
	chain := []struct{ holder, key string }{
		{"promptTokensDetails", "cachedTokens"}, {"prompt_tokens_details", "cached_tokens"}, {"promptTokenDetails", "cachedTokens"},
		{"prompt_token_details", "cached_tokens"}, {"", "numCachedTokens"}, {"", "num_cached_tokens"},
	}
	found := false
	for _, link := range chain {
		object := usageObject
		if link.holder != "" {
			details, isObject := usageObject[link.holder].(map[string]any)
			if !isObject {
				// `?.` reads undefined from a value that is not an object.
				delete(usageObject, link.holder)
				continue
			}
			// Each details object is read for its one key.
			object = map[string]any{}
			if value, present := details[link.key]; present {
				object[link.key] = value
			}
			usageObject[link.holder] = object
		}
		value, present := object[link.key]
		switch {
		case !present || value == nil:
		case found:
			delete(object, link.key)
		default:
			found = true
			if number, isNumber := value.(json.Number); !isNumber {
				object[link.key] = json.Number("0")
			} else if _, err := number.Int64(); err != nil {
				object[link.key] = json.Number("0")
			}
		}
	}
	return true
}

// mistralFinishReason is a choice's finish_reason as `if (choice.finish_reason)` then mapChatStopReason read it: a falsy reason is skipped, and a truthy
// one that is not a string matches no case of the === switch and reports `Provider stopped with: ${reason}` (:612-619, :922-938). text is the reason, or
// its String() form when notString is set.
type mistralFinishReason struct {
	text      string
	notString bool
}

func (reason *mistralFinishReason) UnmarshalJSON(data []byte) error {
	*reason = mistralFinishReason{}
	trimmed := bytes.TrimSpace(data)
	if !jsonValueTruthy(trimmed) {
		return nil
	}
	if trimmed[0] == '"' {
		return json.Unmarshal(trimmed, &reason.text)
	}
	reason.text, reason.notString = jsStringFromRaw(trimmed), true
	return nil
}

func jsStringFromRaw(raw json.RawMessage) string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return ""
	}
	return jsToString(value)
}

// mistralContentItemText is the text a content item's case appends: sanitizeSurrogates(item.text ?? "") throws for a value that is not a string, and a
// thinking item keeps each part's `part.text ?? ""` whose length is > 0 (:646-683).
func mistralContentItemJS(raw json.RawMessage) (kind, text string, err error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return "", "", nil
	}
	item, isObject := value.(map[string]any)
	if !isObject {
		// `item.type` of a value that is not an object matches no case.
		return "", "", nil
	}
	kind, _ = item["type"].(string)
	switch kind {
	case "text":
		switch member := item["text"].(type) {
		case nil:
		case string:
			text = member
		default:
			return "", "", errors.New("text.replace is not a function")
		}
	case "thinking":
		parts, isArray := item["thinking"].([]any)
		if item["thinking"] != nil && !isArray {
			return "", "", errors.New("(item.thinking ?? []).map is not a function")
		}
		var b strings.Builder
		b.WriteString(text)
		for _, part := range parts {
			if part == nil {
				return "", "", errors.New("Cannot read properties of null (reading 'text')")
			}
			fields, _ := part.(map[string]any)
			member := fields["text"]
			switch member := member.(type) {
			case nil:
			case string:
				b.WriteString(member)
			case []any:
				if len(member) > 0 {
					b.WriteString(jsToString(member))
				}
			case map[string]any:
				if length, hasLength := member["length"]; hasLength && jsGreaterThanZero(length) {
					b.WriteString(jsToString(member))
				}
			}
		}
		text = b.String()
	}
	return kind, text, nil
}
