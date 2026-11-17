package ai

import "testing"

// A handler that adds several members to the payload it was handed in place has no insertion order to give (a Go map keeps none), so the members the provider's
// order does not name follow the named ones in byte order, and an ECMAScript object enumerates its own keys with the array-index keys first in ascending
// numeric order, whoever created them. The encoding must be the same on every call, whatever the map's iteration order.
func TestMarshalHookPayloadWritesAddedMembersInOwnKeyOrder(t *testing.T) {
	payload := map[string]any{
		"model":  "m",
		"config": map[string]any{"b": 1, "temperature": 0.5, "10": true, "a": "x", "2": nil, "Z": []any{}},
		"zeta":   1,
		"alpha":  2,
		"10":     3,
		"9":      4,
		"beta":   5,
	}
	order := &HookObjectOrder{Keys: []string{"model", "config"}, Nested: map[string]*HookObjectOrder{"config": {Keys: []string{"temperature"}}}}
	defer HoldHookPayload(payload, order).Release()
	const want = `{"9":4,"10":3,"model":"m","config":{"2":null,"10":true,"temperature":0.5,"Z":[],"a":"x","b":1},"alpha":2,"beta":5,"zeta":1}`
	for range 50 {
		encoded, ok, err := MarshalHookPayload(payload)
		if err != nil || !ok {
			t.Fatalf("MarshalHookPayload = ok %v, err %v", ok, err)
		}
		if string(encoded) != want {
			t.Fatalf("payload = %s\nwant      %s", encoded, want)
		}
	}
}
