package chord

// Ports packages/chord/src/services/wire.ts

import (
	"encoding/json"
	"errors"
)

// WireServiceMemberSnapshot carries compressed WireOps, never decoded operations.
type WireServiceMemberSnapshot struct {
	Name, Kind string
	Sequence   int
	Ops        []WireOp
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
type WireServiceProviderUpdate struct {
	Type     string
	Address  *ServiceInstanceAddress
	Member   string
	Sequence int
	Ops      []WireOp
	Snapshot *WireServiceInstanceSnapshot
}

func (m WireServiceMemberSnapshot) MarshalJSON() ([]byte, error) {
	if m.Kind == MemberMethod {
		return json.Marshal(struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		}{m.Name, m.Kind})
	}
	ops := m.Ops
	if ops == nil {
		ops = []WireOp{}
	}
	return json.Marshal(struct {
		Name     string   `json:"name"`
		Kind     string   `json:"kind"`
		Sequence int      `json:"sequence"`
		Ops      []WireOp `json:"ops"`
	}{m.Name, m.Kind, m.Sequence, ops})
}
func (m *WireServiceMemberSnapshot) UnmarshalJSON(data []byte) error {
	var fields struct {
		Name     string   `json:"name"`
		Kind     string   `json:"kind"`
		Sequence *int     `json:"sequence"`
		Ops      []WireOp `json:"ops"`
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
func (u WireServiceProviderUpdate) MarshalJSON() ([]byte, error) {
	fields := map[string]any{"type": u.Type}
	switch u.Type {
	case UpdateState:
		if u.Address != nil {
			fields["instance"] = u.Address
		}
		fields["member"], fields["sequence"] = u.Member, u.Sequence
		ops := u.Ops
		if ops == nil {
			ops = []WireOp{}
		}
		fields["ops"] = ops
	case UpdateUnavailable:
	case UpdateReplaced:
		fields["snapshot"] = u.Snapshot
	case UpdateSpawned:
		fields["instance"] = u.Snapshot
	case UpdateClosed:
		fields["instance"] = u.Address
	default:
		return nil, errors.New("Invalid service provider update")
	}
	return json.Marshal(fields)
}
func (u *WireServiceProviderUpdate) UnmarshalJSON(data []byte) error {
	var fields struct {
		Type     string          `json:"type"`
		Instance json.RawMessage `json:"instance"`
		Member   string          `json:"member"`
		Sequence int             `json:"sequence"`
		Ops      []WireOp        `json:"ops"`
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	next := WireServiceProviderUpdate{Type: fields.Type, Member: fields.Member, Sequence: fields.Sequence, Ops: fields.Ops}
	switch next.Type {
	case UpdateState, UpdateClosed:
		if len(fields.Instance) != 0 {
			if err := json.Unmarshal(fields.Instance, &next.Address); err != nil {
				return err
			}
		}
	case UpdateReplaced:
		if err := json.Unmarshal(fields.Snapshot, &next.Snapshot); err != nil {
			return err
		}
	case UpdateSpawned:
		if err := json.Unmarshal(fields.Instance, &next.Snapshot); err != nil {
			return err
		}
	case UpdateUnavailable:
	default:
		return errors.New("Invalid service provider update")
	}
	*u = next
	return nil
}
