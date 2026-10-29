package link

import (
	"encoding/base64"
	"errors"
	"math"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/src/next/link/values.ts. Values in messages (§3 values, §22): JSON text, and for payloads
// marked as carrying bytes raw byte segments, a `[]byte` leaf written as `{"$bytes": i}` and read back as a view of
// the received message (no copy, never base64); a part as the text codec splices it. Shared by the codecs.
//
// Values are the JSON values of the delta package (nil, bool, float64, string, []any, *delta.JsonObject), with
// `[]byte` leaves in payloads marked as carrying bytes (and map[string]any accepted as an object).

// JsonPart is a JSON part at its origin: the value's JSON text (state and params are JSON only).
func JsonPart(value any) (Text, error) {
	text, err := chordjson.Marshal(value)
	if err != nil {
		return "", err
	}
	return Text(text), nil
}

// Payload is a payload part at its origin: its JSON and, when its method's schema has a `bytes()` leaf, its byte
// segments (nil otherwise).
type Payload struct {
	Json     string
	Segments [][]byte
}

// PayloadPart is a payload part at its origin (arguments, a result): its JSON and, when bytes (its method's schema has
// a `bytes()` leaf), its byte segments: each `[]byte` leaf becomes a segment, and an own key `$bytes`, reserved there,
// fails `invalid/args`; otherwise plain JSON, where `$bytes` is ordinary data. nil (a handler returning nothing) is
// `null`.
func PayloadPart(value any, bytes bool) (Payload, error) {
	if !bytes {
		text, err := JsonPart(value)
		return Payload{Json: string(text)}, err
	}
	segments := [][]byte{}
	replaced, err := replaceLeaves(value, &segments)
	if err != nil {
		return Payload{}, err
	}
	text, err := JsonPart(replaced)
	return Payload{Json: string(text), Segments: segments}, err
}

// replaceLeaves is JSON.stringify's replacer: each []byte becomes a `{"$bytes": i}` marker over its segment, and an
// object with an own `$bytes` key is refused (the replacer does not see the markers it returns, only their fields).
func replaceLeaves(value any, segments *[][]byte) (any, error) {
	switch typed := value.(type) {
	case []byte:
		*segments = append(*segments, typed)
		return delta.JsonObjectOf("$bytes", float64(len(*segments)-1)), nil
	case []any:
		replaced := make([]any, len(typed))
		for index, item := range typed {
			item, err := replaceLeaves(item, segments)
			if err != nil {
				return nil, err
			}
			replaced[index] = item
		}
		return replaced, nil
	case *delta.JsonObject:
		if typed == nil {
			return nil, nil
		}
		if typed.Has("$bytes") {
			return nil, reservedBytes()
		}
		replaced := delta.NewJsonObject(typed.Len())
		for key, item := range typed.All() {
			item, err := replaceLeaves(item, segments)
			if err != nil {
				return nil, err
			}
			replaced.Set(key, item)
		}
		return replaced, nil
	case map[string]any:
		if _, has := typed["$bytes"]; has {
			return nil, reservedBytes()
		}
		replaced := delta.NewJsonObject(len(typed))
		for _, key := range chordjson.MapOwnKeys(typed) {
			item, err := replaceLeaves(typed[key], segments)
			if err != nil {
				return nil, err
			}
			replaced.Set(key, item)
		}
		return replaced, nil
	}
	return value, nil
}

func reservedBytes() error {
	return data.Invalid(data.ReasonArgs, `"$bytes" is reserved in values crossing a link`)
}

// ParsePart parses a part's JSON (Text or Bytes) where it is owned, once opened (OpenPart). It returns an error for a
// part that is no strict UTF-8 JSON (a protocol error).
func ParsePart(json Part) (any, error) {
	switch typed := json.(type) {
	case Text:
		return parseJSONText(string(typed))
	case Bytes:
		return parseJson(typed)
	}
	return nil, errors.New("values: a part that was not opened")
}

// ParsePayload parses a payload part's JSON where it is owned, once opened, its `{"$bytes": i}` references resolved to
// views of segments when it has them. It returns an error for a malformed part (a protocol error).
func ParsePayload(json Part, segments [][]byte) (any, error) {
	value, err := ParsePart(json)
	if err != nil || segments == nil {
		return value, err
	}
	return ResolveSegments(value, segments)
}

func parseJSONText(text string) (any, error) { return parseJson([]byte(text)) }

// parseJson is strict UTF-8 JSON; a leading U+FEFF is data, not a byte order mark to strip. It returns an error for
// anything else (a protocol error).
func parseJson(bytes []byte) (any, error) {
	if !utf8.Valid(bytes) {
		return nil, errors.New("The encoded data was not valid for encoding utf-8")
	}
	return chordjson.Decode(bytes)
}

// readUtf8 is strict UTF-8 text. It returns an error on invalid UTF-8 (a protocol error).
func readUtf8(bytes []byte) (string, error) {
	if !utf8.Valid(bytes) {
		return "", errors.New("The encoded data was not valid for encoding utf-8")
	}
	return string(bytes), nil
}

// partText is a part as the text codec splices it into its tuple, tagged so that no JSON value is reserved: `[0, json]`
// for a plain part, `[1, base64]` for a deflated one. It returns an error on invalid UTF-8.
func partText(part Part) (string, error) {
	switch typed := part.(type) {
	case *DeflatedPart:
		return `[1,"` + base64.StdEncoding.EncodeToString(typed.Bytes) + `"]`, nil
	case Text:
		return "[0," + string(typed) + "]", nil
	case Bytes:
		text, err := readUtf8(typed)
		return "[0," + text + "]", err
	}
	return "", errors.New("text: a part is missing")
}

// textPart is a part from its place in a text tuple (partText reversed). It returns an error on anything else (a
// protocol error).
func textPart(value any) (Part, error) {
	// Trust boundary: fresh JSON from the text decoder.
	pair, ok := value.([]any)
	if !ok || len(pair) != 2 {
		return nil, errors.New("text: a part is no [codec, payload] pair")
	}
	if codec, isNumber := pair[0].(float64); isNumber && codec == 0 {
		return JsonPart(pair[1])
	}
	if codec, isNumber := pair[0].(float64); isNumber && codec == 1 {
		if payload, isString := pair[1].(string); isString {
			bytes, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				return nil, errors.New("text: a part that is no base64")
			}
			return NewDeflatedPart(bytes), nil
		}
	}
	return nil, errors.New("text: a part of an unknown codec")
}

// ResolveSegments replaces every `{"$bytes": i}` in a freshly decoded payload with segment i, in place. It returns an
// error for a malformed reference (a protocol error).
func ResolveSegments(value any, segments [][]byte) (any, error) {
	switch node := value.(type) {
	case []any:
		for index, child := range node {
			resolved, err := ResolveSegments(child, segments)
			if err != nil {
				return nil, err
			}
			node[index] = resolved
		}
		return node, nil
	case *delta.JsonObject:
		if node.Has("$bytes") {
			index, isNumber := node.Value("$bytes").(float64)
			if node.Len() != 1 || !isNumber || index < 0 || index != math.Trunc(index) || index >= float64(len(segments)) {
				return nil, errors.New("payload: a malformed byte segment reference")
			}
			return segments[int(index)], nil
		}
		for _, key := range node.Keys() {
			child := node.Value(key)
			resolved, err := ResolveSegments(child, segments)
			if err != nil {
				return nil, err
			}
			// Set, not assigned by name: a key `__proto__` is an own member of parsed JSON.
			node.Set(key, resolved)
		}
		return node, nil
	}
	return value, nil
}
