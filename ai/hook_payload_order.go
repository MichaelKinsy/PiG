package ai

import (
	"reflect"
	"slices"
	"strings"
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

// hookPayloadOrders holds the order of each payload map a provider has handed to a hook that has not returned, by the map's address.
var hookPayloadOrders sync.Map

// HoldHookPayload records the member order of payload until the returned function is called, which the provider does when the hook has returned.
func HoldHookPayload(payload map[string]any, order *HookObjectOrder) (release func()) {
	key := reflect.ValueOf(payload).Pointer()
	hookPayloadOrders.Store(key, order)
	return func() { hookPayloadOrders.Delete(key) }
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
	order, held := hookPayloadOrders.Load(reflect.ValueOf(object).Pointer())
	if !held {
		return nil, false, nil
	}
	encoded, err = marshalOrderedHookObject(object, order.(*HookObjectOrder))
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
	var extra []jsonMember
	for _, key := range order.Keys {
		known[key] = true
	}
	encode := func(key string, value any) error {
		encoded, err := marshalOrderedHookValue(value, order.Nested[key])
		if err != nil {
			return err
		}
		member := jsonMember{key: key, value: encoded}
		if known[key] {
			members = append(members, member)
		} else {
			extra = append(extra, member)
		}
		return nil
	}
	for _, key := range order.Keys {
		if value, present := object[key]; present {
			if err := encode(key, value); err != nil {
				return nil, err
			}
		}
	}
	for key, value := range object {
		if !known[key] {
			if err := encode(key, value); err != nil {
				return nil, err
			}
		}
	}
	slices.SortFunc(extra, func(a, b jsonMember) int { return strings.Compare(a.key, b.key) })
	jsstring.SortOwnKeys(extra, func(member jsonMember) string { return member.key })
	members = append(members, extra...)
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
