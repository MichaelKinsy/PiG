package chord

// Ports packages/chord/src/services/wire.ts

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

type opAssertion func(any) error

func serviceRecord(value any, description string) (*chordjson.Object, error) {
	record, ok := value.(*chordjson.Object)
	if !ok || record == nil {
		return nil, fmt.Errorf("Invalid %s", description)
	}
	return record, nil
}
func serviceKeys(value *chordjson.Object, required, optional []string, description string) error {
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		allowed[key] = true
		if !value.Has(key) {
			return fmt.Errorf("Invalid %s", description)
		}
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range value.All() {
		if !allowed[key] {
			return fmt.Errorf("Invalid %s", description)
		}
	}
	return nil
}
func isServiceIDString(value any) bool { s, ok := value.(string); return ok && s != "" }
func serviceMode(value any) bool {
	return value == string(ServiceSingleton) || value == string(ServiceKeyed)
}
func assertServiceAddress(value any) error {
	address, err := serviceRecord(value, "service instance address")
	if err != nil {
		return err
	}
	if err := serviceKeys(address, []string{"key", "generation"}, nil, "service instance address"); err != nil {
		return err
	}
	if !isServiceIDString(address.Value("key")) || !deltaInteger(address.Value("generation"), 1) {
		return fmt.Errorf("Invalid service instance address")
	}
	return nil
}
func assertServiceInstance(value any, assertOp opAssertion) error {
	instance, err := serviceRecord(value, "service instance snapshot")
	if err != nil {
		return err
	}
	if err := serviceKeys(instance, []string{"members"}, []string{"instance"}, "service instance snapshot"); err != nil {
		return err
	}
	if address, present := instance.Get("instance"); present {
		if err := assertServiceAddress(address); err != nil {
			return err
		}
	}
	members, ok := instance.Value("members").([]any)
	if !ok {
		return fmt.Errorf("Invalid service instance snapshot")
	}
	for _, candidate := range members {
		member, err := serviceRecord(candidate, "service member snapshot")
		if err != nil {
			return err
		}
		switch member.Value("kind") {
		case string(MemberMethod):
			if err := serviceKeys(member, []string{"name", "kind"}, nil, "service method snapshot"); err != nil {
				return err
			}
			if !isServiceIDString(member.Value("name")) {
				return fmt.Errorf("Invalid service method snapshot")
			}
		case string(MemberState):
			if err := serviceKeys(member, []string{"name", "kind", "sequence", "ops"}, nil, "service state snapshot"); err != nil {
				return err
			}
			ops, ok := member.Value("ops").([]any)
			if !isServiceIDString(member.Value("name")) || !deltaInteger(member.Value("sequence"), 0) || !ok {
				return fmt.Errorf("Invalid service state snapshot")
			}
			for _, op := range ops {
				if err := assertOp(op); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("Invalid service member snapshot")
		}
	}
	return nil
}
func assertServiceSubscription(value any, assertOp opAssertion) error {
	snapshot, err := serviceRecord(value, "service subscription snapshot")
	if err != nil {
		return err
	}
	if err := serviceKeys(snapshot, []string{"serviceId", "mode", "instances"}, nil, "service subscription snapshot"); err != nil {
		return err
	}
	instances, ok := snapshot.Value("instances").([]any)
	if !isServiceIDString(snapshot.Value("serviceId")) || !serviceMode(snapshot.Value("mode")) || !ok {
		return fmt.Errorf("Invalid service subscription snapshot")
	}
	for _, instance := range instances {
		if err := assertServiceInstance(instance, assertOp); err != nil {
			return err
		}
	}
	return nil
}
func assertServiceUpdate(value any, assertOp opAssertion) error {
	update, err := serviceRecord(value, "service provider update")
	if err != nil {
		return err
	}
	kind, _ := update.Value("type").(string)
	switch ServiceProviderUpdateType(kind) {
	case UpdateState:
		if err := serviceKeys(update, []string{"type", "member", "sequence", "ops"}, []string{"instance"}, "state update"); err != nil {
			return err
		}
		ops, ok := update.Value("ops").([]any)
		if !isServiceIDString(update.Value("member")) || !deltaInteger(update.Value("sequence"), 1) || !ok {
			return fmt.Errorf("Invalid service state update")
		}
		if address, present := update.Get("instance"); present {
			if err := assertServiceAddress(address); err != nil {
				return err
			}
		}
		for _, op := range ops {
			if err := assertOp(op); err != nil {
				return err
			}
		}
	case UpdateReset:
		if err := serviceKeys(update, []string{"type", "snapshot"}, nil, "reset update"); err != nil {
			return err
		}
		if err := assertServiceSubscription(update.Value("snapshot"), assertOp); err != nil {
			return err
		}
		for _, instance := range update.Value("snapshot").(*chordjson.Object).Value("instances").([]any) {
			for _, member := range instance.(*chordjson.Object).Value("members").([]any) {
				fields := member.(*chordjson.Object)
				if fields.Value("kind") != string(MemberState) {
					continue
				}
				ops := fields.Value("ops").([]any)
				if len(ops) != 1 || !isRootReplacement(ops[0]) {
					return fmt.Errorf("Service reset must contain full root replacements")
				}
			}
		}
		return nil
	case UpdateUnavailable:
		return serviceKeys(update, []string{"type"}, nil, "unavailable update")
	case UpdateReplaced:
		if err := serviceKeys(update, []string{"type", "snapshot"}, nil, "replacement update"); err != nil {
			return err
		}
		return assertServiceInstance(update.Value("snapshot"), assertOp)
	case UpdateSpawned:
		if err := serviceKeys(update, []string{"type", "instance"}, nil, "spawn update"); err != nil {
			return err
		}
		return assertServiceInstance(update.Value("instance"), assertOp)
	case UpdateClosed:
		if err := serviceKeys(update, []string{"type", "instance"}, nil, "close update"); err != nil {
			return err
		}
		return assertServiceAddress(update.Value("instance"))
	default:
		return fmt.Errorf("Invalid service provider update")
	}
	return nil
}
func parseServiceValue[T any](raw json.RawMessage, validate func(any) error) (T, error) {
	var result T
	// The validator walks the text's own values, objects in document order, as Pi's validators walk JSON.parse's result.
	value, err := chordjson.Decode(raw)
	if err != nil {
		return result, err
	}
	if err := validate(value); err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

// ParseServiceCall validates the closed invocation shape and optional keyed instance address. Argument payloads are not recursively inspected.
func ParseServiceCall(raw json.RawMessage) (ServiceCall, error) {
	return parseServiceValue[ServiceCall](raw, func(value any) error {
		call, err := serviceRecord(value, "service call")
		if err != nil {
			return err
		}
		if err := serviceKeys(call, []string{"serviceId", "member", "args"}, []string{"instance"}, "service call"); err != nil {
			return err
		}
		if _, ok := call.Value("args").([]any); !ok || !isServiceIDString(call.Value("serviceId")) || !isServiceIDString(call.Value("member")) {
			return fmt.Errorf("Invalid service call")
		}
		if instance, present := call.Get("instance"); present {
			return assertServiceAddress(instance)
		}
		return nil
	})
}
func ParseServiceCatalogue(raw json.RawMessage) ([]ServiceCatalogueEntry, error) {
	return parseServiceValue[[]ServiceCatalogueEntry](raw, func(value any) error {
		values, ok := value.([]any)
		if !ok {
			return fmt.Errorf("Invalid service catalogue")
		}
		ids := make(map[string]bool)
		for _, candidate := range values {
			entry, err := serviceRecord(candidate, "service catalogue entry")
			if err != nil {
				return err
			}
			if err := serviceKeys(entry, []string{"serviceId", "mode"}, nil, "service catalogue entry"); err != nil {
				return err
			}
			id, _ := entry.Value("serviceId").(string)
			if !isServiceIDString(id) || !serviceMode(entry.Value("mode")) || ids[id] {
				return fmt.Errorf("Invalid service catalogue")
			}
			ids[id] = true
		}
		return nil
	})
}
func ParseServiceSubscriptionSnapshot(raw json.RawMessage) (ServiceSubscriptionSnapshot, error) {
	return parseServiceValue[ServiceSubscriptionSnapshot](raw, func(value any) error { return assertServiceSubscription(value, AssertValidOp) })
}
func ParseWireServiceSubscriptionSnapshot(raw json.RawMessage) (WireServiceSubscriptionSnapshot, error) {
	return parseServiceValue[WireServiceSubscriptionSnapshot](raw, func(value any) error { return assertServiceSubscription(value, AssertValidWireOp) })
}
func ParseServiceProviderUpdate(raw json.RawMessage) (ServiceProviderUpdate, error) {
	return parseServiceValue[ServiceProviderUpdate](raw, func(value any) error { return assertServiceUpdate(value, AssertValidOp) })
}
func ParseWireServiceProviderUpdate(raw json.RawMessage) (WireServiceProviderUpdate, error) {
	// The validator walks the text's own values, objects in document order, as Pi's validators walk JSON.parse's result.
	value, err := chordjson.Decode(raw)
	if err != nil {
		return nil, err
	}
	if err := assertServiceUpdate(value, AssertValidWireOp); err != nil {
		return nil, err
	}
	return UnmarshalWireServiceProviderUpdate(raw)
}

// isRootReplacement reports whether a decoded or wire operation is an "r" tuple.
func isRootReplacement(op any) bool {
	tuple, ok := deltaTuple(op)
	return ok && len(tuple) > 0 && tuple[0] == "r"
}
