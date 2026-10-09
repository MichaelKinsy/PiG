package chord

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// packages/chord/src/services/wire.ts:11-25: a wire member is {name, kind:"method"} or {name, kind:"state", sequence, ops}; an instance snapshot is
// {instance?, members}; a subscription snapshot is {serviceId, mode, instances}. The wire types serialize to exactly those objects, in that order,
// omit an absent instance, and round-trip through the strict parsers.
func TestWireServiceSnapshotsSerializeLikePi(t *testing.T) {
	address := &ServiceInstanceAddress{Key: "k", Generation: 2}
	cases := []struct {
		name  string
		value any
		want  string
		parse func(json.RawMessage) (any, error)
	}{
		{"instance without address and a method member", WireServiceInstanceSnapshot{Members: []WireServiceMemberSnapshot{{Name: "call", Kind: MemberMethod}}}, `{"members":[{"name":"call","kind":"method"}]}`, nil},
		{"instance with address and a state member", WireServiceInstanceSnapshot{Instance: address, Members: []WireServiceMemberSnapshot{{Name: "state", Kind: MemberState, Sequence: 3, Ops: []WireOp{{"r", chordjson.ObjectOf("a", 1.0)}}}}}, `{"instance":{"key":"k","generation":2},"members":[{"name":"state","kind":"state","sequence":3,"ops":[["r",{"a":1}]]}]}`, nil},
		{"state member without ops is an empty list", WireServiceInstanceSnapshot{Members: []WireServiceMemberSnapshot{{Name: "state", Kind: MemberState}}}, `{"members":[{"name":"state","kind":"state","sequence":0,"ops":[]}]}`, nil},
		{"subscription snapshot", WireServiceSubscriptionSnapshot{ServiceId: "pi.models", Mode: ServiceSingleton, Instances: []WireServiceInstanceSnapshot{{Members: []WireServiceMemberSnapshot{}}}}, `{"serviceId":"pi.models","mode":"singleton","instances":[{"members":[]}]}`, func(raw json.RawMessage) (any, error) { return ParseWireServiceSubscriptionSnapshot(raw) }},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.value)
		if err != nil || string(got) != c.want {
			t.Errorf("%s: %s, %v; want %s", c.name, got, err, c.want)
			continue
		}
		if c.parse != nil {
			parsed, err := c.parse(got)
			again, _ := json.Marshal(parsed)
			if err != nil || string(again) != c.want {
				t.Errorf("%s: parsed and re-serialized %s, %v", c.name, again, err)
			}
		}
	}
}
