package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// A response.output_item.added or .done item that is valid JSON but holds a member of an unexpected type is read by Pi's JavaScript member by member
// (openai-responses-shared.ts createSlot :463-540 and the output_item.done branch :683-741). normalizeResponsesItemJS rewrites such an item into the shape the
// typed decoder expects for the cases whose JS result it reproduces:
//   - an item that is not an object, or whose type is not a string, matches no branch;
//   - `${item.call_id}|${item.id}` interpolates the String() form of each;
//   - a done message joins `content.map((c) => (c.type === "output_text" ? c.text : c.refusal)).join("")`, and a done reasoning item joins `summary.map((s) =>
//     s.text)` and `content.map((c) => c.text)` with "\n\n", where join writes "" for null and undefined and String() for any other value, and a part that is
//     not an object reads every member as undefined;
//   - `item.arguments || ...` skips a falsy value, and an added item's truthy non-string arguments reads as its String() form.
//
// A message's id and phase are read only into the text signature, which takes them from the item as sent (responsesTextSignature), so a non-string one is
// dropped here. A member Pi stores raw (a name, a namespace, a reasoning id, a custom tool call's members) is not rewritten, so the item stays undecodable
// and the turn ends with the decode error. thrown is the TypeError Pi throws reading the done item: a content or summary that is not an array
// (`item.content?.map is not a function`), a null part, or a truthy function_call arguments that is not a string (parseToolCallArguments's
// `json?.trim is not a function`).
// upstream: openai-responses-shared.ts:463-741
func normalizeResponsesItemJS(raw []byte, done bool) (normalized []byte, thrown error, ok bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, nil, false
	}
	item, isObject := value.(map[string]any)
	if !isObject {
		return []byte("{}"), nil, true
	}
	kind, _ := item["type"].(string)
	read := map[string]any{}
	if kind != "" {
		read["type"] = kind
	}
	copyRaw := func(keys ...string) bool {
		for _, key := range keys {
			member, present := item[key]
			if !present || member == nil {
				continue
			}
			if _, isString := member.(string); !isString {
				return false
			}
			read[key] = member
		}
		return true
	}
	switch kind {
	case "message":
		for _, key := range []string{"id", "phase"} {
			if member, isString := item[key].(string); isString {
				read[key] = member
			}
		}
		if done {
			read["content"], thrown = jsResponsesParts(item, "content", "type")
		}
	case "reasoning":
		if !copyRaw("id") {
			return nil, nil, false
		}
		if done {
			for _, key := range []string{"summary", "content"} {
				if read[key], thrown = jsResponsesParts(item, key, "text"); thrown != nil {
					break
				}
			}
		}
	case "function_call":
		if !copyRaw("name", "namespace") {
			return nil, nil, false
		}
		for _, key := range []string{"call_id", "id"} {
			if member, present := item[key]; present {
				if member != nil {
					member = jsToString(member)
				}
				read[key] = member
			}
		}
		if arguments, present := item["arguments"]; present && arguments != nil {
			switch {
			case !jsTruthy(arguments):
			case isJSString(arguments):
				read["arguments"] = arguments
			case done:
				thrown = errors.New("json?.trim is not a function")
			default:
				// createSlot keeps `item.arguments || ""` as it is, and a delta appends to its String() form; the caller records that it is not a
				// string yet (responsesArgumentsNotString).
				read["arguments"] = jsToString(arguments)
			}
		}
	case "custom_tool_call":
		return nil, nil, false
	}
	// JSON.parse keeps the item's member order; the rewritten item keeps it too.
	out, err := orderedjson.MarshalInSourceOrder(read, raw)
	if err != nil {
		return nil, nil, false
	}
	return out, thrown, true
}

func isJSString(value any) bool {
	_, isString := value.(string)
	return isString
}

// jsResponsesParts is the parts of `item[key]?.map(...)` as respOutputContent decodes them: each part keeps the String() form of its text and refusal,
// and the type when it is a string. A null or absent member has no parts.
func jsResponsesParts(item map[string]any, key, firstRead string) ([]any, error) {
	member, present := item[key]
	if !present || member == nil {
		return nil, nil
	}
	elements, isArray := member.([]any)
	if !isArray {
		return nil, errors.New("item." + key + "?.map is not a function")
	}
	parts := make([]any, len(elements))
	for i, element := range elements {
		if element == nil {
			return nil, errors.New("Cannot read properties of null (reading '" + firstRead + "')")
		}
		part := map[string]any{}
		if fields, isObject := element.(map[string]any); isObject {
			if kind, isString := fields["type"].(string); isString {
				part["type"] = kind
			}
			for _, field := range []string{"text", "refusal"} {
				if value := fields[field]; value != nil {
					part[field] = jsToString(value)
				}
			}
		}
		parts[i] = part
	}
	return parts, nil
}

// decodeResponsesItem decodes a streamed output item into item and returns the bytes it was decoded from. An item the typed decode rejects for a member's
// type takes the JS read. err is the error that ends the turn for an item neither reads: `event.item.type` on a missing or null item throws Pi's TypeError,
// and an item whose mistyped member Pi stores raw ends it with the decode error. thrown is the TypeError Pi throws for the done item.
func decodeResponsesItem(raw json.RawMessage, done bool, item *respOutputItem) (decoded json.RawMessage, thrown, err error) {
	if err := responsesItemMissing(raw); err != nil {
		return nil, nil, err
	}
	err = json.Unmarshal(raw, item)
	if err == nil {
		if done && hasEmptyResponsesPart(item) {
			// The typed decode reads a null part as an empty one, where reading its first member throws.
			_, thrown, _ = normalizeResponsesItemJS(raw, true)
		}
		return raw, thrown, nil
	}
	if _, ok := errors.AsType[*json.UnmarshalTypeError](err); !ok {
		return nil, nil, fmt.Errorf("openai-responses: invalid SSE JSON: %w", err)
	}
	normalized, thrown, ok := normalizeResponsesItemJS(raw, done)
	*item = respOutputItem{}
	if !ok || json.Unmarshal(normalized, item) != nil {
		return nil, nil, fmt.Errorf("openai-responses: invalid SSE JSON: %w", err)
	}
	return normalized, thrown, nil
}

// responsesTextSignature is encodeTextSignatureV1(item.id, item.phase ?? undefined) (openai-responses-shared.ts:53-57, :703): JSON.stringify({ v: 1, id,
// phase }) of the item as sent, so a missing id is left out, a non-string id is written as its JSON value, and any truthy phase is kept.
func responsesTextSignature(raw json.RawMessage, item *respOutputItem) string {
	var members struct {
		ID    json.RawMessage `json:"id"`
		Phase json.RawMessage `json:"phase"`
	}
	if json.Unmarshal(raw, &members) != nil {
		return encodeResponsesTextSignature(item.ID, item.Phase)
	}
	var signature strings.Builder
	signature.WriteString(`{"v":1`)
	if len(members.ID) > 0 {
		signature.WriteString(`,"id":`)
		signature.Write(jsStringifiedJSON(members.ID))
	}
	if len(members.Phase) > 0 && jsTruthyJSON(members.Phase) {
		signature.WriteString(`,"phase":`)
		signature.Write(jsStringifiedJSON(members.Phase))
	}
	signature.WriteByte('}')
	return signature.String()
}

// jsStringifiedJSON is JSON.stringify(JSON.parse(raw)) for one parsed member.
func jsStringifiedJSON(raw json.RawMessage) []byte {
	if canonical, err := jsonstringify.Canonicalize(raw); err == nil {
		return canonical
	}
	return raw
}

// responsesDeltaEvents are the events whose handler appends `event.delta` with `+=`, so it reads String(delta), and "null" or "undefined" for a null or
// absent delta (openai-responses-shared.ts:605-679).
var responsesDeltaEvents = map[string]bool{
	"response.reasoning_summary_text.delta": true, "response.reasoning_text.delta": true, "response.output_text.delta": true,
	"response.refusal.delta": true, "response.function_call_arguments.delta": true, "response.custom_tool_call_input.delta": true,
}

// responsesEventRead is what the JS read of a mistyped stream event yields beyond the rewritten event.
type responsesEventRead struct {
	// status is String(response.status) for a truthy status that is not a string: mapStopReason's switch matches no case and throws
	// `Unhandled stop reason: ${status}` after the usage is recorded (:578-581, :779-800).
	status *string
	// thrown is a TypeError the event's handler throws: a function_call_arguments.done whose arguments is not a string, or a terminal response whose
	// output is not iterable.
	thrown error
}

// normalizeResponsesEventJS rewrites a stream event the typed decode rejects for a member's type into the shape it accepts, for the members whose JS
// result it reproduces; ok is false for any other. A member the event's handler never reads is dropped. A member Pi stores raw (a response id, a custom
// tool call input) is not rewritten; output_index decodes as any JSON value (responsesSlotKey).
// upstream: openai-responses-shared.ts:548-760
func normalizeResponsesEventJS(data []byte) (normalized []byte, read responsesEventRead, ok bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, read, false
	}
	event, isObject := value.(map[string]any)
	if !isObject {
		// `event.type` of a value that is not an object matches no branch.
		return []byte("{}"), read, true
	}
	kind, _ := event["type"].(string)
	if kind == "" {
		delete(event, "type")
	}
	if delta, present := event["delta"]; present && delta != nil && !isJSString(delta) {
		if responsesDeltaEvents[kind] {
			event["delta"] = jsToString(delta)
		} else {
			delete(event, "delta")
		}
	}
	if arguments, present := event["arguments"]; present && arguments != nil && !isJSString(arguments) {
		if kind == "response.function_call_arguments.done" {
			read.thrown = responsesArgumentsDoneError(arguments, true)
		}
		delete(event, "arguments")
	}
	if input, present := event["input"]; present && input != nil && !isJSString(input) {
		if kind == "response.custom_tool_call_input.done" {
			return nil, read, false
		}
		delete(event, "input")
	}
	response, present := event["response"]
	if !present || response == nil {
		return marshalResponsesEvent(event, data, read)
	}
	responseObject, isObject := response.(map[string]any)
	switch kind {
	case "response.created":
		if !isObject {
			// `event.response.id` of a value that is not an object is undefined.
			event["response"] = map[string]any{}
			return marshalResponsesEvent(event, data, read)
		}
		if id, present := responseObject["id"]; present && id != nil && !isJSString(id) {
			return nil, read, false
		}
		event["response"] = map[string]any{"id": responseObject["id"]}
	case "response.completed", "response.incomplete":
		if !isObject {
			// finalizeResponse reads every member of a value that is not an object as undefined; `response.output ?? []` is then empty.
			event["response"] = map[string]any{}
			return marshalResponsesEvent(event, data, read)
		}
		if !normalizeResponsesTerminal(responseObject, &read) {
			return nil, read, false
		}
	case "response.failed":
		if !isObject {
			event["response"] = map[string]any{}
			return marshalResponsesEvent(event, data, read)
		}
		normalizeResponsesFailed(responseObject)
	default:
		delete(event, "response")
	}
	return marshalResponsesEvent(event, data, read)
}

// marshalResponsesEvent writes the rewritten event in the member order of the event it was parsed from, as JSON.parse keeps it.
func marshalResponsesEvent(event map[string]any, source []byte, read responsesEventRead) ([]byte, responsesEventRead, bool) {
	out, err := orderedjson.MarshalInSourceOrder(event, source)
	if err != nil {
		return nil, read, false
	}
	return out, read, true
}

// responsesArgumentsDoneError is the TypeError function_call_arguments.done throws for arguments that is not a string: parseStreamingJson calls
// `partialJson.trim()` on a truthy value, and `event.arguments.startsWith(...)` then fails on a falsy one (:661-671).
func responsesArgumentsDoneError(arguments any, present bool) error {
	switch {
	case !present:
		return errors.New("Cannot read properties of undefined (reading 'startsWith')")
	case arguments == nil:
		return errors.New("Cannot read properties of null (reading 'startsWith')")
	case jsTruthy(arguments):
		return errors.New("partialJson.trim is not a function")
	}
	return errors.New("event.arguments.startsWith is not a function")
}

func normalizeResponsesTerminal(response map[string]any, read *responsesEventRead) bool {
	if id, present := response["id"]; present && id != nil && !isJSString(id) {
		// `if (response?.id) output.responseId = response.id` stores a truthy id raw.
		if jsTruthy(id) {
			return false
		}
		delete(response, "id")
	}
	if output, present := response["output"]; present && output != nil {
		switch output := output.(type) {
		case []any:
			for i, item := range output {
				fields, isObject := item.(map[string]any)
				if !isObject {
					// backfillReasoningSignatures reads `item.type` of a value that is not an object as undefined.
					output[i] = map[string]any{}
					continue
				}
				kept := map[string]any{}
				for _, key := range []string{"type", "id", "encrypted_content"} {
					if member, isString := fields[key].(string); isString {
						kept[key] = member
					} else if fields[key] != nil && fields["type"] == "reasoning" && (key != "encrypted_content" || jsTruthy(fields[key])) {
						return false
					}
				}
				output[i] = kept
			}
		case string:
			// for-of over a string yields characters, whose type is undefined.
			delete(response, "output")
		default:
			read.thrown = errors.New("responseOutput is not iterable")
			delete(response, "output")
		}
	}
	if status, present := response["status"]; present && status != nil && !isJSString(status) {
		if jsTruthy(status) {
			text := jsToString(status)
			read.status = &text
		}
		delete(response, "status")
	}
	if details, present := response["incomplete_details"]; present && details != nil {
		detailsObject, isObject := details.(map[string]any)
		if !isObject {
			delete(response, "incomplete_details")
		} else if !isJSString(detailsObject["reason"]) {
			// `typeof incompleteDetails?.reason === "string" ? ... : undefined`
			delete(detailsObject, "reason")
		}
	}
	if !normalizeResponsesUsage(response) {
		return false
	}
	for _, key := range []string{"service_tier"} {
		if member, present := response[key]; present && member != nil && !isJSString(member) {
			return false
		}
	}
	for _, key := range []string{"model", "error"} {
		if member, present := response[key]; present && member != nil && !isJSString(member) {
			delete(response, key)
		}
	}
	if endTurn, present := response["end_turn"]; present {
		if _, isBool := endTurn.(bool); !isBool {
			delete(response, "end_turn")
		}
	}
	return true
}

// normalizeResponsesUsage rewrites response.usage as finalizeResponse reads it (:557-575): a truthy usage that is not an object reads every count as
// undefined, `input_tokens || 0` is subtracted from so a truthy non-number counts as its Number() when that is an integer, the other counts keep a falsy
// value as 0, and a details member that is not an object reads undefined.
func normalizeResponsesUsage(response map[string]any) bool {
	usage, present := response["usage"]
	if !present || usage == nil {
		return true
	}
	usageObject, isObject := usage.(map[string]any)
	if !isObject {
		if jsTruthy(usage) {
			response["usage"] = map[string]any{}
		} else {
			delete(response, "usage")
		}
		return true
	}
	if input, present := usageObject["input_tokens"]; present && input != nil {
		if _, isNumber := input.(json.Number); !isNumber {
			if !jsTruthy(input) {
				delete(usageObject, "input_tokens")
			} else {
				number, ok := jsIntegerNumber(input)
				if !ok {
					return false
				}
				usageObject["input_tokens"] = number
			}
		}
	}
	for _, key := range []string{"output_tokens", "total_tokens"} {
		if !numberOrFalsy(usageObject, key) {
			return false
		}
	}
	for key, counts := range map[string][]string{"input_tokens_details": {"cached_tokens", "cache_write_tokens"}, "output_tokens_details": {"reasoning_tokens"}} {
		details, present := usageObject[key]
		if !present || details == nil {
			continue
		}
		detailsObject, isObject := details.(map[string]any)
		if !isObject {
			delete(usageObject, key)
			continue
		}
		for _, count := range counts {
			if !numberOrFalsy(detailsObject, count) {
				return false
			}
		}
	}
	return true
}

// normalizeResponsesFailed rewrites a response.failed response as its handler reads it (:747-758): `${error.code || "unknown"}: ${error.message || "no
// message"}` and `incomplete: ${details.reason}` interpolate the String() form of a truthy member, and a member that is not an object reads every member
// as undefined.
func normalizeResponsesFailed(response map[string]any) {
	for key, members := range map[string][]string{"error": {"code", "message"}, "incomplete_details": {"reason"}} {
		value, present := response[key]
		if !present || value == nil {
			continue
		}
		if !jsTruthy(value) {
			delete(response, key)
			continue
		}
		object, isObject := value.(map[string]any)
		if !isObject {
			response[key] = map[string]any{}
			continue
		}
		read := map[string]any{}
		for _, member := range members {
			if field := object[member]; jsTruthy(field) {
				read[member] = jsToString(field)
			}
		}
		response[key] = read
	}
	if status, present := response["status"]; present && status != nil && !isJSString(status) {
		// `output.rawStopReason = event.response?.status`
		response["status"] = jsToString(status)
	}
	for key, value := range response {
		if key != "error" && key != "incomplete_details" && key != "status" && value != nil && !isJSString(value) {
			delete(response, key)
		}
	}
}

// readResponsesEmptyMember gives an event whose typed delta or arguments is empty the member Pi's handler reads: `+=` appends "null" or "undefined" for a
// null or absent delta, and function_call_arguments.done throws for null or absent arguments.
func readResponsesEmptyMember(data string, event *respSSEEvent, read *responsesEventRead) {
	record := anthropicRecord(data)
	if record == nil {
		return
	}
	if event.Type == "response.function_call_arguments.done" {
		if arguments, present := record["arguments"]; !present || arguments == nil {
			read.thrown = responsesArgumentsDoneError(arguments, present)
		}
		return
	}
	switch delta, present := record["delta"]; {
	case !present:
		event.Delta = "undefined"
	case delta == nil:
		event.Delta = "null"
	}
}

// hasEmptyResponsesPart reports whether a done item's read parts include one with no members, which may have been null.
func hasEmptyResponsesPart(item *respOutputItem) bool {
	var lists [][]respOutputContent
	switch item.Type {
	case "message":
		lists = [][]respOutputContent{item.Content}
	case "reasoning":
		lists = [][]respOutputContent{item.Summary, item.Content}
	}
	for _, parts := range lists {
		if slices.Contains(parts, (respOutputContent{})) {
			return true
		}
	}
	return false
}

// responsesArgumentsNotString reports whether an added function_call item's arguments is truthy and not a string: createSlot keeps that value as
// the slot's partialJson (`item.arguments || ""`) until a delta or an arguments.done event makes it a string, and output_item.done's
// parseStreamingJson throws on it.
func responsesArgumentsNotString(raw json.RawMessage) bool {
	var member struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(raw, &member) != nil {
		return false
	}
	arguments := bytes.TrimSpace(member.Arguments)
	if len(arguments) == 0 || arguments[0] == '"' {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	var value any
	return decoder.Decode(&value) == nil && jsTruthy(value)
}

// responsesItemMissing is the TypeError of reading `item.type` on a missing or null output item.
func responsesItemMissing(raw json.RawMessage) error {
	switch string(bytes.TrimSpace(raw)) {
	case "":
		return errors.New("Cannot read properties of undefined (reading 'type')")
	case "null":
		return errors.New("Cannot read properties of null (reading 'type')")
	}
	return nil
}

// responsesSlotKey is an event's output_index as a key of Pi's `outputSlots` Map (openai-responses-shared.ts:441), which compares keys with
// SameValueZero: a number by its value, a string by its text, true, false, null and an absent member (undefined) each as one key, and an object or
// array, which JSON.parse creates anew for every event, as a key no other event's equals.
type responsesSlotKey struct {
	kind   responsesSlotKind
	number float64
	text   string
	object uint64
}

type responsesSlotKind uint8

const (
	responsesSlotUndefined responsesSlotKind = iota
	responsesSlotNumber
	responsesSlotString
	responsesSlotNull
	responsesSlotTrue
	responsesSlotFalse
	responsesSlotObject
)

var responsesSlotObjects atomic.Uint64

func (key *responsesSlotKey) UnmarshalJSON(data []byte) error {
	*key = responsesSlotKey{}
	switch {
	case len(data) == 0:
		return errors.New("responsesSlotKey: empty value")
	case data[0] == '-' || data[0] >= '0' && data[0] <= '9':
		key.kind = responsesSlotNumber
		if small, ok := smallJSONInteger(data); ok {
			key.number = float64(small)
			return nil
		}
		// JSON.parse reads an overflowing number as ±Infinity, which ParseFloat returns with ErrRange.
		number, err := strconv.ParseFloat(string(data), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return err
		}
		key.number = number
	case data[0] == '"':
		key.kind = responsesSlotString
		return json.Unmarshal(data, &key.text)
	case string(data) == "null":
		key.kind = responsesSlotNull
	case string(data) == "true":
		key.kind = responsesSlotTrue
	case string(data) == "false":
		key.kind = responsesSlotFalse
	default:
		key.kind, key.object = responsesSlotObject, responsesSlotObjects.Add(1)
	}
	return nil
}

// smallJSONInteger reads a JSON integer of at most 15 digits, which float64 holds exactly, without allocating.
func smallJSONInteger(data []byte) (int64, bool) {
	digits, negative := data, false
	if len(digits) > 0 && digits[0] == '-' {
		digits, negative = digits[1:], true
	}
	if len(digits) == 0 || len(digits) > 15 {
		return 0, false
	}
	var value int64
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return 0, false
		}
		value = value*10 + int64(digit-'0')
	}
	if negative {
		value = -value
	}
	return value, true
}
