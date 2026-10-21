package telemetry

// Ports packages/telemetry/src/index.ts.
//
// JSON of the attribute definitions. Upstream's `values`, `elementValues` and `examples` are one member each whose element
// type follows `type`; the Go struct holds one field per element type, so these methods write the field that applies to
// Type under upstream's member name and read it back into that field.

import (
	"encoding/json"
	"fmt"
)

// attributeDefinitionWire is the JSON object of a TelemetryAttributeDefinition. A nil member is absent; a non-nil empty
// slice is [].
type attributeDefinitionWire struct {
	TelemetryAttributeMetadata
	Type          TelemetryAttributeType `json:"type"`
	Values        any                    `json:"values,omitzero"`
	ElementValues any                    `json:"elementValues,omitzero"`
	Examples      any                    `json:"examples,omitzero"`
}

// present returns slice as a JSON member value, or nil when the slice is absent.
func present[T any](slice []T) any {
	if slice == nil {
		return nil
	}
	return slice
}

func (definition TelemetryAttributeDefinition) wire() attributeDefinitionWire {
	wire := attributeDefinitionWire{TelemetryAttributeMetadata: definition.TelemetryAttributeMetadata, Type: definition.Type}
	switch definition.Type {
	case TelemetryAttributeTypeString:
		wire.Values, wire.Examples = present(definition.Values), present(definition.Examples)
	case TelemetryAttributeTypeNumber:
		wire.Values, wire.Examples = present(definition.NumberValues), present(definition.NumberExamples)
	case TelemetryAttributeTypeBoolean:
		wire.Values, wire.Examples = present(definition.BooleanValues), present(definition.BooleanExamples)
	case TelemetryAttributeTypeStringArray:
		wire.ElementValues, wire.Examples = present(definition.ElementValues), present(definition.ArrayExamples)
	case TelemetryAttributeTypeNumberArray:
		wire.ElementValues, wire.Examples = present(definition.NumberElementValues), present(definition.NumberArrayExamples)
	case TelemetryAttributeTypeBooleanArray:
		wire.ElementValues, wire.Examples = present(definition.BooleanElementValues), present(definition.BooleanArrayExamples)
	}
	return wire
}

// attributeDefinitionRaw is attributeDefinitionWire with members held undecoded until Type selects their element type.
type attributeDefinitionRaw struct {
	TelemetryAttributeMetadata
	Type          TelemetryAttributeType `json:"type"`
	Values        json.RawMessage        `json:"values"`
	ElementValues json.RawMessage        `json:"elementValues"`
	Examples      json.RawMessage        `json:"examples"`
}

// attributeMember is every element type of an attribute definition's value and example members.
type attributeMember interface {
	string | float64 | bool | []string | []float64 | []bool
}

func decodeMember[T attributeMember](raw json.RawMessage, into *[]T) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, into)
}

func (raw attributeDefinitionRaw) definition() (TelemetryAttributeDefinition, error) {
	definition := TelemetryAttributeDefinition{TelemetryAttributeMetadata: raw.TelemetryAttributeMetadata, Type: raw.Type}
	var err error
	switch raw.Type {
	case TelemetryAttributeTypeString:
		err = firstError(decodeMember(raw.Values, &definition.Values), decodeMember(raw.Examples, &definition.Examples))
	case TelemetryAttributeTypeNumber:
		err = firstError(decodeMember(raw.Values, &definition.NumberValues), decodeMember(raw.Examples, &definition.NumberExamples))
	case TelemetryAttributeTypeBoolean:
		err = firstError(decodeMember(raw.Values, &definition.BooleanValues), decodeMember(raw.Examples, &definition.BooleanExamples))
	case TelemetryAttributeTypeStringArray:
		err = firstError(decodeMember(raw.ElementValues, &definition.ElementValues), decodeMember(raw.Examples, &definition.ArrayExamples))
	case TelemetryAttributeTypeNumberArray:
		err = firstError(decodeMember(raw.ElementValues, &definition.NumberElementValues), decodeMember(raw.Examples, &definition.NumberArrayExamples))
	case TelemetryAttributeTypeBooleanArray:
		err = firstError(decodeMember(raw.ElementValues, &definition.BooleanElementValues), decodeMember(raw.Examples, &definition.BooleanArrayExamples))
	}
	if err != nil {
		return TelemetryAttributeDefinition{}, fmt.Errorf("telemetry: attribute definition of type %q: %w", raw.Type, err)
	}
	return definition, nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// MarshalJSON writes the definition as upstream's attribute object.
func (definition TelemetryAttributeDefinition) MarshalJSON() ([]byte, error) {
	return json.Marshal(definition.wire()) //perf:ok the schema definition is written when a catalogue is described, not per span or per turn
}

// UnmarshalJSON reads upstream's attribute object, decoding the value members as the element type of its type.
func (definition *TelemetryAttributeDefinition) UnmarshalJSON(data []byte) error {
	var raw attributeDefinitionRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	decoded, err := raw.definition()
	if err != nil {
		return err
	}
	*definition = decoded
	return nil
}

// startAttributeDefinitionWire is the JSON object of a TelemetryStartAttributeDefinition: the attribute object and required.
type startAttributeDefinitionWire struct {
	attributeDefinitionWire
	Required bool `json:"required"`
}

type startAttributeDefinitionRaw struct {
	attributeDefinitionRaw
	Required bool `json:"required"`
}

// MarshalJSON writes the definition as upstream's start-attribute object.
func (definition TelemetryStartAttributeDefinition) MarshalJSON() ([]byte, error) {
	return json.Marshal(startAttributeDefinitionWire{attributeDefinitionWire: definition.wire(), Required: definition.Required}) //perf:ok the schema definition is written when a catalogue is described, not per span or per turn
}

// UnmarshalJSON reads upstream's start-attribute object.
func (definition *TelemetryStartAttributeDefinition) UnmarshalJSON(data []byte) error {
	var raw startAttributeDefinitionRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	decoded, err := raw.definition()
	if err != nil {
		return err
	}
	*definition = TelemetryStartAttributeDefinition{TelemetryAttributeDefinition: decoded, Required: raw.Required}
	return nil
}
