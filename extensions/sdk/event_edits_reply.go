package sdk

import (
	"hash/maphash"
	"math"
	"reflect"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi hands every message_end handler the one message (runner.ts emitMessageEnd gives each handler an event over currentMessage) and stores that object
// (agent-session.ts appendMessage), so a handler that edits event.message in place changes the message the next handlers, turn_end, agent_end, the entry and
// the next provider request hold, even when it throws afterwards. A turn_end handler is handed the turn's message objects the same way (agent-session.ts
// _dispatchTurnEndBoundary). The host cannot share the maps, so the reply carries what the handler left of them.

// eventObjects are the message objects of an event a handler may edit in place, by the position the reply names them at (_pigEventEdits), as the handler received them:
// message_start and message_end have one, turn_end its assistant message and then its tool results, agent_end its messages. An element is not a map when the event
// carries no message object there (a handler that assigns another value to the member of its data changes nothing, as assigning event.message changes nothing in Pi:
// the event is made for each handler).
//
// A handler that edits nothing must cost nothing here: the objects are not copied or encoded when the event is received. was is a fingerprint of them (no allocation),
// and only an event whose objects no longer have it is compared with what the host sent.
type eventObjects struct {
	message any
	list    []any
	named   bool // the event holds a message first (message_start, message_end, turn_end): agent_end holds only its list
	was     uint64
}

func captureEventObjects(event string, data map[string]any) (eventObjects, bool) {
	var o eventObjects
	switch event {
	case "message_start", "message_end":
		o = eventObjects{message: data["message"], named: true}
	case "turn_end":
		o = eventObjects{message: data["message"], list: listMember(data["toolResults"]), named: true}
	case "agent_end":
		o = eventObjects{list: listMember(data["messages"])}
	default:
		return eventObjects{}, false
	}
	o.was = o.fingerprint()
	return o, true
}

// fingerprint is a 64-bit hash of the objects' contents, in order, that does not depend on the order of a map's members (a Go map has none a handler could change).
func (o eventObjects) fingerprint() uint64 {
	h := uint64(len(o.list))
	if o.named {
		h = mixFingerprint(h, valueFingerprint(o.message))
	}
	for _, item := range o.list {
		h = mixFingerprint(h, valueFingerprint(item))
	}
	return h
}

var fingerprintSeed = maphash.MakeSeed()

func mixFingerprint(h, v uint64) uint64 {
	h ^= v + 0x9e3779b97f4a7c15 + h<<6 + h>>2
	return h
}

// valueFingerprint hashes a decoded JSON value. A value of a type a decode does not produce (a handler assigned it) hashes by its type, so it differs from what was received.
func valueFingerprint(value any) uint64 {
	switch v := value.(type) {
	case nil:
		return 1
	case bool:
		if v {
			return 3
		}
		return 2
	case float64:
		return mixFingerprint(4, math.Float64bits(v))
	case string:
		return mixFingerprint(5, maphash.String(fingerprintSeed, v))
	case json.Number:
		return mixFingerprint(6, maphash.String(fingerprintSeed, string(v)))
	case []any:
		h := uint64(7)
		for _, item := range v {
			h = mixFingerprint(h, valueFingerprint(item))
		}
		return h
	case map[string]any:
		h := uint64(8) + uint64(len(v))
		for key, item := range v {
			h += mixFingerprint(maphash.String(fingerprintSeed, key), valueFingerprint(item))
		}
		return h
	}
	return mixFingerprint(9, maphash.String(fingerprintSeed, reflect.TypeOf(value).String()))
}

// sentEventMessages are the messages of the event as the host sent them, decoded again from the frame's arguments (only the members that hold messages), in the order of eventObjects.objects.
func sentEventMessages(event string, args []byte) []any {
	var sent struct {
		Message     any   `json:"message"`
		Messages    []any `json:"messages"`
		ToolResults []any `json:"toolResults"`
	}
	if unmarshalFrameMember(args, &sent) != nil {
		return nil
	}
	switch event {
	case "message_start", "message_end":
		return []any{sent.Message}
	case "turn_end":
		return append([]any{sent.Message}, sent.ToolResults...)
	case "agent_end":
		return sent.Messages
	}
	return nil
}

func listMember(value any) []any {
	list, _ := value.([]any)
	return list
}

// edits are the messages of the objects whose value differs from the one the host sent, each at its position, or nil when the handler left them as they were.
func (o eventObjects) edits(event string, args []byte) []any {
	if o.fingerprint() == o.was {
		return nil
	}
	var objects []any
	if o.named {
		objects = append(objects, o.message)
	}
	objects = append(objects, o.list...)
	var was []any
	var edits []any
	for at, object := range objects {
		if _, ok := object.(map[string]any); !ok {
			continue
		}
		if was == nil {
			was = sentEventMessages(event, args)
		}
		if at < len(was) && !reflect.DeepEqual(object, was[at]) {
			edits = append(edits, map[string]any{"at": at, "message": object})
		}
	}
	return edits
}

// eventEditsReply is the reply to a handler of message_start, message_end or agent_end: result is what the handler returned and edits the messages it edited in place.
//
// The reply is {_pigEventEdits, _pigEventResult}: the messages that differ from what the host sent, each at its position, and the handler's result.
func eventEditsReply(result any, edits []any) any {
	reply := map[string]any{"_pigEventResult": result}
	if edits != nil {
		reply["_pigEventEdits"] = edits
	}
	return reply
}

// boundaryReply is the reply of a turn_end or agent_before_settle handler: the entries proposal and the handler's result (pig additive, D19), and for turn_end the
// messages the handler edited in place, each at its position.
func boundaryReply(data map[string]any, result any, edits []any) map[string]any {
	reply := map[string]any{"_pigBoundaryEntries": data["entries"], "_pigBoundaryResult": result}
	if edits != nil {
		reply["_pigEventEdits"] = edits
	}
	return reply
}
