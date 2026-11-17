package ai

import (
	"reflect"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Pi hands the onPayload hook the object a provider's buildParams created, and JSON.stringify of that object writes its members in the order the code created
// them. A provider that hands the hook a map[string]any (Google, Vertex and pi-messages build theirs that way, so an in-process handler can read and edit
// it in place) has no order to give: a Go map writes its members sorted. The provider therefore holds the order of the map it hands out for the length of the
// hook call, and MarshalHookPayload, which writes the payload for an extension in another process, writes the members it names in that order.

// HookObjectOrder is the member order of one object of a hook payload: the members Pi's code creates, in that order, and for a member that is an object (or a list
// of objects, each element) the order of that object. A member it does not describe is written as any value is (a typed value in its own order, a map sorted).
type HookObjectOrder struct {
	Keys   []string
	Nested map[string]*HookObjectOrder
}

// hookPayloads holds the member order of each payload map a provider has handed to a hook that has not returned, by the map's address. Only a request with a
// hook registered reaches it, and the provider releases a payload with a deferred call, so an entry lives exactly as long as the hook call that holds the map
// (an error, a cancellation or a panic in the hook included): no entry outlives its map, so the collector cannot give its address to another map.
var hookPayloads struct {
	sync.Mutex
	held []heldHookPayload
}

type heldHookPayload struct {
	key   uintptr
	order *HookObjectOrder
}

// HookPayloadHold is one payload held by HoldHookPayload.
type HookPayloadHold struct{ key uintptr }

// HoldHookPayload records the member order of payload until the returned hold is released, which the provider does, deferred, when the hook has returned.
func HoldHookPayload(payload map[string]any, order *HookObjectOrder) HookPayloadHold {
	key := reflect.ValueOf(payload).Pointer()
	hookPayloads.Lock()
	hookPayloads.held = append(hookPayloads.held, heldHookPayload{key: key, order: order})
	hookPayloads.Unlock()
	return HookPayloadHold{key: key}
}

// Release forgets the payload: its entry is replaced by the last one and the slice keeps its capacity, so a request allocates nothing once the registry has grown to the requests in flight.
func (hold HookPayloadHold) Release() {
	hookPayloads.Lock()
	defer hookPayloads.Unlock()
	held := hookPayloads.held
	for i := len(held) - 1; i >= 0; i-- {
		if held[i].key == hold.key {
			held[i] = held[len(held)-1]
			held[len(held)-1] = heldHookPayload{}
			hookPayloads.held = held[:len(held)-1]
			return
		}
	}
}

// callPayloadHook runs a provider's onPayload hook with the member order of its map payload held for the length of the call.
func callPayloadHook(hook func(any, *Model) (any, error), payload map[string]any, order *HookObjectOrder, model *Model) (any, error) {
	defer HoldHookPayload(payload, order).Release()
	return hook(payload, model)
}

// MarshalHookPayload writes the payload an onPayload hook was handed as JSON.stringify writes the object Pi hands its hook: the members Pi's provider
// creates in the order it creates them, then any member a handler added in place, sorted (a Go map remembers no insertion order, so that is the one case where
// two or more members added in place by an in-process handler are not in the order they were added). ok is false for a payload no provider holds, which the
// caller writes as it writes any value.
func MarshalHookPayload(payload any) (encoded []byte, ok bool, err error) {
	object, isMap := payload.(map[string]any)
	if !isMap {
		return nil, false, nil
	}
	key := reflect.ValueOf(object).Pointer()
	var order *HookObjectOrder
	hookPayloads.Lock()
	for _, entry := range hookPayloads.held {
		if entry.key == key {
			order = entry.order
		}
	}
	hookPayloads.Unlock()
	if order == nil {
		return nil, false, nil
	}
	encoded, err = marshalOrderedHookObject(object, order)
	return encoded, err == nil, err
}

// marshalOrderedHookValue writes value under order: a map in the order's members, a list of maps with each element in it, anything else as any value.
func marshalOrderedHookValue(value any, order *HookObjectOrder) ([]byte, error) {
	if order == nil {
		return marshalRequestBody(value)
	}
	switch typed := value.(type) {
	case map[string]any:
		return marshalOrderedHookObject(typed, order)
	case []map[string]any:
		out := []byte{'['}
		for i, element := range typed {
			if i > 0 {
				out = append(out, ',')
			}
			encoded, err := marshalOrderedHookObject(element, order)
			if err != nil {
				return nil, err
			}
			out = append(out, encoded...)
		}
		return append(out, ']'), nil
	case []any:
		out := []byte{'['}
		for i, element := range typed {
			if i > 0 {
				out = append(out, ',')
			}
			encoded, err := marshalOrderedHookValue(element, order)
			if err != nil {
				return nil, err
			}
			out = append(out, encoded...)
		}
		return append(out, ']'), nil
	}
	return marshalRequestBody(value)
}

func marshalOrderedHookObject(object map[string]any, order *HookObjectOrder) ([]byte, error) {
	members := make([]jsonMember, 0, len(object))
	known := make(map[string]bool, len(order.Keys))
	for _, key := range order.Keys {
		known[key] = true
	}
	encode := func(key string, value any) (jsonMember, error) {
		encoded, err := marshalOrderedHookValue(value, order.Nested[key])
		return jsonMember{key: key, value: encoded}, err
	}
	for _, key := range order.Keys {
		if value, present := object[key]; present {
			member, err := encode(key, value)
			if err != nil {
				return nil, err
			}
			members = append(members, member)
		}
	}
	// A member the order does not name is one a caller or a handler added: Pi creates it after the named ones, in the order it was assigned, which a Go map
	// does not keep, so it follows in byte order. An array-index key (a handler's "9") enumerates first in an ECMAScript object, whoever created it.
	var extraKeys []string
	for key := range object {
		if !known[key] {
			extraKeys = append(extraKeys, key)
		}
	}
	slices.Sort(extraKeys)
	extra := make([]jsonMember, 0, len(extraKeys))
	for _, key := range extraKeys {
		member, err := encode(key, object[key])
		if err != nil {
			return nil, err
		}
		extra = append(extra, member)
	}
	members = append(members, extra...)
	jsstring.SortOwnKeys(members, func(member jsonMember) string { return member.key })
	out := []byte{'{'}
	for i, member := range members {
		if i > 0 {
			out = append(out, ',')
		}
		key, err := marshalRequestBody(member.key)
		if err != nil {
			return nil, err
		}
		out = append(out, key...)
		out = append(out, ':')
		out = append(out, member.value...)
	}
	return append(out, '}'), nil
}
