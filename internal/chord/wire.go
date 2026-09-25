package chord

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

// Upstream services/wire.ts control-call vocabulary.
const (
	serviceControlId         = "$chord.service"
	serviceCatalogueMember   = "catalogue"
	serviceSubscribeMember   = "subscribe"
	serviceUnsubscribeMember = "unsubscribe"
	controlCatalogue         = "catalogue"
	controlSubscribe         = "subscribe"
	controlUnsubscribe       = "unsubscribe"
)

// ServiceControlCall is a decoded $chord.service control call.
type ServiceControlCall struct {
	Type           string
	SubscriptionId string
	ServiceId      string
	Mode           ServiceMode
}

// CreateServiceCatalogueCall builds the catalogue control call.
func CreateServiceCatalogueCall() ServiceCall {
	return ServiceCall{ServiceId: serviceControlId, Member: serviceCatalogueMember, Args: []json.RawMessage{}}
}

// CreateServiceSubscribeCall builds the subscribe control call.
func CreateServiceSubscribeCall(subscriptionId, serviceId string, mode ServiceMode) ServiceCall {
	return ServiceCall{ServiceId: serviceControlId, Member: serviceSubscribeMember, Args: []json.RawMessage{
		mustRaw(subscriptionId), mustRaw(serviceId), mustRaw(string(mode)),
	}}
}

// CreateServiceUnsubscribeCall builds the unsubscribe control call.
func CreateServiceUnsubscribeCall(subscriptionId string) ServiceCall {
	return ServiceCall{ServiceId: serviceControlId, Member: serviceUnsubscribeMember, Args: []json.RawMessage{mustRaw(subscriptionId)}}
}

// DecodeServiceControlCall returns the control call, or false for an ordinary
// service call (including malformed control calls, as upstream).
func DecodeServiceControlCall(call ServiceCall) (ServiceControlCall, bool) {
	if call.ServiceId != serviceControlId || call.Instance != nil {
		return ServiceControlCall{}, false
	}
	strings := make([]string, len(call.Args))
	for index, arg := range call.Args {
		if json.Unmarshal(arg, &strings[index]) != nil || strings[index] == "" {
			return ServiceControlCall{}, false
		}
	}
	switch {
	case call.Member == serviceCatalogueMember && len(call.Args) == 0:
		return ServiceControlCall{Type: controlCatalogue}, true
	case call.Member == serviceSubscribeMember && len(call.Args) == 3 &&
		(strings[2] == string(ServiceSingleton) || strings[2] == string(ServiceKeyed)):
		return ServiceControlCall{Type: controlSubscribe, SubscriptionId: strings[0], ServiceId: strings[1], Mode: ServiceMode(strings[2])}, true
	case call.Member == serviceUnsubscribeMember && len(call.Args) == 1:
		return ServiceControlCall{Type: controlUnsubscribe, SubscriptionId: strings[0]}, true
	}
	return ServiceControlCall{}, false
}

func mustRaw(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("chord: %v", err))
	}
	return encoded
}

type wireMember struct {
	Name     string     `json:"name"`
	Kind     string     `json:"kind"`
	Sequence *int       `json:"sequence,omitempty"`
	Ops      []pico3.Op `json:"ops,omitempty"`
}

// MarshalJSON emits the upstream discriminated member shape.
func (member ServiceMemberSnapshot) MarshalJSON() ([]byte, error) {
	wire := wireMember{Name: member.Name, Kind: member.Kind}
	if member.Kind == MemberState {
		sequence := member.Sequence
		wire.Sequence = &sequence
		wire.Ops = member.Ops
		if wire.Ops == nil {
			wire.Ops = []pico3.Op{}
		}
	}
	return json.Marshal(wire)
}

// UnmarshalJSON validates the upstream discriminated member shape.
func (member *ServiceMemberSnapshot) UnmarshalJSON(data []byte) error {
	var wire wireMember
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	switch wire.Kind {
	case MemberMethod:
		if wire.Sequence != nil || wire.Ops != nil {
			return errors.New("invalid service member snapshot")
		}
		*member = ServiceMemberSnapshot{Name: wire.Name, Kind: MemberMethod}
	case MemberState:
		if wire.Sequence == nil || *wire.Sequence < 0 {
			return errors.New("invalid service member snapshot")
		}
		*member = ServiceMemberSnapshot{Name: wire.Name, Kind: MemberState, Sequence: *wire.Sequence, Ops: wire.Ops}
	default:
		return errors.New("invalid service member snapshot")
	}
	return nil
}

type wireUpdate struct {
	Type     string          `json:"type"`
	Instance json.RawMessage `json:"instance,omitempty"`
	Member   string          `json:"member,omitempty"`
	Sequence *int            `json:"sequence,omitempty"`
	Ops      []pico3.Op      `json:"ops,omitempty"`
	Snapshot json.RawMessage `json:"snapshot,omitempty"`
}

// MarshalJSON emits the upstream ServiceProviderUpdate union.
func (update ServiceProviderUpdate) MarshalJSON() ([]byte, error) {
	wire := wireUpdate{Type: update.Type}
	var err error
	switch update.Type {
	case UpdateState:
		if update.Address != nil {
			wire.Instance = mustRaw(update.Address)
		}
		sequence := update.Sequence
		wire.Member, wire.Sequence, wire.Ops = update.Member, &sequence, update.Ops
		if wire.Ops == nil {
			wire.Ops = []pico3.Op{}
		}
	case UpdateUnavailable:
	case UpdateReplaced:
		wire.Snapshot, err = json.Marshal(update.Snapshot)
	case UpdateSpawned:
		wire.Instance, err = json.Marshal(update.Snapshot)
	case UpdateClosed:
		wire.Instance, err = json.Marshal(update.Address)
	default:
		return nil, fmt.Errorf("invalid service provider update type %q", update.Type)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(wire)
}

// UnmarshalJSON decodes the upstream ServiceProviderUpdate union.
func (update *ServiceProviderUpdate) UnmarshalJSON(data []byte) error {
	var wire wireUpdate
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	result := ServiceProviderUpdate{Type: wire.Type}
	switch wire.Type {
	case UpdateState:
		if wire.Member == "" || wire.Sequence == nil {
			return errors.New("invalid service provider update")
		}
		if len(wire.Instance) > 0 {
			result.Address = new(ServiceInstanceAddress)
			if err := json.Unmarshal(wire.Instance, result.Address); err != nil {
				return err
			}
		}
		result.Member, result.Sequence, result.Ops = wire.Member, *wire.Sequence, wire.Ops
	case UpdateUnavailable:
	case UpdateReplaced:
		result.Snapshot = new(ServiceInstanceSnapshot)
		if err := json.Unmarshal(wire.Snapshot, result.Snapshot); err != nil {
			return err
		}
	case UpdateSpawned:
		result.Snapshot = new(ServiceInstanceSnapshot)
		if err := json.Unmarshal(wire.Instance, result.Snapshot); err != nil {
			return err
		}
	case UpdateClosed:
		result.Address = new(ServiceInstanceAddress)
		if err := json.Unmarshal(wire.Instance, result.Address); err != nil {
			return err
		}
	default:
		return errors.New("invalid service provider update")
	}
	*update = result
	return nil
}
