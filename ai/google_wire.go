package ai

import (
	"bytes"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The @google/genai SDK does not send the params Pi's buildParams hands it: generateContentParametersToMldev and the converters it calls
// rebuild every object member by member, copying a member only when it is neither null nor undefined. The functions here write a params
// value the same way, so the body keeps the SDK's member order whatever order the params (or a before_provider_request replacement) use.
//
// PiG has no @google/genai dependency, but the tables below are a port of its converters, so an SDK update in Pi is a port obligation for
// this file: compare the converters reachable from generateContentParametersToMldev and generateContentParametersToVertex (dist/node/index.mjs)
// between the two versions, member by member. geminiMirroredSDKVersion names the version mirrored; `make pin-check` and
// TestGoogleWireMirrorsTheInstalledSDK fail when it is not the version the parity oracle installs.

// geminiMirroredSDKVersion is the @google/genai version whose converters the tables mirror.
const geminiMirroredSDKVersion = "2.32.0"

// geminiWireMember is one member of a rebuilt object; value is JSON.
type geminiWireMember struct {
	key   string
	value json.RawMessage
}

// geminiWireObject writes its members in order.
type geminiWireObject []geminiWireMember

func (object geminiWireObject) MarshalJSON() ([]byte, error) {
	buf := []byte{'{'}
	for i, member := range object {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(append(jsstring.AppendString(buf, member.key), ':'), member.value...)
	}
	return append(buf, '}'), nil
}

// geminiWireDialect is the member order one SDK backend writes: the Gemini API (generateContentParametersToMldev and the *ToMldev
// converters) or Vertex (the *ToVertex converters).
type geminiWireDialect struct {
	// topLevel is contents, then the config members generateContentConfigTo* moves to the body, then generationConfig, which is set
	// after the config conversion returns.
	topLevel         []string
	generationConfig []string
	part             []string
	// partMembers are the members of the part values the backend converts; the other part values are copied as they are, in the
	// member order the params give them (inlineData and fileData on both backends since SDK 2.32.0, which dropped blobToMldev and
	// fileDataToMldev from partToMldev: Pi's own image parts are mimeType then data, google-shared.ts convertMessages).
	partMembers map[string][]string
	// functionResponsePart is the member order of each part of a functionResponse the backend converts (functionResponsePartToVertex).
	functionResponsePart []string
}

var geminiAPIWire = geminiWireDialect{
	topLevel:         []string{"contents", "serviceTier", "systemInstruction", "safetySettings", "tools", "toolConfig", "labels", "cachedContent", "continuationToken", "store", "generationConfig", "modelArmorConfig"},
	generationConfig: []string{"temperature", "topP", "topK", "candidateCount", "maxOutputTokens", "stopSequences", "responseLogprobs", "logprobs", "presencePenalty", "frequencyPenalty", "seed", "responseMimeType", "responseSchema", "responseJsonSchema", "responseModalities", "mediaResolution", "speechConfig", "thinkingConfig", "audioTranscriptionConfig", "imageConfig", "enableEnhancedCivicAnswers"},
	part:             []string{"mediaResolution", "toolCall", "toolResponse", "audioTranscription", "codeExecutionResult", "executableCode", "fileData", "functionCall", "functionResponse", "inlineData", "text", "thought", "thoughtSignature", "videoMetadata", "partMetadata", "mediaProcessing", "speechMetadata"},
	partMembers: map[string][]string{
		"functionCall": {"args", "id", "name"},
	},
}

var geminiVertexWire = geminiWireDialect{
	topLevel:         []string{"contents", "serviceTier", "systemInstruction", "safetySettings", "tools", "toolConfig", "labels", "cachedContent", "modelArmorConfig", "continuationToken", "outputConfig", "generationConfig"},
	generationConfig: []string{"temperature", "topP", "topK", "candidateCount", "maxOutputTokens", "stopSequences", "responseLogprobs", "logprobs", "presencePenalty", "frequencyPenalty", "seed", "responseMimeType", "responseSchema", "responseJsonSchema", "routingConfig", "modelConfig", "responseModalities", "mediaResolution", "speechConfig", "audioTimestamp", "thinkingConfig", "audioTranscriptionConfig", "imageConfig"},
	part:             []string{"mediaResolution", "audioTranscription", "codeExecutionResult", "executableCode", "fileData", "functionCall", "functionResponse", "inlineData", "text", "thought", "thoughtSignature", "videoMetadata", "mediaProcessing", "speechMetadata"},
	partMembers: map[string][]string{
		"functionResponse": {"id", "name", "parts", "response", "scheduling"},
	},
	functionResponsePart: []string{"fileData", "inlineData"},
}

func geminiWireFor(api API) *geminiWireDialect {
	if api == APIGoogleVertex {
		return &geminiVertexWire
	}
	return &geminiAPIWire
}

func isJSONNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

// geminiObjectMembers decodes a JSON object; ok is false for any other value.
func geminiObjectMembers(value json.RawMessage) (map[string]json.RawMessage, bool) {
	var members map[string]json.RawMessage
	if json.Unmarshal(value, &members) != nil || members == nil {
		return nil, false
	}
	return members, true
}

// geminiPick rebuilds members with the given keys in order, skipping absent and null ones. lower rewrites a picked value.
func geminiPick(members map[string]json.RawMessage, keys []string, lower func(key string, value json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	object := make(geminiWireObject, 0, len(keys))
	for _, key := range keys {
		value, ok := members[key]
		if !ok || len(value) == 0 || isJSONNull(value) {
			continue
		}
		if lower != nil {
			var err error
			if value, err = lower(key, value); err != nil {
				return nil, err
			}
		}
		object = append(object, geminiWireMember{key, value})
	}
	return object.MarshalJSON()
}

// lowerPart writes a part as partToMldev or partToVertex does. A value that is not an object is the SDK's input as it is.
func (dialect *geminiWireDialect) lowerPart(part json.RawMessage) (json.RawMessage, error) {
	members, ok := geminiObjectMembers(part)
	if !ok {
		return part, nil
	}
	return geminiPick(members, dialect.part, func(key string, value json.RawMessage) (json.RawMessage, error) {
		keys, converted := dialect.partMembers[key]
		if !converted {
			return value, nil
		}
		inner, ok := geminiObjectMembers(value)
		if !ok {
			return value, nil
		}
		return geminiPick(inner, keys, func(member string, value json.RawMessage) (json.RawMessage, error) {
			if key != "functionResponse" || member != "parts" {
				return value, nil
			}
			// functionResponseToVertex converts each part: fileData, then inlineData, each copied as it is.
			return lowerGeminiArray(value, func(part json.RawMessage) (json.RawMessage, error) {
				members, ok := geminiObjectMembers(part)
				if !ok {
					return part, nil
				}
				return geminiPick(members, dialect.functionResponsePart, nil)
			})
		})
	})
}

// lowerContent writes a content as contentToMldev and contentToVertex do: parts, each lowered, then role.
func (dialect *geminiWireDialect) lowerContent(content json.RawMessage) (json.RawMessage, error) {
	members, ok := geminiObjectMembers(content)
	if !ok {
		return content, nil
	}
	return geminiPick(members, []string{"parts", "role"}, func(key string, value json.RawMessage) (json.RawMessage, error) {
		if key != "parts" {
			return value, nil
		}
		return lowerGeminiArray(value, dialect.lowerPart)
	})
}

// lowerGeminiArray lowers each element of a JSON array; any other value is kept.
func lowerGeminiArray(value json.RawMessage, lower func(json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	var elements []json.RawMessage
	if json.Unmarshal(value, &elements) != nil || elements == nil {
		return value, nil
	}
	for i, element := range elements {
		lowered, err := lower(element)
		if err != nil {
			return nil, err
		}
		elements[i] = lowered
	}
	return json.Marshal(elements)
}

// lowerBody writes the REST body for body members already split out of the params: contents and systemInstruction are rebuilt,
// generationConfig is rebuilt in its member order, and the body members follow the dialect's order.
func (dialect *geminiWireDialect) lowerBody(body map[string]json.RawMessage) (json.RawMessage, error) {
	return geminiPick(body, dialect.topLevel, func(key string, value json.RawMessage) (json.RawMessage, error) {
		switch key {
		case "contents":
			return lowerGeminiArray(value, dialect.lowerContent)
		case "systemInstruction":
			return dialect.lowerContent(value)
		case "generationConfig":
			members, ok := geminiObjectMembers(value)
			if !ok {
				return value, nil
			}
			return geminiPick(members, dialect.generationConfig, nil)
		}
		return value, nil
	})
}

// wireBody writes the REST body the SDK's api backend sends for the request's params.
func (request geminiRequest) wireBody(api API) (json.RawMessage, error) {
	// A functionCall and a functionResponse part name a tool, and the name can hold a lone UTF-16 surrogate that Pi does not sanitize (google-shared.ts
	// convertMessages): the request is written as JSON.stringify writes it.
	native, err := jsstring.MarshalKeepingSurrogates(request)
	if err != nil {
		return nil, err
	}
	members, _ := geminiObjectMembers(native)
	return geminiWireFor(api).lowerBody(members)
}
