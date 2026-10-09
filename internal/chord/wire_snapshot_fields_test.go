package chord

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// wire.ts WireServiceSubscriptionSnapshot is { serviceId, mode, instances }: the encoder keeps the service identity and mode, encodes each instance, and the decoder restores the decoded snapshot; the JSON carries exactly those three members.
func TestWireServiceSubscriptionSnapshotCarriesServiceIdentityModeAndInstances(t *testing.T) {
	snapshot := snapshotOf("pi.models", ServiceKeyed,
		ServiceInstanceSnapshot{Instance: &ServiceInstanceAddress{Key: "a", Generation: 1}, Members: []ServiceMemberSnapshot{stateMember("state", 0, Op{"r", chordjson.ObjectOf("revision", 1.0)})}},
		ServiceInstanceSnapshot{Instance: &ServiceInstanceAddress{Key: "b", Generation: 1}, Members: []ServiceMemberSnapshot{stateMember("state", 0, Op{"r", chordjson.ObjectOf("revision", 2.0)})}},
	)
	var wire WireServiceSubscriptionSnapshot
	wire, err := CreateServiceStateEncoder().EncodeSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if wire.ServiceId != "pi.models" || wire.Mode != ServiceKeyed || len(wire.Instances) != 2 {
		t.Fatalf("wire snapshot = %+v", wire)
	}
	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil || len(members) != 3 || members["serviceId"] == nil || members["mode"] == nil || members["instances"] == nil {
		t.Fatalf("wire members = %s, %v", data, err)
	}
	parsed, err := ParseWireServiceSubscriptionSnapshot(data)
	if err != nil || !reflect.DeepEqual(parsed, wire) {
		t.Fatalf("parsed = %+v, %v", parsed, err)
	}
	decoded, err := CreateServiceStateDecoder().DecodeSnapshot(parsed)
	if err != nil || !reflect.DeepEqual(canon(t, decoded), canon(t, snapshot)) {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
	if _, err := ParseWireServiceSubscriptionSnapshot([]byte(`{"serviceId":"pi.models","mode":"keyed"}`)); err == nil {
		t.Fatal("a snapshot without instances was accepted")
	}
}

// wire.ts:15-18 WireServiceInstanceSnapshot is { instance?, members }: a singleton snapshot omits instance, a keyed one carries the address,
// members always encode (an empty list stays []), and a method member carries no sequence or ops.
func TestWireServiceInstanceSnapshotOmitsInstanceForSingletonsAndKeepsMembers(t *testing.T) {
	for _, c := range []struct {
		name     string
		snapshot WireServiceInstanceSnapshot
		want     string
	}{
		{"singleton without members", WireServiceInstanceSnapshot{Members: []WireServiceMemberSnapshot{}}, `{"members":[]}`},
		{"keyed with a method", WireServiceInstanceSnapshot{Instance: &ServiceInstanceAddress{Key: "a", Generation: 2}, Members: []WireServiceMemberSnapshot{{Name: "go", Kind: MemberMethod}}}, `{"instance":{"key":"a","generation":2},"members":[{"name":"go","kind":"method"}]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			data, err := json.Marshal(c.snapshot)
			if err != nil || string(data) != c.want {
				t.Fatalf("encoded = %s (%v), want %s", data, err, c.want)
			}
			var back WireServiceInstanceSnapshot
			if err := json.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(back, c.snapshot) {
				t.Fatalf("decoded = %+v (%v)", back, err)
			}
		})
	}
}

// types.ts ServiceProviderUpdate / wire.ts WireServiceProviderUpdate: `type` is the closed union "state" | "reset" | "unavailable" |
// "replaced" | "spawned" | "closed". The Go field is the named ServiceProviderUpdateType, not a bare string, and a wire update outside the
// union is rejected.
func TestProviderUpdateTypeIsTheClosedPiUnion(t *testing.T) {
	want := map[ServiceProviderUpdateType]string{UpdateState: "state", UpdateReset: "reset", UpdateUnavailable: "unavailable", UpdateReplaced: "replaced", UpdateSpawned: "spawned", UpdateClosed: "closed"}
	for got, literal := range want {
		if string(got) != literal {
			t.Errorf("constant %q, want %q", got, literal)
		}
	}
	if reflect.TypeOf(ServiceProviderUpdate{}.Type) != reflect.TypeFor[ServiceProviderUpdateType]() {
		t.Fatal("the update type field is not ServiceProviderUpdateType")
	}
	// Every member of the wire union reports its own `type`, and a wire update outside the union is rejected.
	for _, member := range []struct {
		update  WireServiceProviderUpdate
		literal ServiceProviderUpdateType
	}{
		{WireStateServiceProviderUpdate{}, UpdateState}, {WireResetServiceProviderUpdate{}, UpdateReset}, {WireUnavailableServiceProviderUpdate{}, UpdateUnavailable},
		{WireReplacedServiceProviderUpdate{}, UpdateReplaced}, {WireSpawnedServiceProviderUpdate{}, UpdateSpawned}, {WireClosedServiceProviderUpdate{}, UpdateClosed},
	} {
		if member.update.Type() != member.literal {
			t.Errorf("%T.Type() = %q, want %q", member.update, member.update.Type(), member.literal)
		}
	}
	if update, err := UnmarshalWireServiceProviderUpdate([]byte(`{"type":"bogus"}`)); err == nil {
		t.Fatalf("a wire update with an unknown type was accepted: %+v", update)
	}
	if update, err := UnmarshalWireServiceProviderUpdate([]byte(`{"type":"unavailable"}`)); err != nil || update.Type() != UpdateUnavailable {
		t.Fatalf("unavailable = %+v (%v)", update, err)
	}
}
