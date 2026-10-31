package link

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/src/next/link/text.ts. The text codec (2b): one JSON tuple per message, Delta style (a short kind
// tag, positional fields, no field names), and DescribeMessage, which prints any message in that form. A part's JSON
// text is spliced into the tuple as it is; decoding parses the tuple and writes each part back as text (one parse per
// part per hop: a debugging codec). Byte segments of a payload marked as carrying bytes follow the tuple after a
// newline (JSON text never contains a raw one), their lengths the tuple's last field. A message with an origin is
// wrapped: `["@", origin, tuple]`. Every part is tagged with its codec, so no JSON value is reserved: `[0, json]`, or
// `[1, base64]` for a part its origin deflated (the text codec itself never compresses).

type textCodec struct{}

// TextCodec is the messages as JSON tuples: readable on the wire, for debugging links.
var TextCodec Codec = textCodec{}

func (textCodec) Name() string { return "text" }

func (textCodec) Encode(message Message) (encoded []byte, err error) {
	defer recoverCodec(&err)
	tupleText := tuple(message)
	segments := segmentsOf(message)
	if segments == nil {
		return []byte(tupleText), nil
	}
	length := len(tupleText) + 1
	for _, segment := range segments {
		length += len(segment)
	}
	out := make([]byte, 0, length)
	out = append(out, tupleText...)
	out = append(out, newline)
	for _, segment := range segments {
		out = append(out, segment...)
	}
	return out, nil
}

func (textCodec) Decode(data []byte) (message Message, err error) {
	defer recoverCodec(&err)
	line, rest := data, data[len(data):]
	if before, after, ok0 := bytes.Cut(data, []byte{newline}); ok0 {
		line, rest = before, after
	}
	parsed, parseErr := parseJson(line)
	if parseErr != nil {
		failWith(parseErr)
	}
	fields, ok := parsed.([]any)
	if !ok {
		fail("text: a message is a tuple")
	}
	return fromTuple(fields, rest), nil
}

// DescribeMessage is a message in the text codec's tuple form, for logs and tests (byte segments show as
// `{"$bytes": i}`). A message that cannot be written (a part that is no UTF-8) is described by its error.
func DescribeMessage(message Message) (described string) {
	defer func() {
		if caught := recover(); caught != nil {
			failure, ok := caught.(codecError)
			if !ok {
				panic(caught)
			}
			described = "<" + failure.err.Error() + ">"
		}
	}()
	return tuple(message)
}

const newline = 0x0a

// segmentsOf are the segments following a payload's tuple; nil without a payload marked as carrying bytes.
func segmentsOf(message Message) [][]byte {
	switch typed := message.(type) {
	case *CallMessage:
		return typed.Segments
	case *ResultMessage:
		return typed.Segments
	}
	return nil
}

// tuple is the tuple's JSON text, parts spliced in; wrapped with its origin when it has one.
func tuple(message Message) string {
	origin := OriginOf(message)
	if !origin.Ok {
		return ownTuple(message)
	}
	return `["@",` + strconv.Itoa(origin.Value) + "," + ownTuple(message) + "]"
}

func jsonText(value any) string {
	encoded, err := chordjson.Marshal(value)
	if err != nil {
		failWith(err)
	}
	return string(encoded)
}

func mustPartText(part Part) string {
	text, err := partText(part)
	if err != nil {
		failWith(err)
	}
	return text
}

func idList(ids []string) []any {
	out := make([]any, len(ids))
	for index, id := range ids {
		out[index] = id
	}
	return out
}

// ownTuple is the tuple of a message's own fields.
func ownTuple(message Message) string {
	switch typed := message.(type) {
	case *HelloMessage:
		return jsonText([]any{"h", float64(typed.Protocol), typed.Incarnation, float64(typed.Calls)})
	case *HoldMessage:
		head := jsonText([]any{"H", float64(typed.Hold), typed.Type, float64(typed.Version), typed.Fingerprint})
		head = head[:len(head)-1]
		// Optional trailing fields: an absent one in the middle is null, absent ones at the end are left out.
		optional := []any{nil, nil, nil, nil}
		if typed.Principal != nil {
			optional[0] = typed.Principal
		}
		if typed.Incarnation != "" {
			optional[1] = typed.Incarnation
		}
		if typed.Chain != nil {
			optional[2] = idList(typed.Chain)
		}
		if typed.Limit.Ok {
			optional[3] = float64(typed.Limit.Value)
		}
		for len(optional) > 0 && optional[len(optional)-1] == nil {
			optional = optional[:len(optional)-1]
		}
		var rest strings.Builder
		for _, field := range optional {
			rest.WriteString("," + jsonText(field))
		}
		return head + "," + mustPartText(typed.Params) + "," + jsonText([]any{typed.Address.Kind, typed.Address.Params}) + rest.String() + "]"
	case *ReleaseMessage:
		return jsonText([]any{"R", float64(typed.Hold)})
	case *CallMessage:
		return withPayload(jsonText([]any{"c", float64(typed.Call), float64(typed.Hold), float64(typed.Method)}), typed.Args, typed.Segments)
	case *AbortMessage:
		return jsonText([]any{"x", float64(typed.Call)})
	case *BoundMessage:
		entries := make([]string, len(typed.Holds))
		for index, entry := range typed.Holds {
			entries[index] = boundEntry(entry)
		}
		return `["b",[` + strings.Join(entries, ",") + "]]"
	case *StateMessage:
		entries := make([]string, len(typed.Holds))
		for index, entry := range typed.Holds {
			entries[index] = stateEntry(entry)
		}
		return `["s",[` + strings.Join(entries, ",") + "]]"
	case *FailedMessage:
		fields := []any{"f", float64(typed.Hold), typed.Cause.Kind[:1], typed.Cause.Error}
		if typed.RetryIn.Ok {
			fields = append(fields, float64(typed.RetryIn.Value))
		}
		return jsonText(fields)
	case *ResultMessage:
		return withPayload(jsonText([]any{"r", float64(typed.Call)}), typed.Value, typed.Segments)
	case *ErrorMessage:
		return jsonText([]any{"e", float64(typed.Call), typed.Error})
	case *GoneMessage:
		return jsonText([]any{"g", float64(typed.Hold), typed.Error})
	case *RevokeMessage:
		return jsonText([]any{"V", typed.Revocation})
	case *RetryMessage:
		return jsonText([]any{"T", float64(typed.Hold), float64(typed.Hops)})
	case *LoadingMessage:
		return jsonText([]any{"l", float64(typed.Hold)})
	case *AckMessage:
		return `["a"]`
	case *ClosedMessage:
		return `["C"]`
	case *PingMessage:
		return `["p"]`
	case *PongMessage:
		return `["P"]`
	}
	fail("text: an unknown message type %T", message)
	return ""
}

func failedPairs(failed []FailedMember) string {
	pairs := make([]any, len(failed))
	for index, entry := range failed {
		pairs[index] = []any{float64(entry.Member), entry.Error}
	}
	return jsonText(pairs)
}

// boundEntry is `[hold, variant, [[member, value]…], failed?]`.
func boundEntry(entry BoundEntry) string {
	pairs := make([]string, len(entry.Members))
	for index, member := range entry.Members {
		pairs[index] = "[" + strconv.Itoa(member.Member) + "," + mustPartText(member.Value) + "]"
	}
	return "[" + strconv.Itoa(entry.Hold) + "," + strconv.Itoa(entry.Variant) + ",[" + strings.Join(pairs, ",") + "]" + failedField(entry.Failed) + "]"
}

// stateEntry is `[hold, [[member, ops]…], failed?, retired?]`; `failed` is `[]` when only `retired` follows.
func stateEntry(entry StateEntry) string {
	pairs := make([]string, len(entry.Members))
	for index, member := range entry.Members {
		pairs[index] = "[" + strconv.Itoa(member.Member) + "," + mustPartText(member.Ops) + "]"
	}
	head := "[" + strconv.Itoa(entry.Hold) + ",[" + strings.Join(pairs, ",") + "]"
	if len(entry.Retired) == 0 {
		return head + failedField(entry.Failed) + "]"
	}
	retired := make([]any, len(entry.Retired))
	for index, member := range entry.Retired {
		retired[index] = float64(member)
	}
	return head + "," + failedPairs(entry.Failed) + "," + jsonText(retired) + "]"
}

// failedField is the failures as an entry's optional last field: left out when there are none.
func failedField(failed []FailedMember) string {
	if len(failed) == 0 {
		return ""
	}
	return "," + failedPairs(failed)
}

// withPayload is a tuple (its closed JSON head) ending in a payload part; one marked as carrying bytes adds the
// segments' lengths as the last field.
func withPayload(head string, part Part, segments [][]byte) string {
	if part == nil {
		return head
	}
	prefix := head[:len(head)-1] + "," + mustPartText(part)
	if segments == nil {
		return prefix + "]"
	}
	lengths := make([]any, len(segments))
	for index, segment := range segments {
		lengths[index] = float64(len(segment))
	}
	return prefix + "," + jsonText(lengths) + "]"
}

// fromTuple is a message from its tuple: the tag and arity checked, lists of pairs checked as lists, parts written back
// as text; the field types are the message schema's to check (here they are read into the typed message, and a field
// of another type is an error). rest holds the segments, which only a payload marked as carrying bytes may have.
func fromTuple(fields []any, rest []byte) Message {
	if len(fields) > 0 && fields[0] == "@" {
		var inner []any
		if len(fields) == 3 {
			inner = list(fields[2])
		}
		if len(inner) == 0 || inner[0] == "@" {
			fail("text: an origin wraps one message")
		}
		message := fromFields(inner, rest)
		// A fresh message; the message schema checks the origin (an origin of 0 or one on a kind without it is refused).
		origin := asInt(fields[1])
		if !setOrigin(message, origin) {
			fail("text: an origin on a kind without one")
		}
		return message
	}
	return fromFields(fields, rest)
}

func asInt(value any) int {
	number, ok := value.(float64)
	if !ok || number != math.Trunc(number) || math.Abs(number) > maxSafeInteger {
		fail("text: expected an integer")
	}
	return int(number)
}

func asString(value any) string {
	text, ok := value.(string)
	if !ok {
		fail("text: expected a string")
	}
	return text
}

func asObject(value any, what string) *delta.JsonObject {
	object, ok := value.(*delta.JsonObject)
	if !ok {
		fail("text: %s is no object", what)
	}
	return object
}

func asIDs(value any) []string {
	items := list(value)
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = asString(item)
	}
	return ids
}

// fromFields is a message from its own fields' tuple (see fromTuple).
func fromFields(fields []any, rest []byte) Message {
	if len(fields) == 0 {
		fail("text: an unknown kind")
	}
	tag, f := fields[0], fields[1:]
	arity := func(allowed ...int) {
		if slices.Contains(allowed, len(f)) {
			return
		}
		fail("text: a %q message with %d fields", tagText(tag), len(f))
	}
	if tag != "c" && tag != "r" && len(rest) > 0 {
		fail("text: byte segments without a payload that carries them")
	}
	switch tag {
	case "h":
		arity(3)
		return &HelloMessage{Protocol: asInt(f[0]), Incarnation: asString(f[1]), Calls: asInt(f[2])}
	case "H":
		arity(6, 7, 8, 9, 10)
		address := list(f[5])
		if len(address) != 2 {
			fail("text: an address is a pair")
		}
		message := &HoldMessage{Hold: asInt(f[0]), Type: asString(f[1]), Version: asInt(f[2]), Fingerprint: asString(f[3])}
		message.Params = mustTextPart(f[4])
		message.Address = WireAddress{Kind: asString(address[0]), Params: address[1]}
		optional := func(index int) any {
			if index < len(f) {
				return f[index]
			}
			return nil
		}
		if principal := optional(6); principal != nil {
			message.Principal = asObject(principal, "a principal")
		}
		if incarnation := optional(7); incarnation != nil {
			message.Incarnation = asString(incarnation)
		}
		if chain := optional(8); chain != nil {
			message.Chain = asIDs(chain)
		}
		if limit := optional(9); limit != nil {
			message.Limit = Some(asInt(limit))
		}
		return message
	case "R":
		arity(1)
		return &ReleaseMessage{Hold: asInt(f[0])}
	case "c":
		arity(3, 4, 5)
		message := &CallMessage{Call: asInt(f[0]), Hold: asInt(f[1]), Method: asInt(f[2])}
		message.Args, message.Segments = payloadFields(f[3:], rest)
		return message
	case "x":
		arity(1)
		return &AbortMessage{Call: asInt(f[0])}
	case "b":
		arity(1)
		entries := list(f[0])
		holds := make([]BoundEntry, len(entries))
		for index, item := range entries {
			entry := list(item)
			if len(entry) != 3 && len(entry) != 4 {
				fail("text: a bound entry with %d fields", len(entry))
			}
			holds[index] = BoundEntry{Hold: asInt(entry[0]), Variant: asInt(entry[1])}
			for _, pair := range pairs(entry[2]) {
				holds[index].Members = append(holds[index].Members, BoundMember{Member: asInt(pair[0]), Value: mustTextPart(pair[1])})
			}
			if len(entry) == 4 {
				holds[index].Failed = failures(entry[3])
			}
		}
		return &BoundMessage{Holds: holds}
	case "s":
		arity(1)
		entries := list(f[0])
		holds := make([]StateEntry, len(entries))
		for index, item := range entries {
			entry := list(item)
			if len(entry) < 2 || len(entry) > 4 {
				fail("text: a state entry with %d fields", len(entry))
			}
			holds[index] = StateEntry{Hold: asInt(entry[0]), Retired: NoRetired}
			for _, pair := range pairs(entry[1]) {
				holds[index].Members = append(holds[index].Members, StateMember{Member: asInt(pair[0]), Ops: mustTextPart(pair[1])})
			}
			if len(entry) >= 3 {
				holds[index].Failed = failures(entry[2])
			}
			if len(entry) == 4 {
				retired := list(entry[3])
				holds[index].Retired = make([]int, len(retired))
				for at, member := range retired {
					holds[index].Retired[at] = asInt(member)
				}
			}
		}
		return &StateMessage{Holds: holds}
	case "f":
		arity(3, 4)
		kind := ""
		if letter, ok := f[1].(string); ok {
			for _, each := range CauseKinds {
				if each[:1] == letter {
					kind = each
				}
			}
		}
		if kind == "" {
			fail("text: an unknown failure kind")
		}
		message := &FailedMessage{Hold: asInt(f[0]), Cause: WireCause{Kind: kind, Error: asObject(f[2], "an error")}}
		if len(f) == 4 {
			message.RetryIn = Some(asInt(f[3]))
		}
		return message
	case "r":
		arity(1, 2, 3)
		message := &ResultMessage{Call: asInt(f[0])}
		message.Value, message.Segments = payloadFields(f[1:], rest)
		return message
	case "e":
		arity(2)
		return &ErrorMessage{Call: asInt(f[0]), Error: asObject(f[1], "an error")}
	case "g":
		arity(2)
		return &GoneMessage{Hold: asInt(f[0]), Error: asObject(f[1], "an error")}
	case "V":
		arity(1)
		return &RevokeMessage{Revocation: f[0]}
	case "T":
		arity(2)
		return &RetryMessage{Hold: asInt(f[0]), Hops: asInt(f[1])}
	case "l":
		arity(1)
		return &LoadingMessage{Hold: asInt(f[0])}
	case "a":
		arity(0)
		return &AckMessage{}
	case "C":
		arity(0)
		// Its origin is the wrapper's; 0 without one, which the message schema refuses.
		return &ClosedMessage{}
	case "p":
		arity(0)
		return &PingMessage{}
	case "P":
		arity(0)
		return &PongMessage{}
	}
	fail("text: an unknown kind")
	return nil
}

func tagText(tag any) string {
	if text, ok := tag.(string); ok {
		return text
	}
	return fmt.Sprint(tag)
}

func mustTextPart(value any) Part {
	part, err := textPart(value)
	if err != nil {
		failWith(err)
	}
	return part
}

// payloadFields is a payload part from its fields: none, the value, or the value and its segments' lengths (marked as
// carrying bytes, the segments views of rest).
func payloadFields(fields []any, rest []byte) (Part, [][]byte) {
	if len(fields) < 2 {
		if len(rest) > 0 {
			fail("text: byte segments without a payload that carries them")
		}
		if len(fields) == 0 {
			return nil, nil
		}
		return mustTextPart(fields[0]), nil
	}
	lengths := list(fields[1])
	offset := 0
	segments := make([][]byte, len(lengths))
	for index, item := range lengths {
		length, ok := item.(float64)
		if !ok || length != math.Trunc(length) || length < 0 || length > maxSafeInteger {
			fail("text: a segment length")
		}
		end := min(offset+int(length), len(rest))
		segments[index] = rest[offset:end:end]
		offset += int(length)
	}
	if offset != len(rest) {
		fail("text: the segments do not fill the message")
	}
	return mustTextPart(fields[0]), segments
}

func list(value any) []any {
	items, ok := value.([]any)
	if !ok {
		fail("text: expected a list")
	}
	return items
}

// pairs is a list of `[index, value]` pairs.
func pairs(value any) [][2]any {
	items := list(value)
	out := make([][2]any, len(items))
	for index, item := range items {
		entry := list(item)
		if len(entry) != 2 {
			fail("text: expected a pair")
		}
		out[index] = [2]any{entry[0], entry[1]}
	}
	return out
}

// failures is the optional failures field; absent means none.
func failures(value any) []FailedMember {
	if value == nil {
		// A `null` is a value, not an absent field: upstream's check refuses it as a list.
		fail("text: expected a list")
	}
	var failed []FailedMember
	for _, pair := range pairs(value) {
		failed = append(failed, FailedMember{Member: asInt(pair[0]), Error: asObject(pair[1], "an error")})
	}
	return failed
}
