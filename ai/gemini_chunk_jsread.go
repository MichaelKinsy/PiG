package ai

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/MichaelKinsy/PiG/internal/jsjson"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// A Gemini stream chunk that is valid JSON but holds a member of an unexpected type is read by Pi's JavaScript member by member (google-generative-ai.ts
// :106-235, google-vertex.ts the same loop). normalizeGeminiChunkJS rewrites such a chunk into the shape the typed decoder expects for the cases whose JS
// result it reproduces:
//   - `chunk.candidates?.[0]` of a value that is not an array is the value's "0" member, or undefined; a candidate, content or functionCall that is not an
//     object reads every member as undefined;
//   - `for (const part of candidate.content.parts)` iterates a string's characters, which have no members, and throws for any other value that is not
//     iterable; a null part throws reading its text;
//   - `part.text !== undefined` then `+=` appends String(text), "null" for null;
//   - `part.thought === true`, and retainThoughtSignature keeps only a non-empty string;
//   - a truthy finishReason that is not a string matches no case of mapStopReason's === switch, which throws `Unhandled stop reason: ${reason}`;
//   - usageMetadata counts are read with `|| 0`, and the prompt count is subtracted from, so it counts as its Number() when that is an integer.
//
// A member Pi stores raw (a responseId, a functionCall id or name, a tool call's thoughtSignature, a count added or stored) is not rewritten, so the
// typed decode fails again and the turn ends with the decode error.
// upstream: google-generative-ai.ts:106-235

// geminiChunkRead is what the JS read of a chunk yields beyond the rewritten chunk.
type geminiChunkRead struct {
	// partsErr is the TypeError the parts loop throws after the parts before it were handled.
	partsErr error
	// finishReason is String(finishReason) for a truthy finish reason that is not a string.
	finishReason *string
}

// decodeGeminiChunk decodes one stream chunk as the SDK's Response.json() and Pi's reads of it see it. The typed decode stays the first pass; a chunk
// it rejects for a member's type, or one with a part that has no members (a null text reads as nothing), takes the JS read.
func decodeGeminiChunk(text string, chunk *geminiStreamChunk) error {
	data := jsjson.NormalizeIntegers([]byte(text))
	err := json.Unmarshal(data, chunk)
	var mismatch *json.UnmarshalTypeError
	if err == nil && !geminiChunkHasEmptyPart(chunk) || err != nil && !errors.As(err, &mismatch) {
		return err
	}
	normalized, read, ok := normalizeGeminiChunkJS(data)
	if !ok {
		return err
	}
	var rewritten geminiStreamChunk
	if json.Unmarshal(normalized, &rewritten) != nil {
		return err
	}
	*chunk = rewritten
	chunk.read = read
	return nil
}

func geminiChunkHasEmptyPart(chunk *geminiStreamChunk) bool {
	if len(chunk.Candidates) == 0 || chunk.Candidates[0].Content == nil {
		return false
	}
	for _, part := range chunk.Candidates[0].Content.Parts {
		if part.Text == nil && part.Thought == nil && part.ThoughtSignature == "" && part.FunctionCall == nil && part.FunctionResponse == nil && part.InlineData == nil {
			return true
		}
	}
	return false
}

func normalizeGeminiChunkJS(data []byte) (normalized []byte, read geminiChunkRead, ok bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, read, false
	}
	chunk, isObject := value.(map[string]any)
	if !isObject {
		return nil, read, false
	}
	if id, present := chunk["responseId"]; present && id != nil && !isJSString(id) {
		// `output.responseId ||= chunk.responseId` stores a truthy id raw.
		if jsTruthy(id) {
			return nil, read, false
		}
		delete(chunk, "responseId")
	}
	if !normalizeGeminiUsage(chunk) {
		return nil, read, false
	}
	var candidate any
	switch candidates := chunk["candidates"].(type) {
	case []any:
		if len(candidates) > 0 {
			candidate = candidates[0]
		}
	case map[string]any:
		candidate = candidates["0"]
	}
	delete(chunk, "candidates")
	if candidate == nil {
		return marshalGeminiChunk(chunk, data, read)
	}
	fields, isObject := candidate.(map[string]any)
	if !isObject {
		// A string candidate's characters, like a number's members, are undefined.
		chunk["candidates"] = []any{map[string]any{}}
		return marshalGeminiChunk(chunk, data, read)
	}
	kept := map[string]any{}
	if reason, present := fields["finishReason"]; present && reason != nil {
		if isJSString(reason) {
			kept["finishReason"] = reason
		} else if jsTruthy(reason) {
			text := jsToString(reason)
			read.finishReason = &text
		}
	}
	if content, isObject := fields["content"].(map[string]any); isObject {
		parts, err, ok := normalizeGeminiParts(content["parts"])
		if !ok {
			return nil, read, false
		}
		read.partsErr = err
		if role, isString := content["role"].(string); isString {
			kept["content"] = map[string]any{"role": role, "parts": parts}
		} else {
			kept["content"] = map[string]any{"parts": parts}
		}
	}
	chunk["candidates"] = []any{kept}
	return marshalGeminiChunk(chunk, data, read)
}

// marshalGeminiChunk writes the rewritten chunk in the member order of the chunk it was parsed from: the SDK's JSON.parse keeps that order, and a
// functionCall's args reach the tool call as parsed.
func marshalGeminiChunk(chunk map[string]any, source []byte, read geminiChunkRead) ([]byte, geminiChunkRead, bool) {
	out, err := orderedjson.MarshalInSourceOrder(chunk, source)
	if err != nil {
		return nil, read, false
	}
	return out, read, true
}

// normalizeGeminiParts is the parts the loop handles before it throws, and what it throws.
func normalizeGeminiParts(value any) (parts []any, thrown error, ok bool) {
	if !jsTruthy(value) {
		// `if (candidate?.content?.parts)`
		return nil, nil, true
	}
	switch value := value.(type) {
	case string:
		// Each character is a part with no members.
		return nil, nil, true
	case []any:
		parts = make([]any, 0, len(value))
		for _, element := range value {
			if element == nil {
				return parts, errors.New("Cannot read properties of null (reading 'text')"), true
			}
			fields, isObject := element.(map[string]any)
			if !isObject {
				parts = append(parts, map[string]any{})
				continue
			}
			part, ok := normalizeGeminiPart(fields)
			if !ok {
				return nil, nil, false
			}
			parts = append(parts, part)
		}
		return parts, nil, true
	}
	return nil, errors.New("candidate.content.parts is not iterable"), true
}

func normalizeGeminiPart(fields map[string]any) (map[string]any, bool) {
	part := map[string]any{}
	if text, present := fields["text"]; present {
		if text == nil {
			part["text"] = "null"
		} else {
			part["text"] = jsToString(text)
		}
	}
	if thought, isBool := fields["thought"].(bool); isBool {
		part["thought"] = thought
	}
	call, hasCall := fields["functionCall"]
	hasCall = hasCall && jsTruthy(call)
	if signature, present := fields["thoughtSignature"]; present && signature != nil {
		if isJSString(signature) {
			part["thoughtSignature"] = signature
		} else if hasCall && jsTruthy(signature) {
			// `...(part.thoughtSignature && { thoughtSignature })` stores it raw on the tool call.
			return nil, false
		}
	}
	if hasCall {
		callFields, isObject := call.(map[string]any)
		if !isObject {
			callFields = map[string]any{}
		}
		read := map[string]any{}
		for _, key := range []string{"name", "args"} {
			if member, present := callFields[key]; present {
				read[key] = member
			}
		}
		if id, present := callFields["id"]; present && id != nil {
			if isJSString(id) {
				read["id"] = id
			} else if jsTruthy(id) {
				// A truthy id is the tool call's id, raw.
				return nil, false
			}
		}
		part["functionCall"] = read
	}
	for _, key := range []string{"functionResponse", "inlineData"} {
		if member, present := fields[key]; present {
			part[key] = member
		}
	}
	return part, true
}

// normalizeGeminiUsage rewrites usageMetadata as the chunk loop reads it: a truthy value that is not an object reads every count as undefined, a falsy
// count reads 0, and the prompt count is only subtracted from and the candidates count only added to.
func normalizeGeminiUsage(chunk map[string]any) bool {
	usage, present := chunk["usageMetadata"]
	if !present || usage == nil {
		return true
	}
	usageObject, isObject := usage.(map[string]any)
	if !isObject {
		if jsTruthy(usage) {
			chunk["usageMetadata"] = map[string]any{}
		} else {
			delete(chunk, "usageMetadata")
		}
		return true
	}
	if prompt, present := usageObject["promptTokenCount"]; present && prompt != nil {
		if _, isNumber := prompt.(json.Number); !isNumber {
			if !jsTruthy(prompt) {
				delete(usageObject, "promptTokenCount")
			} else {
				number, ok := jsIntegerNumber(prompt)
				if !ok {
					return false
				}
				usageObject["promptTokenCount"] = number
			}
		}
	}
	// candidatesTokenCount is only added to (`(candidates || 0) + (thoughts || 0)`), so true counts as 1; every other count is stored as read.
	if usageObject["candidatesTokenCount"] == true {
		usageObject["candidatesTokenCount"] = json.Number("1")
	}
	for _, key := range []string{"candidatesTokenCount", "totalTokenCount", "cachedContentTokenCount", "thoughtsTokenCount"} {
		if !numberOrFalsy(usageObject, key) {
			return false
		}
	}
	return true
}
