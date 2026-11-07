package sdk

import (
	"cmp"
	"math"
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A Go handler holds the objects of an event as map[string]any, which has no member order, and the SDK writes a map in byte order. Pi hands its handlers the
// JavaScript objects themselves and writes what they hold with their members in insertion order. An extension that hands back an object it was sent (the
// entries of a turn boundary, a message of the context, the provider request's payload) must therefore not change the order of its members: the host stores
// the entries and sends the messages and the payload, with their tool-call arguments, to the provider as it reads them (the saved session and the prompt-cache
// prefix change with the order).
//
// The decode of an event records the member order of each object it builds (json.ObjectOrder), so an object is known by its own map: whatever a handler does to
// the lists and objects of the event (moves, drops, copies or edits them), an object it hands back that the event held is written in the order the host wrote
// it, and an object it built is written in byte order. Nothing is read from the event's text when the reply is written.

// echoEvents are the events whose result the host reads as data: the objects of the event a handler hands back are written in the order they came in.
var echoEvents = map[string]bool{
	"turn_end":                true,
	"agent_before_settle":     true,
	"context":                 true,
	"context_with_system":     true,
	"before_provider_request": true,
	"tool_result":             true,
	"message_end":             true,
}

// handedBack is result, the reply to an event, written with the member order of the objects the event held. A result no object of the event can be in, or an
// event whose order is not known (its payload arrived in pieces), is returned as it is.
func handedBack(event string, result any, order *json.ObjectOrder) any {
	if order == nil || result == nil || !echoEvents[event] {
		return result
	}
	return orderedReply{result, order}
}

// ObjectKeys returns the keys of object in the order Object.keys gives them in Pi. For an object of the event this handler is serving that is the order the
// host wrote its members in, for the members the object still holds, and then the members the handler added, in byte order. A Go map has no order of its own,
// so this is how a Go handler reads the order the host sent; Rust, Python, Lisp and Node objects keep it and need no call. Array-index keys ("0", "1", ...)
// come first, ascending, as JavaScript enumerates them. A map the handler built, or any object of an event whose payload arrived in pieces or whose request
// has completed, has its keys in byte order. The order is recorded when the event is decoded, so the call reads it from a table and costs a lookup.
// pig additive (D273)
func (c Context) ObjectKeys(object map[string]any) []string {
	var known []string
	if c.req != nil && object != nil {
		c.req.keyMu.Lock()
		order := c.req.keyOrder
		c.req.keyMu.Unlock()
		known, _ = order.KeysOf(object)
	}
	keys := make([]string, 0, len(object))
	for _, key := range known {
		if _, ok := object[key]; ok {
			keys = append(keys, key)
		}
	}
	if len(keys) < len(object) {
		var rest []string
		if len(known) > 16 {
			held := make(map[string]struct{}, len(known))
			for _, key := range known {
				held[key] = struct{}{}
			}
			for key := range object {
				if _, ok := held[key]; !ok {
					rest = append(rest, key)
				}
			}
		} else {
			for key := range object {
				if !slices.Contains(known, key) {
					rest = append(rest, key)
				}
			}
		}
		slices.Sort(rest)
		keys = append(keys, rest...)
	}
	return arrayIndexKeysFirst(keys)
}

// arrayIndexKeysFirst orders keys as a JavaScript object enumerates them: the array-index keys first, ascending, then the others in the order given.
func arrayIndexKeysFirst(keys []string) []string {
	indexes := 0
	for _, key := range keys {
		if _, ok := arrayIndex(key); ok {
			indexes++
		}
	}
	if indexes == 0 {
		return keys
	}
	ordered := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, ok := arrayIndex(key); ok {
			ordered = append(ordered, key)
		}
	}
	slices.SortStableFunc(ordered, func(a, b string) int {
		x, _ := arrayIndex(a)
		y, _ := arrayIndex(b)
		return cmp.Compare(x, y)
	})
	for _, key := range keys {
		if _, ok := arrayIndex(key); !ok {
			ordered = append(ordered, key)
		}
	}
	return ordered
}

// arrayIndex returns the value of key when it is an array index: a canonical decimal integer below 2^32-1.
func arrayIndex(key string) (uint32, bool) {
	if key == "" || len(key) > 10 || (key[0] == '0' && len(key) > 1) {
		return 0, false
	}
	value, err := strconv.ParseUint(key, 10, 32)
	if err != nil || value == math.MaxUint32 {
		return 0, false
	}
	return uint32(value), true
}
