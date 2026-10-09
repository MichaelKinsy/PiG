package chord

// Ports packages/chord/src/services/wire.ts

import (
	"encoding/json"
	"errors"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// WireServiceMemberSnapshot carries compressed WireOps, never decoded operations.
type WireServiceMemberSnapshot struct {
	Name     string
	Kind     ServiceMemberKind
	Sequence int
	Ops      []WireOp
}
type WireServiceInstanceSnapshot struct {
	Instance *ServiceInstanceAddress     `json:"instance,omitempty"`
	Members  []WireServiceMemberSnapshot `json:"members"`
}
type WireServiceSubscriptionSnapshot struct {
	ServiceId string                        `json:"serviceId"`
	Mode      ServiceMode                   `json:"mode"`
	Instances []WireServiceInstanceSnapshot `json:"instances"`
}

// WireServiceProviderUpdate is wire.ts WireServiceProviderUpdate, a service provider update with its state operations compressed. It is closed over the six
// members below, one struct per `type` of the union.
type WireServiceProviderUpdate interface {
	// Type is the update's `type` member.
	Type() ServiceProviderUpdateType
	wireServiceProviderUpdate()
}

// WireStateServiceProviderUpdate is the "state" member: one state member's operations, for a keyed instance when Instance is set.
type WireStateServiceProviderUpdate struct {
	Instance *ServiceInstanceAddress
	Member   string
	Sequence int
	Ops      []WireOp
}

// WireResetServiceProviderUpdate is the "reset" member: the whole subscription again.
type WireResetServiceProviderUpdate struct {
	Snapshot WireServiceSubscriptionSnapshot
}

// WireUnavailableServiceProviderUpdate is the "unavailable" member.
type WireUnavailableServiceProviderUpdate struct{}

// WireReplacedServiceProviderUpdate is the "replaced" member: a new instance in place of the old one.
type WireReplacedServiceProviderUpdate struct{ Snapshot WireServiceInstanceSnapshot }

// WireSpawnedServiceProviderUpdate is the "spawned" member: a keyed instance that appeared.
type WireSpawnedServiceProviderUpdate struct{ Instance WireServiceInstanceSnapshot }

// WireClosedServiceProviderUpdate is the "closed" member: a keyed instance that went away.
type WireClosedServiceProviderUpdate struct{ Instance ServiceInstanceAddress }

func (WireStateServiceProviderUpdate) Type() ServiceProviderUpdateType { return UpdateState }
func (WireResetServiceProviderUpdate) Type() ServiceProviderUpdateType { return UpdateReset }
func (WireUnavailableServiceProviderUpdate) Type() ServiceProviderUpdateType {
	return UpdateUnavailable
}
func (WireReplacedServiceProviderUpdate) Type() ServiceProviderUpdateType { return UpdateReplaced }
func (WireSpawnedServiceProviderUpdate) Type() ServiceProviderUpdateType  { return UpdateSpawned }
func (WireClosedServiceProviderUpdate) Type() ServiceProviderUpdateType   { return UpdateClosed }

func (WireStateServiceProviderUpdate) wireServiceProviderUpdate()       {}
func (WireResetServiceProviderUpdate) wireServiceProviderUpdate()       {}
func (WireUnavailableServiceProviderUpdate) wireServiceProviderUpdate() {}
func (WireReplacedServiceProviderUpdate) wireServiceProviderUpdate()    {}
func (WireSpawnedServiceProviderUpdate) wireServiceProviderUpdate()     {}
func (WireClosedServiceProviderUpdate) wireServiceProviderUpdate()      {}

func (m WireServiceMemberSnapshot) MarshalJSON() ([]byte, error) {
	if m.Kind == MemberMethod {
		return json.Marshal(struct {
			Name string            `json:"name"`
			Kind ServiceMemberKind `json:"kind"`
		}{m.Name, m.Kind})
	}
	ops := m.Ops
	if ops == nil {
		ops = []WireOp{}
	}
	return json.Marshal(struct {
		Name     string            `json:"name"`
		Kind     ServiceMemberKind `json:"kind"`
		Sequence int               `json:"sequence"`
		Ops      []WireOp          `json:"ops"`
	}{m.Name, m.Kind, m.Sequence, ops})
}
func (m *WireServiceMemberSnapshot) UnmarshalJSON(data []byte) error {
	var fields struct {
		Name     string            `json:"name"`
		Kind     ServiceMemberKind `json:"kind"`
		Sequence *int              `json:"sequence"`
		Ops      []WireOp          `json:"ops"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields.Kind == MemberMethod {
		*m = WireServiceMemberSnapshot{Name: fields.Name, Kind: fields.Kind}
		return nil
	}
	if fields.Kind != MemberState || fields.Sequence == nil {
		return errors.New("Invalid service member snapshot")
	}
	*m = WireServiceMemberSnapshot{Name: fields.Name, Kind: fields.Kind, Sequence: *fields.Sequence, Ops: fields.Ops}
	return nil
}

// The MarshalJSON methods below emit members in the order Pi's provider builds them (provider.ts:354-358 {type, instance?, member, sequence, ops}; the other updates {type, snapshot|instance}), which state-consumer replicas and Pi's JSON.stringify observe.
func (u WireStateServiceProviderUpdate) MarshalJSON() ([]byte, error) {
	fields := chordjson.ObjectOf("type", UpdateState)
	if u.Instance != nil {
		fields.Set("instance", u.Instance)
	}
	fields.Set("member", u.Member)
	fields.Set("sequence", u.Sequence)
	ops := u.Ops
	if ops == nil {
		ops = []WireOp{}
	}
	fields.Set("ops", ops)
	return json.Marshal(fields)
}
func (u WireResetServiceProviderUpdate) MarshalJSON() ([]byte, error) {
	return json.Marshal(chordjson.ObjectOf("type", UpdateReset, "snapshot", u.Snapshot))
}
func (WireUnavailableServiceProviderUpdate) MarshalJSON() ([]byte, error) {
	return json.Marshal(chordjson.ObjectOf("type", UpdateUnavailable))
}
func (u WireReplacedServiceProviderUpdate) MarshalJSON() ([]byte, error) {
	return json.Marshal(chordjson.ObjectOf("type", UpdateReplaced, "snapshot", u.Snapshot))
}
func (u WireSpawnedServiceProviderUpdate) MarshalJSON() ([]byte, error) {
	return json.Marshal(chordjson.ObjectOf("type", UpdateSpawned, "instance", u.Instance))
}
func (u WireClosedServiceProviderUpdate) MarshalJSON() ([]byte, error) {
	return json.Marshal(chordjson.ObjectOf("type", UpdateClosed, "instance", u.Instance))
}

// UnmarshalWireServiceProviderUpdate decodes the member of the union that the JSON object's `type` names. It checks no more than the
// decoding needs; [ParseWireServiceProviderUpdate] validates the whole shape first.
func UnmarshalWireServiceProviderUpdate(data []byte) (WireServiceProviderUpdate, error) {
	var fields struct {
		Type     ServiceProviderUpdateType `json:"type"`
		Instance json.RawMessage           `json:"instance"`
		Member   string                    `json:"member"`
		Sequence int                       `json:"sequence"`
		Ops      []WireOp                  `json:"ops"`
		Snapshot json.RawMessage           `json:"snapshot"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	switch fields.Type {
	case UpdateState:
		next := WireStateServiceProviderUpdate{Member: fields.Member, Sequence: fields.Sequence, Ops: fields.Ops}
		if len(fields.Instance) != 0 {
			if err := json.Unmarshal(fields.Instance, &next.Instance); err != nil {
				return nil, err
			}
		}
		return next, nil
	case UpdateClosed:
		var next WireClosedServiceProviderUpdate
		return next, json.Unmarshal(fields.Instance, &next.Instance)
	case UpdateReset:
		var next WireResetServiceProviderUpdate
		return next, json.Unmarshal(fields.Snapshot, &next.Snapshot)
	case UpdateReplaced:
		var next WireReplacedServiceProviderUpdate
		return next, json.Unmarshal(fields.Snapshot, &next.Snapshot)
	case UpdateSpawned:
		var next WireSpawnedServiceProviderUpdate
		return next, json.Unmarshal(fields.Instance, &next.Instance)
	case UpdateUnavailable:
		return WireUnavailableServiceProviderUpdate{}, nil
	}
	return nil, errors.New("Invalid service provider update")
}
