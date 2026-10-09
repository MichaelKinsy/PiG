package chord

// Ports packages/chord/src/services/state-codec.ts

import (
	"fmt"
	"sync"
)

type serviceStateKey struct {
	hasInstance bool
	key         string
	generation  int
	member      string
}

func stateCodecKey(instance *ServiceInstanceAddress, member string) serviceStateKey {
	key := serviceStateKey{member: member}
	if instance != nil {
		key.hasInstance = true
		key.key = instance.Key
		key.generation = instance.Generation
	}
	return key
}
func describeServiceState(instance *ServiceInstanceAddress, member string) string {
	if instance == nil {
		return member
	}
	return fmt.Sprintf("%s@%d.%s", instance.Key, instance.Generation, member)
}

type stateCodecRegistry[C any] struct {
	create  func() C
	entries map[serviceStateKey]C
}

func newStateCodecRegistry[C any](create func() C) stateCodecRegistry[C] {
	return stateCodecRegistry[C]{create: create, entries: make(map[serviceStateKey]C)}
}
func (r *stateCodecRegistry[C]) add(instance *ServiceInstanceAddress, member string) (C, error) {
	key := stateCodecKey(instance, member)
	if _, present := r.entries[key]; present {
		var zero C
		return zero, fmt.Errorf("Duplicate service state %s", describeServiceState(instance, member))
	}
	codec := r.create()
	r.entries[key] = codec
	return codec, nil
}
func (r *stateCodecRegistry[C]) get(instance *ServiceInstanceAddress, member string) (C, error) {
	codec, present := r.entries[stateCodecKey(instance, member)]
	if !present {
		return codec, fmt.Errorf("Unknown service state %s", describeServiceState(instance, member))
	}
	return codec, nil
}
func (r *stateCodecRegistry[C]) removeInstance(instance *ServiceInstanceAddress) {
	if instance == nil {
		return
	}
	for key := range r.entries {
		if key.hasInstance && key.key == instance.Key && key.generation == instance.Generation {
			delete(r.entries, key)
		}
	}
}

// ServiceStateEncoder owns one delta encoder per replicated member in one subscription. Callers preserve update order; sequence-gap handling belongs to replicas.
type ServiceStateEncoder struct {
	mu     sync.Mutex
	codecs stateCodecRegistry[*Encoder]
}

func CreateServiceStateEncoder() *ServiceStateEncoder {
	return &ServiceStateEncoder{codecs: newStateCodecRegistry(NewEncoder)}
}
func (e *ServiceStateEncoder) EncodeSnapshot(snapshot ServiceSubscriptionSnapshot) (WireServiceSubscriptionSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	clear(e.codecs.entries)
	out := WireServiceSubscriptionSnapshot{ServiceId: snapshot.ServiceId, Mode: snapshot.Mode, Instances: make([]WireServiceInstanceSnapshot, 0, len(snapshot.Instances))}
	for _, instance := range snapshot.Instances {
		encoded, err := e.encodeInstance(instance)
		if err != nil {
			return WireServiceSubscriptionSnapshot{}, err
		}
		out.Instances = append(out.Instances, encoded)
	}
	return out, nil
}
func (e *ServiceStateEncoder) encodeInstance(instance ServiceInstanceSnapshot) (WireServiceInstanceSnapshot, error) {
	out := WireServiceInstanceSnapshot{Instance: instance.Instance, Members: make([]WireServiceMemberSnapshot, 0, len(instance.Members))}
	for _, member := range instance.Members {
		encoded := WireServiceMemberSnapshot{Name: member.Name, Kind: member.Kind, Sequence: member.Sequence}
		if member.Kind == MemberState {
			codec, err := e.codecs.add(instance.Instance, member.Name)
			if err != nil {
				return WireServiceInstanceSnapshot{}, err
			}
			encoded.Ops, err = codec.Encode(member.Ops)
			if err != nil {
				return WireServiceInstanceSnapshot{}, err
			}
		}
		out.Members = append(out.Members, encoded)
	}
	return out, nil
}
func (e *ServiceStateEncoder) EncodeUpdate(update ServiceProviderUpdate) (WireServiceProviderUpdate, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch update.Type {
	case UpdateState:
		out := WireStateServiceProviderUpdate{Instance: update.Instance, Member: update.Member, Sequence: update.Sequence}
		codec, err := e.codecs.get(update.Instance, update.Member)
		if err != nil {
			return out, err
		}
		out.Ops, err = codec.Encode(update.Ops)
		return out, err
	case UpdateReset:
		if update.Reset == nil {
			return WireResetServiceProviderUpdate{}, fmt.Errorf("Invalid service subscription snapshot")
		}
		clear(e.codecs.entries)
		reset := WireServiceSubscriptionSnapshot{ServiceId: update.Reset.ServiceId, Mode: update.Reset.Mode, Instances: make([]WireServiceInstanceSnapshot, 0, len(update.Reset.Instances))}
		for _, instance := range update.Reset.Instances {
			encoded, err := e.encodeInstance(instance)
			if err != nil {
				return WireResetServiceProviderUpdate{}, err
			}
			reset.Instances = append(reset.Instances, encoded)
		}
		return WireResetServiceProviderUpdate{Snapshot: reset}, nil
	case UpdateReplaced:
		clear(e.codecs.entries)
		if update.Snapshot == nil {
			return WireReplacedServiceProviderUpdate{}, fmt.Errorf("Invalid service instance snapshot")
		}
		instance, err := e.encodeInstance(*update.Snapshot)
		return WireReplacedServiceProviderUpdate{Snapshot: instance}, err
	case UpdateSpawned:
		if update.Snapshot == nil {
			return WireSpawnedServiceProviderUpdate{}, fmt.Errorf("Invalid service instance snapshot")
		}
		instance, err := e.encodeInstance(*update.Snapshot)
		return WireSpawnedServiceProviderUpdate{Instance: instance}, err
	case UpdateUnavailable:
		clear(e.codecs.entries)
		return WireUnavailableServiceProviderUpdate{}, nil
	case UpdateClosed:
		e.codecs.removeInstance(update.Instance)
		out := WireClosedServiceProviderUpdate{}
		if update.Instance != nil {
			out.Instance = *update.Instance
		}
		return out, nil
	}
	return nil, fmt.Errorf("Invalid service provider update")
}

// ServiceStateDecoder keeps independent path dictionaries for each keyed instance and member, resetting them on snapshot, replacement, unavailability, or a member's base operation.
type ServiceStateDecoder struct {
	mu     sync.Mutex
	codecs stateCodecRegistry[*Decoder]
}

func CreateServiceStateDecoder() *ServiceStateDecoder {
	return &ServiceStateDecoder{codecs: newStateCodecRegistry(NewDecoder)}
}
func (d *ServiceStateDecoder) DecodeSnapshot(snapshot WireServiceSubscriptionSnapshot) (ServiceSubscriptionSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	clear(d.codecs.entries)
	out := ServiceSubscriptionSnapshot{ServiceId: snapshot.ServiceId, Mode: snapshot.Mode, Instances: make([]ServiceInstanceSnapshot, 0, len(snapshot.Instances))}
	for _, instance := range snapshot.Instances {
		decoded, err := d.decodeInstance(instance)
		if err != nil {
			return ServiceSubscriptionSnapshot{}, err
		}
		out.Instances = append(out.Instances, decoded)
	}
	return out, nil
}
func (d *ServiceStateDecoder) decodeInstance(instance WireServiceInstanceSnapshot) (ServiceInstanceSnapshot, error) {
	out := ServiceInstanceSnapshot{Instance: instance.Instance, Members: make([]ServiceMemberSnapshot, 0, len(instance.Members))}
	for _, member := range instance.Members {
		decoded := ServiceMemberSnapshot{Name: member.Name, Kind: member.Kind, Sequence: member.Sequence}
		if member.Kind == MemberState {
			codec, err := d.codecs.add(instance.Instance, member.Name)
			if err != nil {
				return ServiceInstanceSnapshot{}, err
			}
			decoded.Ops, err = codec.Decode(member.Ops)
			if err != nil {
				return ServiceInstanceSnapshot{}, err
			}
		}
		out.Members = append(out.Members, decoded)
	}
	return out, nil
}
func (d *ServiceStateDecoder) DecodeUpdate(update WireServiceProviderUpdate) (ServiceProviderUpdate, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch update := update.(type) {
	case WireStateServiceProviderUpdate:
		out := ServiceProviderUpdate{Type: UpdateState, Instance: update.Instance, Member: update.Member, Sequence: update.Sequence}
		codec, err := d.codecs.get(update.Instance, update.Member)
		if err != nil {
			return out, err
		}
		out.Ops, err = codec.Decode(update.Ops)
		return out, err
	case WireResetServiceProviderUpdate:
		out := ServiceProviderUpdate{Type: UpdateReset}
		clear(d.codecs.entries)
		reset := ServiceSubscriptionSnapshot{ServiceId: update.Snapshot.ServiceId, Mode: update.Snapshot.Mode, Instances: make([]ServiceInstanceSnapshot, 0, len(update.Snapshot.Instances))}
		for _, instance := range update.Snapshot.Instances {
			decoded, err := d.decodeInstance(instance)
			if err != nil {
				return out, err
			}
			reset.Instances = append(reset.Instances, decoded)
		}
		out.Reset = &reset
		return out, nil
	case WireReplacedServiceProviderUpdate:
		out := ServiceProviderUpdate{Type: UpdateReplaced}
		clear(d.codecs.entries)
		instance, err := d.decodeInstance(update.Snapshot)
		out.Snapshot = &instance
		return out, err
	case WireSpawnedServiceProviderUpdate:
		out := ServiceProviderUpdate{Type: UpdateSpawned}
		instance, err := d.decodeInstance(update.Instance)
		out.Snapshot = &instance
		return out, err
	case WireUnavailableServiceProviderUpdate:
		clear(d.codecs.entries)
		return ServiceProviderUpdate{Type: UpdateUnavailable}, nil
	case WireClosedServiceProviderUpdate:
		d.codecs.removeInstance(&update.Instance)
		return ServiceProviderUpdate{Type: UpdateClosed, Instance: &update.Instance}, nil
	}
	return ServiceProviderUpdate{}, fmt.Errorf("Invalid service provider update")
}
