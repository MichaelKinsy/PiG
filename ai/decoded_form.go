package ai

import (
	"math"
	"reflect"
	"slices"
	"unicode/utf8"
	"unsafe"
)

// pig additive (D112): DecodedFormBlock and DecodedFormUsage let the session store a message it just encoded without decoding the record again.

// DecodedFormBlock is the block decoding the JSON encoding of block yields, when that can be proven without encoding or decoding: a text, thinking, image or tool call block that holds no streaming scratch, only valid UTF-8 text and, for a tool call, arguments that are an object of plain JSON values (nil, bool, finite float64 other than negative zero, string, []any, map[string]any). It reports false for anything else, and the caller decodes the record.
func DecodedFormBlock(block ContentBlock) (ContentBlock, bool) {
	switch value := block.(type) {
	case TextContent:
		if value.scratch != "" || !utf8.ValidString(value.Text) || !utf8.ValidString(value.TextSignature) {
			return nil, false
		}
		return value, true
	case ThinkingContent:
		if value.scratch != "" || !utf8.ValidString(value.Thinking) || !utf8.ValidString(value.ThinkingSignature) {
			return nil, false
		}
		// Decoding sets the empty-signature mark only for a signature that is present and empty.
		value.thinkingSignatureEmpty = value.thinkingSignatureEmpty && value.ThinkingSignature == ""
		return value, true
	case ImageContent:
		if !utf8.ValidString(value.Data) || !utf8.ValidString(value.MimeType) {
			return nil, false
		}
		return value, true
	case ToolCall:
		if value.scratch != (toolCallScratch{}) || value.nonObjectArguments != nil || value.ArgumentsError != "" || value.Arguments == nil {
			return nil, false
		}
		if !utf8.ValidString(value.ID) || !utf8.ValidString(value.Name) || !utf8.ValidString(value.ThoughtSignature) || !utf8.ValidString(value.Namespace) {
			return nil, false
		}
		path := []unsafe.Pointer{reflect.ValueOf(value.Arguments).UnsafePointer()}
		for key, member := range value.Arguments {
			if !utf8.ValidString(key) || !plainJSONValue(member, path) {
				return nil, false
			}
		}
		// Decoding records the member order the arguments' text is written in, which the order walk reads without writing the text.
		return ToolCall{ID: value.ID, Name: value.Name, Arguments: cloneDecodedObject(value.Arguments), ThoughtSignature: value.ThoughtSignature,
			Namespace: value.Namespace, argumentOrder: writtenSchemaObjectOrder(value.Arguments, value.argumentOrder)}, true
	}
	return nil, false
}

// plainJSONValue reports whether v is a value that encodes to JSON and decodes back to itself: nil, bool, a finite float64 other than negative zero, a valid UTF-8 string, and []any and map[string]any of those.
// path holds the maps and slices that contain v. A value that contains itself is not plain: encoding it fails, so the caller takes
// the encode/decode path, which reports the cycle as encoding/json does.
func plainJSONValue(v any, path []unsafe.Pointer) bool {
	switch value := v.(type) {
	case nil, bool:
		return true
	case float64:
		return !math.IsNaN(value) && !math.IsInf(value, 0) && !(value == 0 && math.Signbit(value))
	case string:
		return utf8.ValidString(value)
	case []any:
		if value == nil {
			return false
		}
		if len(value) == 0 {
			return true
		}
		self := unsafe.Pointer(unsafe.SliceData(value)) //nolint:gosec // G103: the backing array's address is the slice's identity on the path; it is only compared
		if slices.Contains(path, self) {
			return false
		}
		path = append(path, self)
		for _, item := range value {
			if !plainJSONValue(item, path) {
				return false
			}
		}
		return true
	case map[string]any:
		if value == nil {
			return false
		}
		self := reflect.ValueOf(value).UnsafePointer()
		if slices.Contains(path, self) {
			return false
		}
		path = append(path, self)
		for key, item := range value {
			if !utf8.ValidString(key) || !plainJSONValue(item, path) {
				return false
			}
		}
		return true
	}
	return false
}

// DecodedFormUsage is the usage decoding the JSON encoding of usage yields, and false when a cost is not a finite number other than negative zero.
func DecodedFormUsage(usage Usage) (Usage, bool) {
	for _, cost := range [...]float64{usage.Cost.Input, usage.Cost.Output, usage.Cost.CacheRead, usage.Cost.CacheWrite, usage.Cost.Total} {
		if math.IsNaN(cost) || math.IsInf(cost, 0) || (cost == 0 && math.Signbit(cost)) {
			return Usage{}, false
		}
	}
	if usage.CacheWrite1h == nil && usage.Reasoning == nil {
		usage.splitsAfterCost = false
	}
	if usage.CacheWrite1h != nil {
		value := *usage.CacheWrite1h
		usage.CacheWrite1h = &value
	}
	if usage.Reasoning != nil {
		value := *usage.Reasoning
		usage.Reasoning = &value
	}
	return usage, true
}

func cloneDecodedObject(object JsonObject) JsonObject {
	out := make(JsonObject, len(object))
	for key, value := range object {
		out[key] = cloneDecoded(value)
	}
	return out
}

func cloneDecoded(v any) any {
	switch value := v.(type) {
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = cloneDecoded(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = cloneDecoded(item)
		}
		return out
	}
	return v
}
