package chord

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Ports packages/chord/test/service-wire.test.ts. The parsers take JSON text, so the upstream "returns the same object" assertions become equality of the parsed value with its source.

func raw(t *testing.T, text string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(text)) {
		t.Fatalf("invalid JSON %q", text)
	}
	return json.RawMessage(text)
}

func snapshotOf(serviceId string, mode ServiceMode, instances ...ServiceInstanceSnapshot) ServiceSubscriptionSnapshot {
	if instances == nil {
		instances = []ServiceInstanceSnapshot{}
	}
	return ServiceSubscriptionSnapshot{ServiceId: serviceId, Mode: mode, Instances: instances}
}

func stateMember(name string, sequence int, ops ...Op) ServiceMemberSnapshot {
	return ServiceMemberSnapshot{Name: name, Kind: MemberState, Sequence: sequence, Ops: ops}
}

func stateUpdate(member string, sequence int, ops ...Op) ServiceProviderUpdate {
	return ServiceProviderUpdate{Type: UpdateState, Member: member, Sequence: sequence, Ops: ops}
}

func TestServiceWireProtocol(t *testing.T) {
	t.Run("encodes control calls and validates service values", func(t *testing.T) {
		if got, ok := DecodeServiceControlCall(CreateServiceCatalogueCall()); !ok || got.Type != "catalogue" {
			t.Fatalf("catalogue call = %+v", got)
		}
		catalogue, err := ParseServiceCatalogue(raw(t, `[{"serviceId":"pi.models","mode":"singleton"},{"serviceId":"pi.dialogs","mode":"keyed"}]`))
		if err != nil || !reflect.DeepEqual(catalogue, []ServiceCatalogueEntry{{"pi.models", ServiceSingleton}, {"pi.dialogs", ServiceKeyed}}) {
			t.Fatalf("catalogue = %+v, %v", catalogue, err)
		}
		subscribe, ok := DecodeServiceControlCall(CreateServiceSubscribeCall("subscription-1", "pi.models", ServiceSingleton))
		if !ok || subscribe.Type != "subscribe" || subscribe.SubscriptionId != "subscription-1" || subscribe.ServiceId != "pi.models" || subscribe.Mode != ServiceSingleton {
			t.Fatalf("subscribe call = %+v", subscribe)
		}
		unsubscribe, ok := DecodeServiceControlCall(CreateServiceUnsubscribeCall("subscription-1"))
		if !ok || unsubscribe.Type != "unsubscribe" || unsubscribe.SubscriptionId != "subscription-1" {
			t.Fatalf("unsubscribe call = %+v", unsubscribe)
		}
		call, err := ParseServiceCall(raw(t, `{"serviceId":"pi.question-dialog","instance":{"key":"invocation-1","generation":2},"member":"submit","args":[{"outcome":"selected","index":0}]}`))
		if err != nil || call.Member != "submit" {
			t.Fatalf("call = %+v, %v", call, err)
		}
	})

	t.Run("rejects malformed service values", func(t *testing.T) {
		if _, err := ParseServiceCall(raw(t, `{"serviceId":"pi.models","member":"list","args":[],"extra":true}`)); err == nil || err.Error() != "Invalid service call" {
			t.Fatalf("error = %v", err)
		}
		if _, err := ParseServiceCatalogue(raw(t, `[{"serviceId":"pi.models","mode":"unknown"}]`)); err == nil || err.Error() != "Invalid service catalogue" {
			t.Fatalf("error = %v", err)
		}
		if _, err := ParseServiceProviderUpdate(raw(t, `{"type":"state","member":"state","sequence":0,"ops":[]}`)); err == nil || err.Error() != "Invalid service state update" {
			t.Fatalf("error = %v", err)
		}
		if _, err := ParseWireServiceProviderUpdate(raw(t, `{"type":"state","member":"state","sequence":1,"ops":[["?",0]]}`)); err == nil {
			t.Fatal("unknown wire verb accepted")
		}
	})

	t.Run("validates decoded and wire snapshots and updates", func(t *testing.T) {
		snapshot := snapshotOf("pi.models", ServiceSingleton, ServiceInstanceSnapshot{Members: []ServiceMemberSnapshot{stateMember("state", 0, Op{"r", map[string]any{"revision": 1.0}})}})
		parsed, err := ParseServiceSubscriptionSnapshot(mustRaw(snapshot))
		if err != nil || !reflect.DeepEqual(canon(t, parsed), canon(t, snapshot)) {
			t.Fatalf("snapshot = %+v, %v", parsed, err)
		}
		encoder := CreateServiceStateEncoder()
		wireSnapshot, err := encoder.EncodeSnapshot(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		parsedWire, err := ParseWireServiceSubscriptionSnapshot(mustRaw(wireSnapshot))
		if err != nil || !reflect.DeepEqual(canon(t, parsedWire), canon(t, wireSnapshot)) {
			t.Fatalf("wire snapshot = %+v, %v", parsedWire, err)
		}
		update := stateUpdate("state", 1, Op{"s", []any{"revision"}, 2.0})
		parsedUpdate, err := ParseServiceProviderUpdate(mustRaw(update))
		if err != nil || !reflect.DeepEqual(canon(t, parsedUpdate), canon(t, update)) {
			t.Fatalf("update = %+v, %v", parsedUpdate, err)
		}
		wireUpdate, err := encoder.EncodeUpdate(update)
		if err != nil {
			t.Fatal(err)
		}
		parsedWireUpdate, err := ParseWireServiceProviderUpdate(mustRaw(wireUpdate))
		if err != nil || !reflect.DeepEqual(canon(t, parsedWireUpdate), canon(t, wireUpdate)) {
			t.Fatalf("wire update = %+v, %v", parsedWireUpdate, err)
		}
	})

	t.Run("keeps one operation codec pair for one subscription state", func(t *testing.T) {
		enc, dec := CreateServiceStateEncoder(), CreateServiceStateDecoder()
		snapshot := snapshotOf("pi.models", ServiceSingleton, ServiceInstanceSnapshot{Members: []ServiceMemberSnapshot{stateMember("state", 0, Op{"r", map[string]any{"revision": 0.0}})}})
		wire, err := enc.EncodeSnapshot(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := dec.DecodeSnapshot(wire)
		if err != nil || !reflect.DeepEqual(canon(t, decoded), canon(t, snapshot)) {
			t.Fatalf("decoded = %+v, %v", decoded, err)
		}
		first := stateUpdate("state", 1, Op{"s", []any{"revision"}, 1.0})
		second := stateUpdate("state", 2, Op{"s", []any{"revision"}, 2.0})
		firstWire, err := enc.EncodeUpdate(first)
		if err != nil {
			t.Fatal(err)
		}
		secondWire, err := enc.EncodeUpdate(second)
		if err != nil {
			t.Fatal(err)
		}
		if got := mustMarshal(t, firstWire.Ops); got != `[["s",["revision"],1]]` {
			t.Fatalf("first ops = %s", got)
		}
		if got := mustMarshal(t, secondWire.Ops); got != `[["#",0,["revision"]],["s",0,2]]` {
			t.Fatalf("second ops = %s", got)
		}
		for _, pair := range []struct {
			wire   WireServiceProviderUpdate
			update ServiceProviderUpdate
		}{{firstWire, first}, {secondWire, second}} {
			decodedUpdate, err := dec.DecodeUpdate(pair.wire)
			if err != nil || !reflect.DeepEqual(canon(t, decodedUpdate), canon(t, pair.update)) {
				t.Fatalf("decoded update = %+v, %v", decodedUpdate, err)
			}
		}
	})

	t.Run("validates explicit resets and restarts path dictionaries at the new baseline", func(t *testing.T) {
		enc, dec := CreateServiceStateEncoder(), CreateServiceStateDecoder()
		snapshot := snapshotOf("pi.states", ServiceSingleton, ServiceInstanceSnapshot{Members: []ServiceMemberSnapshot{stateMember("state", 0, Op{"r", map[string]any{"before": 0.0}})}})
		wire, err := enc.EncodeSnapshot(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dec.DecodeSnapshot(wire); err != nil {
			t.Fatal(err)
		}
		roundTrip := func(update ServiceProviderUpdate) ServiceProviderUpdate {
			t.Helper()
			encoded, err := enc.EncodeUpdate(update)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := dec.DecodeUpdate(encoded)
			if err != nil {
				t.Fatal(err)
			}
			return decoded
		}
		for sequence := 1; sequence <= 2; sequence++ {
			roundTrip(stateUpdate("state", sequence, Op{"s", []any{"before"}, float64(sequence)}))
		}
		resetSnapshot := snapshotOf("pi.states", ServiceSingleton, ServiceInstanceSnapshot{Members: []ServiceMemberSnapshot{stateMember("state", 103, Op{"r", map[string]any{"after": 103.0}})}})
		reset := ServiceProviderUpdate{Type: UpdateReset, Reset: &resetSnapshot}
		parsed, err := ParseServiceProviderUpdate(mustRaw(reset))
		if err != nil || !reflect.DeepEqual(canon(t, parsed), canon(t, reset)) {
			t.Fatalf("parsed reset = %+v, %v", parsed, err)
		}
		wireReset, err := enc.EncodeUpdate(reset)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseWireServiceProviderUpdate(mustRaw(wireReset)); err != nil {
			t.Fatal(err)
		}
		decodedReset, err := dec.DecodeUpdate(wireReset)
		if err != nil || !reflect.DeepEqual(canon(t, decodedReset), canon(t, reset)) {
			t.Fatalf("decoded reset = %+v, %v", decodedReset, err)
		}
		for sequence := 104; sequence <= 106; sequence++ {
			update := stateUpdate("state", sequence, Op{"s", []any{"after"}, float64(sequence)})
			if got := roundTrip(update); !reflect.DeepEqual(canon(t, got), canon(t, update)) {
				t.Fatalf("update after reset = %+v", got)
			}
		}
		resetText := string(mustRaw(reset))
		for name, parse := range map[string]func(json.RawMessage) error{
			"decoded": func(r json.RawMessage) error { _, err := ParseServiceProviderUpdate(r); return err },
			"wire":    func(r json.RawMessage) error { _, err := ParseWireServiceProviderUpdate(r); return err },
		} {
			if parse(raw(t, strings.TrimSuffix(resetText, "}")+`,"extra":true}`)) == nil {
				t.Fatalf("%s: extra key accepted", name)
			}
			if parse(raw(t, `{"type":"reset","snapshot":{}}`)) == nil {
				t.Fatalf("%s: empty snapshot accepted", name)
			}
			err := parse(raw(t, `{"type":"reset","snapshot":{"serviceId":"pi.states","mode":"singleton","instances":[{"members":[{"name":"state","kind":"state","sequence":103,"ops":[["s",["before"],103]]}]}]}}`))
			if err == nil || !strings.Contains(err.Error(), "full root replacements") {
				t.Fatalf("%s: error = %v", name, err)
			}
		}
	})

	t.Run("isolates operation dictionaries between states and subscriptions", func(t *testing.T) {
		snapshot := snapshotOf("pi.states", ServiceSingleton, ServiceInstanceSnapshot{Members: []ServiceMemberSnapshot{
			stateMember("left", 0, Op{"r", map[string]any{"revision": 0.0}}), stateMember("right", 0, Op{"r", map[string]any{"revision": 0.0}}),
		}})
		type pairOfCodecs struct {
			enc *ServiceStateEncoder
			dec *ServiceStateDecoder
		}
		open := func() pairOfCodecs {
			enc, dec := CreateServiceStateEncoder(), CreateServiceStateDecoder()
			wire, err := enc.EncodeSnapshot(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dec.DecodeSnapshot(wire); err != nil {
				t.Fatal(err)
			}
			return pairOfCodecs{enc, dec}
		}
		first, second := open(), open()
		update := func(member string, sequence, revision int) ServiceProviderUpdate {
			return stateUpdate(member, sequence, Op{"s", []any{"revision"}, float64(revision)})
		}
		encode := func(codecs pairOfCodecs, update ServiceProviderUpdate) WireServiceProviderUpdate {
			t.Helper()
			wire, err := codecs.enc.EncodeUpdate(update)
			if err != nil {
				t.Fatal(err)
			}
			return wire
		}
		expectOps := func(wire WireServiceProviderUpdate, want string) {
			t.Helper()
			if got := mustMarshal(t, wire.Ops); got != want {
				t.Fatalf("ops = %s, want %s", got, want)
			}
		}
		expectDecoded := func(codecs pairOfCodecs, wire WireServiceProviderUpdate, want ServiceProviderUpdate) {
			t.Helper()
			got, err := codecs.dec.DecodeUpdate(wire)
			if err != nil || !reflect.DeepEqual(canon(t, got), canon(t, want)) {
				t.Fatalf("decoded = %+v, %v", got, err)
			}
		}
		firstLeft, firstRight := encode(first, update("left", 1, 1)), encode(first, update("right", 1, 1))
		secondLeft, secondRight := encode(first, update("left", 2, 2)), encode(first, update("right", 2, 2))
		expectOps(firstLeft, `[["s",["revision"],1]]`)
		expectOps(firstRight, `[["s",["revision"],1]]`)
		expectOps(secondLeft, `[["#",0,["revision"]],["s",0,2]]`)
		expectOps(secondRight, `[["#",0,["revision"]],["s",0,2]]`)
		expectDecoded(first, firstLeft, update("left", 1, 1))
		expectDecoded(first, firstRight, update("right", 1, 1))
		expectDecoded(first, secondLeft, update("left", 2, 2))
		expectDecoded(first, secondRight, update("right", 2, 2))

		independentLeft := encode(second, update("left", 1, 1))
		expectOps(independentLeft, `[["s",["revision"],1]]`)
		expectDecoded(second, independentLeft, update("left", 1, 1))

		leftBase := stateUpdate("left", 3, Op{"r", map[string]any{"revision": 3.0}})
		expectDecoded(first, encode(first, leftBase), leftBase)
		thirdRight := encode(first, update("right", 3, 3))
		expectOps(thirdRight, `[["s",0,3]]`)
		expectDecoded(first, thirdRight, update("right", 3, 3))
	})

	t.Run("creates and removes keyed instance codecs with their lifecycle", func(t *testing.T) {
		enc, dec := CreateServiceStateEncoder(), CreateServiceStateDecoder()
		snapshot := snapshotOf("pi.dialogs", ServiceKeyed)
		wire, err := enc.EncodeSnapshot(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if decoded, err := dec.DecodeSnapshot(wire); err != nil || !reflect.DeepEqual(canon(t, decoded), canon(t, snapshot)) {
			t.Fatalf("decoded = %+v, %v", decoded, err)
		}
		address := &ServiceInstanceAddress{Key: "dialog-1", Generation: 1}
		roundTrip := func(update ServiceProviderUpdate) {
			t.Helper()
			encoded, err := enc.EncodeUpdate(update)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := dec.DecodeUpdate(encoded)
			if err != nil || !reflect.DeepEqual(canon(t, decoded), canon(t, update)) {
				t.Fatalf("decoded = %+v, %v", decoded, err)
			}
		}
		roundTrip(ServiceProviderUpdate{Type: UpdateSpawned, Snapshot: &ServiceInstanceSnapshot{Instance: address, Members: []ServiceMemberSnapshot{stateMember("request", 0, Op{"r", map[string]any{"value": 0.0}})}}})
		update := ServiceProviderUpdate{Type: UpdateState, Address: address, Member: "request", Sequence: 1, Ops: []Op{{"s", []any{"value"}, 1.0}}}
		roundTrip(update)
		roundTrip(ServiceProviderUpdate{Type: UpdateClosed, Address: address})
		update.Sequence = 2
		if _, err := enc.EncodeUpdate(update); err == nil || !strings.Contains(err.Error(), "Unknown service state") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestRemoteServiceEndpointsPublishAndCleanUpProviderSubscriptions(t *testing.T) {
	ctx := context.Background()
	provider := newDeliveryProvider(t, SingletonService(deliveryCounterDefinition))
	counter := newDeliveryCounter(t, 0)
	provideCounter(t, provider, counter)
	endpoint := CreateRemoteServiceEndpoint(provider)
	updates := &locked[ServiceProviderUpdate]{}
	publish := func(_ context.Context, _ string, update ServiceProviderUpdate) error {
		updates.add(update)
		return nil
	}
	catalogue, err := endpoint.Invoke(ctx, CreateServiceCatalogueCall(), publish)
	if err != nil || string(catalogue) != `[{"serviceId":"`+deliveryCounterDefinition.Id()+`","mode":"singleton"}]` {
		t.Fatalf("catalogue = %s, %v", catalogue, err)
	}
	subscribed, err := endpoint.Invoke(ctx, CreateServiceSubscribeCall("subscription-1", deliveryCounterDefinition.Id(), ServiceSingleton), publish)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot ServiceSubscriptionSnapshot
	if err := json.Unmarshal(subscribed, &snapshot); err != nil || snapshot.ServiceId != deliveryCounterDefinition.Id() || snapshot.Mode != ServiceSingleton {
		t.Fatalf("snapshot = %s, %v", subscribed, err)
	}
	counter.set(t, 1)
	want := []ServiceProviderUpdate{stateUpdate("state", 1, Op{"s", []any{"value"}, 1.0})}
	if got := updates.get(); !reflect.DeepEqual(canon(t, got), canon(t, want)) {
		t.Fatalf("updates = %s", mustMarshal(t, got))
	}
	endpoint.Dispose()
	counter.set(t, 2)
	if got := len(updates.get()); got != 1 {
		t.Fatalf("updates after dispose = %d", got)
	}
	_ = provider.Dispose()
}
