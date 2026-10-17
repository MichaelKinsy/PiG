package ai

import (
	"bytes"
	"container/list"
	"encoding/json"
	"math"
	"reflect"
	"runtime"
	"slices"
	"sync"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// upstream: .upstream/v0.99.1/packages/ai/src/utils/validation.ts:317-339 (validateToolArguments returns structuredClone of
// toolCall.arguments) and utils/json-parse.ts (parseStreamingJson). A JavaScript object enumerates its members in insertion
// order, integer-like keys first and ascending, so JSON.stringify of a tool call's arguments writes the order the model sent.
// A Go map has no order. ToolCall keeps the model's order in an unexported sidecar next to the map, as ToolSchema keeps
// parameterOrder. The sidecar is recorded only for an object whose order differs from sorted order, so a call whose objects
// are all sorted carries none. A member the caller adds to the map afterwards, or removes, never invents or drops a member:
// the order lists the members of the model's text, and members it does not list follow them in sorted order.

// ArgumentsJSON encodes Arguments in the member order the model sent them. A nil Arguments is the empty object, as Pi's
// arguments are always an object. The bytes may be shared with other calls of ArgumentsJSON: the caller must not modify them.
func (content ToolCall) ArgumentsJSON() ([]byte, error) {
	if content.Arguments == nil {
		if content.nonObjectArguments != nil {
			return slices.Clone(content.nonObjectArguments), nil
		}
		return []byte("{}"), nil
	}
	if encoded := argumentEncodings.find(content.Arguments, content.argumentOrder); encoded != nil {
		return encoded, nil
	}
	encoded, err := marshalInRecordedOrder(map[string]any(content.Arguments), content.argumentOrder)
	if err != nil {
		return nil, err
	}
	argumentEncodings.keep(content.Arguments, content.argumentOrder, encoded)
	return slices.Clip(encoded), nil
}

// A tool call's arguments are written into every later request, and a large write call's arguments are hundreds of kilobytes, so
// encoding them on every turn made each request cost a pass over every argument of the history. The first ArgumentsJSON of an
// arguments object (the agent's, at toolcall_end) keeps its bytes, and every later request, session line and copy of the call
// reuses them. Pi stringifies the live object on every request, and a caller may still write to the map in place, so the bytes
// are kept with a copy of the object's tree (its maps and slices; strings, numbers and booleans are shared) and serve a call only
// while its arguments still equal that copy, member for member, and it records the same member order. Comparing strings that were
// never replaced compares two pointers, so a hit costs a walk of the tree's members and no pass over its text.
// An encoding lives as long as the arguments object it encodes: the runtime releases it once the arguments are unreachable, so the cache
// holds the encodings of the history a session keeps and never those of calls it has dropped.
// pig additive (D112): Pi stringifies the arguments for each request; the bytes are the same.
const argumentEncodingCacheBytes = 16 << 20

// argumentEncoding is the bytes ArgumentsJSON wrote for an arguments object.
type argumentEncoding struct {
	key uintptr
	// seq numbers the encodings the cache has kept; with key it names this one to release, which holds no reference to the entry.
	seq uint64
	// order is the member order the bytes follow; the entry holds it, so its address names it while the entry lives.
	order schemaObjectOrder
	// snapshot is a copy of the arguments' tree when they were encoded.
	snapshot map[string]any
	encoded  []byte
	// release is the cleanup that forgets this entry when its arguments are unreachable; the cache stops it when it forgets the entry first.
	release runtime.Cleanup
}

func (entry *argumentEncoding) size() int { return 2*len(entry.encoded) + 256 }

// argumentEncodingCache holds the latest encodings, least recently used first out, keyed by the address of the arguments map. The
// address only finds a candidate: a map freed and another allocated at its address fails the comparison with the snapshot.
type argumentEncodingCache struct {
	mu      sync.Mutex
	limit   int
	held    int
	kept    uint64
	entries map[uintptr]*list.Element
	recency list.List // front is the most recently used *argumentEncoding
}

var argumentEncodings = &argumentEncodingCache{limit: argumentEncodingCacheBytes, entries: map[uintptr]*list.Element{}}

func argumentsKey(arguments JsonObject) uintptr { return reflect.ValueOf(arguments).Pointer() }

// find returns the bytes kept for arguments written in order, or nil.
func (c *argumentEncodingCache) find(arguments JsonObject, order schemaObjectOrder) []byte {
	c.mu.Lock()
	element, ok := c.entries[argumentsKey(arguments)]
	if !ok {
		c.mu.Unlock()
		return nil
	}
	c.recency.MoveToFront(element)
	entry := element.Value.(*argumentEncoding)
	c.mu.Unlock()
	// The entry is never modified after it is kept, so it is read outside the lock.
	if !sameOrder(entry.order, order) || !sameArgumentTree(map[string]any(arguments), entry.snapshot) {
		return nil
	}
	return slices.Clip(entry.encoded)
}

// keep remembers encoded as the bytes of arguments written in order, when the arguments' tree is one the comparison can check.
func (c *argumentEncodingCache) keep(arguments JsonObject, order schemaObjectOrder, encoded []byte) {
	snapshot, ok := snapshotArgumentObject(arguments)
	if !ok {
		return
	}
	entry := &argumentEncoding{key: argumentsKey(arguments), order: order, snapshot: snapshot, encoded: encoded}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kept++
	entry.seq = c.kept
	// The entry holds a copy of the arguments' tree, never the arguments' own map, so it does not keep that map reachable.
	entry.release = runtime.AddCleanup(argumentsObject(arguments), c.release, argumentEncodingName{key: entry.key, seq: entry.seq})
	if previous, ok := c.entries[entry.key]; ok {
		c.forget(previous)
	}
	c.entries[entry.key] = c.recency.PushFront(entry)
	c.held += entry.size()
	for c.held > c.limit && c.recency.Len() > 1 {
		c.forget(c.recency.Back())
	}
}

// argumentsObject is the heap object that holds the members of arguments, which a tool call and its copies share. AddCleanup forces it to the
// heap, so a map the caller built on its stack is moved there with it. Nothing reads or writes through the pointer.
func argumentsObject(arguments JsonObject) *byte {
	return (*byte)(reflect.ValueOf(arguments).UnsafePointer())
}

// argumentEncodingName names one kept encoding without referring to it, so the cleanup that releases it does not keep its bytes alive after the
// cache evicted it.
type argumentEncodingName struct {
	key uintptr
	seq uint64
}

// release forgets the named encoding once the arguments it encodes are gone, unless it was evicted or a later encoding of the same address
// replaced it.
func (c *argumentEncodingCache) release(name argumentEncodingName) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[name.key]; ok && element.Value.(*argumentEncoding).seq == name.seq {
		c.forget(element)
	}
}

func (c *argumentEncodingCache) forget(element *list.Element) {
	entry := c.recency.Remove(element).(*argumentEncoding)
	delete(c.entries, entry.key)
	c.held -= entry.size()
	entry.release.Stop()
}

// sameOrder reports whether two member orders are the same map: an order is never modified once recorded.
func sameOrder(a, b schemaObjectOrder) bool {
	return reflect.ValueOf(a).UnsafePointer() == reflect.ValueOf(b).UnsafePointer()
}

// snapshotArgumentObject copies the maps and slices of a tree of decoded JSON (nil, booleans, strings, finite float64 numbers,
// []any, map[string]any and JsonObject), sharing its scalars. ok is false for any other tree, a cyclic one included, whose bytes are
// then not kept.
func snapshotArgumentObject(object JsonObject) (map[string]any, bool) {
	return snapshotArgumentMap(object, 0)
}

// argumentSnapshotDepth bounds the copy: a tree nested deeper (or a cycle) is not kept.
// pig additive (D112): Pi stringifies the arguments for each request; an argument tree that is not kept is stringified again and gives the same bytes.
const argumentSnapshotDepth = 256

func snapshotArgumentMap(object map[string]any, depth int) (map[string]any, bool) {
	if object == nil || depth > argumentSnapshotDepth {
		return nil, false
	}
	out := make(map[string]any, len(object))
	for key, item := range object {
		copied, ok := snapshotArgumentValue(item, depth+1)
		if !ok {
			return nil, false
		}
		out[key] = copied
	}
	return out, true
}

// snapshotArgumentValue copies one value. A nested JsonObject stays a JsonObject: the ordered encoder applies a recorded member
// order to a map[string]any only, so the two types can write one object differently.
func snapshotArgumentValue(value any, depth int) (any, bool) {
	switch typed := value.(type) {
	case nil, bool, string:
		return value, true
	case float64:
		return value, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case JsonObject:
		copied, ok := snapshotArgumentMap(typed, depth)
		return JsonObject(copied), ok
	case map[string]any:
		return snapshotArgumentMap(typed, depth)
	case []any:
		if typed == nil || depth > argumentSnapshotDepth {
			return nil, false
		}
		out := make([]any, len(typed))
		for index, item := range typed {
			copied, ok := snapshotArgumentValue(item, depth+1)
			if !ok {
				return nil, false
			}
			out[index] = copied
		}
		return out, true
	}
	return nil, false
}

// sameArgumentTree reports whether current holds what snapshot does: the same members, the same elements, and equal scalars,
// maps and slices of the same types.
func sameArgumentTree(current, snapshot any) bool {
	switch want := snapshot.(type) {
	case nil:
		return current == nil
	case bool:
		got, ok := current.(bool)
		return ok && got == want
	case string:
		got, ok := current.(string)
		return ok && got == want
	case float64:
		got, ok := current.(float64)
		return ok && got == want
	case map[string]any:
		got, ok := current.(map[string]any)
		return ok && sameArgumentMap(got, want)
	case JsonObject:
		got, ok := current.(JsonObject)
		return ok && sameArgumentMap(got, want)
	case []any:
		got, ok := current.([]any)
		if !ok || got == nil || len(got) != len(want) {
			return false
		}
		for index, item := range want {
			if !sameArgumentTree(got[index], item) {
				return false
			}
		}
		return true
	}
	return false
}

func sameArgumentMap(current, snapshot map[string]any) bool {
	if current == nil || len(current) != len(snapshot) {
		return false
	}
	for key, item := range snapshot {
		value, present := current[key]
		if !present || !sameArgumentTree(value, item) {
			return false
		}
	}
	return true
}

// encodedArguments is the bytes ArgumentsJSON kept, which are JSON.stringify text: written as they are in the JSON.stringify mode.
type encodedArguments []byte

func (arguments encodedArguments) MarshalJSON() ([]byte, error) { return arguments, nil }

// AppendStringify appends the bytes, which are already JSON.stringify text, to dst.
func (arguments encodedArguments) AppendStringify(dst []byte) ([]byte, error) {
	return append(dst, arguments...), nil
}

// ArgumentMember is one member of a tool call's arguments: its key and the text JSON.stringify gives its value.
type ArgumentMember struct {
	Key  string
	JSON string
}

// ArgumentMembers lists the members of Arguments as `Object.entries(arguments).map(([key, value]) => [key, JSON.stringify(value)])` does:
// integer-like keys ascending first, then the others in the order the model sent them, each value written as JSON.stringify writes it
// (no HTML escaping, negative zero as 0, finite numbers in JavaScript form, lone surrogates kept).
func (content ToolCall) ArgumentMembers() ([]ArgumentMember, error) {
	values := map[string]any(content.Arguments)
	members := make([]ArgumentMember, 0, len(values))
	for _, key := range orderedSchemaKeys(values, content.argumentOrder, "") {
		encoded, err := marshalSchemaWithOrder(values[key], content.argumentOrder, schemaPath("", key))
		if err != nil {
			return nil, err
		}
		members = append(members, ArgumentMember{Key: key, JSON: string(encoded)})
	}
	return members, nil
}

// SetStreamingArguments parses a streamed tool-argument text as ParseStreamingJson does and keeps its member order.
func (content *ToolCall) SetStreamingArguments(input string) {
	content.Arguments, content.argumentOrder, content.nonObjectArguments = parseStreamingJsonValue(input)
	content.deferredArguments = ""
}

// SetFinalArguments stores the complete arguments text of a finished tool call as Pi's parseToolCallArguments does: text that is not a valid JSON object leaves Arguments empty and ArgumentsError describing the problem, never the parsed prefix.
func (content *ToolCall) SetFinalArguments(input string) {
	content.Arguments, content.argumentOrder, content.ArgumentsError = parseToolCallArguments(input)
	content.nonObjectArguments = nil
	content.deferredArguments = ""
}

// DefaultFalsyArguments is `arguments || {}`: a non-object value JavaScript treats as false (null, 0, false, "") becomes the empty object; [] and
// any other value stay.
func (content *ToolCall) DefaultFalsyArguments() {
	if falsyArgumentsJSON(content.nonObjectArguments) {
		content.Arguments, content.argumentOrder, content.nonObjectArguments = JsonObject{}, nil, nil
	}
}

// CopyArgumentsFrom gives content other's arguments, a non-object value included.
func (content *ToolCall) CopyArgumentsFrom(other ToolCall) {
	content.Arguments, content.argumentOrder, content.nonObjectArguments = nil, other.argumentOrder, slices.Clone(other.nonObjectArguments)
	content.deferredArguments = other.deferredArguments
	content.ArgumentsError = other.ArgumentsError
	if other.Arguments != nil {
		content.Arguments = JsonObject(cloneJSONValue(map[string]any(other.Arguments)).(map[string]any))
	}
}

// defaultNullishArguments is `arguments ?? {}`: a null value becomes the empty object.
func (content *ToolCall) defaultNullishArguments() {
	if string(content.nonObjectArguments) == "null" {
		content.Arguments, content.argumentOrder, content.nonObjectArguments = JsonObject{}, nil, nil
	}
}

// argumentsJSONDefaulting is ArgumentsJSON of the call with the arguments a converter writes: `arguments ?? {}` when falsy is false, `arguments || {}`
// when it is true.
func (content ToolCall) argumentsJSONDefaulting(falsy bool) ([]byte, error) {
	if falsy {
		content.DefaultFalsyArguments()
	} else {
		content.defaultNullishArguments()
	}
	return content.ArgumentsJSON()
}

// argumentsValue is the arguments as a JSON value: the object, or the non-object value decoded.
func (content ToolCall) argumentsValue() any {
	if content.Arguments == nil && content.nonObjectArguments != nil {
		if value, ok := jsonParseValue(string(content.nonObjectArguments)); ok {
			return value
		}
	}
	return map[string]any(content.Arguments)
}

// falsyArgumentsJSON reports a JSON.stringify text JavaScript reads as false. -0 stringifies as 0.
func falsyArgumentsJSON(raw json.RawMessage) bool {
	switch string(raw) {
	case "null", "0", "false", `""`:
		return true
	}
	return false
}

// setParsedArguments stores what a streaming parse returned.
func (content *ToolCall) setParsedArguments(arguments JsonObject, order schemaObjectOrder, nonObject json.RawMessage) {
	content.Arguments, content.argumentOrder, content.nonObjectArguments = arguments, order, nonObject
	content.deferredArguments = ""
}

// deferArguments records text, the call's streamed argument text so far, in place of its parse: Arguments keeps its
// earlier value until a reader of a published copy settles the call (assistantMessageCell.settleLocked), which parses text as
// setParsedArguments would have stored it, or until a later parse replaces it. text must not be empty.
func (content *ToolCall) deferArguments(text string) {
	content.deferredArguments = text
}

// compactNonObjectArguments returns the JSON text of arguments that are not an object, as JSON.stringify writes the parsed value, and nil for an object or for text that is not JSON.
func compactNonObjectArguments(raw json.RawMessage) json.RawMessage {
	text := bytes.TrimSpace(raw)
	if len(text) == 0 || text[0] == '{' {
		return nil
	}
	value, ok := jsonParseValue(string(text))
	if !ok {
		return nil
	}
	order, err := readSchemaObjectOrder(text)
	if err != nil {
		order = nil
	}
	return nonObjectArgumentsJSON(value, order)
}

// nonObjectArgumentsJSON writes a parsed non-object value as JSON.stringify does.
func nonObjectArgumentsJSON(value any, order schemaObjectOrder) json.RawMessage {
	encoded, err := marshalSchemaWithOrder(nonFiniteToNull(value), order, "")
	if err != nil {
		return json.RawMessage("null")
	}
	return encoded
}

// SetPartialJson records text as the call's `partialJson` member, the argument text an unfinished streamed call has received so far; JSON shows it until DeletePartialJson. Pi keeps the member on the block it streams into, so a message delivered before the call ends carries it.
func (content *ToolCall) SetPartialJson(text string) {
	content.scratch.partialJson, content.scratch.hasPartialJson = text, true
}

// DeletePartialJson removes the `partialJson` member, as `delete toolCall.partialJson` does when the call ends.
func (content *ToolCall) DeletePartialJson() {
	content.scratch.partialJson, content.scratch.hasPartialJson = "", false
}

// SetArgumentsJSON decodes a complete JSON object into Arguments and keeps its member order. A text that is not a JSON object is an error and leaves the call unchanged.
func (content *ToolCall) SetArgumentsJSON(raw []byte) error {
	var decoded JsonObject
	if err := sdkjson.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	order, err := readSchemaObjectOrder(raw)
	if err != nil {
		return err
	}
	content.Arguments, content.argumentOrder = decoded, order
	return nil
}

// MarshalJSONInSourceOrder encodes value with the object member order of the JSON text source. Members source does not have follow in sorted order. A source that is not JSON gives sorted order.
func MarshalJSONInSourceOrder(value any, source []byte) ([]byte, error) {
	order, err := readSchemaObjectOrder(source)
	if err != nil {
		order = nil
	}
	return marshalInRecordedOrder(value, order)
}

// marshalInRecordedOrder encodes value as JSON.stringify does, with order's member order; a nil order is sorted order with integer-like keys first.
func marshalInRecordedOrder(value any, order schemaObjectOrder) ([]byte, error) {
	// The ordered walk recurses without bound, so a cycle or a value with no JSON form is rejected first.
	if err := validateJsonValue(value); err != nil {
		return nil, err
	}
	return marshalSchemaWithOrder(value, order, "")
}
