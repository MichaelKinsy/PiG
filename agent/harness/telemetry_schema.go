package harness

import "encoding/json"

// TelemetryDefinition is one named member of an ordered schema object.
type TelemetryDefinition[T any] struct {
	Name       string
	Definition T
}

// TelemetryDefinitions preserves the insertion order of a schema's named members. It serializes as a JSON object, not an array.
type TelemetryDefinitions[T any] []TelemetryDefinition[T]

// Get looks up a named schema member.
func (definitions TelemetryDefinitions[T]) Get(name string) (T, bool) {
	for _, entry := range definitions {
		if entry.Name == name {
			return entry.Definition, true
		}
	}
	var zero T
	return zero, false
}

// MarshalJSON preserves schema member order when serializing the object.
func (definitions TelemetryDefinitions[T]) MarshalJSON() ([]byte, error) {
	result := []byte{'{'}
	for i, entry := range definitions {
		if i != 0 {
			result = append(result, ',')
		}
		name, err := json.Marshal(entry.Name)
		if err != nil {
			return nil, err
		}
		definition, err := json.Marshal(entry.Definition)
		if err != nil {
			return nil, err
		}
		result = append(result, name...)
		result = append(result, ':')
		result = append(result, definition...)
	}
	return append(result, '}'), nil
}

// TelemetryAttributeType identifies the scalar or homogeneous array value of an attribute.
type TelemetryAttributeType string

// TelemetryAttributeMetadata describes an attribute's meaning and handling.
type TelemetryAttributeMetadata struct {
	Description string `json:"description"`
	Sensitive   *bool  `json:"sensitive,omitempty"`
	Cardinality string `json:"cardinality,omitempty"`
}

// TelemetryAttributeDefinition describes a value, its allowed literals, and examples. Values applies to scalar types; ElementValues applies to array types.
type TelemetryAttributeDefinition struct {
	TelemetryAttributeMetadata
	Type          TelemetryAttributeType `json:"type"`
	Values        []AttributeValue       `json:"values,omitzero"`
	ElementValues []AttributeValue       `json:"elementValues,omitzero"`
	Examples      []AttributeValue       `json:"examples,omitzero"`
}

// TelemetryStartAttributeDefinition records whether a start attribute is required.
type TelemetryStartAttributeDefinition struct {
	TelemetryAttributeDefinition
	Required bool `json:"required"`
}

// TelemetryEventAttributeDefinition records whether an event attribute is required.
type TelemetryEventAttributeDefinition = TelemetryStartAttributeDefinition

// TelemetryEventDefinition describes a named span event.
type TelemetryEventDefinition struct {
	Description string                                                  `json:"description"`
	Attributes  TelemetryDefinitions[TelemetryEventAttributeDefinition] `json:"attributes"`
}

// TelemetryParentDefinition describes permitted parents: any, root_or_external, or the named spans.
type TelemetryParentDefinition struct {
	Kind  string   `json:"kind"`
	Spans []string `json:"spans,omitzero"`
}

// TelemetrySpanDefinition describes a span's parents, start attributes, completion enrichment, events, and status policy.
type TelemetrySpanDefinition struct {
	Description     string                                                  `json:"description"`
	Parents         TelemetryParentDefinition                               `json:"parents"`
	StartAttributes TelemetryDefinitions[TelemetryStartAttributeDefinition] `json:"startAttributes"`
	EndAttributes   TelemetryDefinitions[TelemetryAttributeDefinition]      `json:"endAttributes"`
	Events          TelemetryDefinitions[TelemetryEventDefinition]          `json:"events,omitzero"`
	Status          struct {
		Default   SpanStatusCode `json:"default"`
		ErrorWhen string         `json:"errorWhen"`
	} `json:"status"`
}

// TelemetrySchemaDefinition is the upstream-owned, serializable span vocabulary. Version is the upstream telemetry schema version, not a Pig format discriminator.
type TelemetrySchemaDefinition struct {
	Version int                                           `json:"version"`
	Spans   TelemetryDefinitions[TelemetrySpanDefinition] `json:"spans"`
}
