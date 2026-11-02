package sdk

import "slices"

// Refinement is one entry of a schema's hidden `~refine` list (TypeBox's `{check, error}`): Check decides whether a value passes and Error words the failure. TypeBox's
// own validation runs them, but a tool's arguments are validated as plain JSON Schema (Pi 1.4.0+b556cfe5 validates with pi-schema: ai/CHANGELOG.md:25, coding-agent/docs/extensions.md:176), so nothing in a tool's registration or call consults the
// list: arguments a refinement would reject are accepted, and the constraint belongs in the tool's handler.
type Refinement struct {
	// Check reports whether value passes.
	Check func(value any) (bool, error)
	// Error is the message of a failure; nil is TypeBox's "Refine Error".
	Error func(value any) (string, error)
}

// Refine is `Type.Refine(type, check, error?)`: a copy of the schema whose hidden `~refine` list has the refinement appended. The schema's JSON does not change. A schema made of an
// array, or anything that is not an object, panics as Unsafe does.
// pig divergence (D226): the stored check is never run by any API; remove when every SDK serves TypeBox Value.Check.
func (t typeNamespace) Refine(schema any, check func(value any) (bool, error), errorFn ...func(value any) (string, error)) *TSchema {
	refinement := Refinement{Check: check}
	if len(errorFn) > 0 {
		refinement.Error = errorFn[0]
	}
	var existing []Refinement
	if current, ok := schema.(*TSchema); ok && current != nil {
		if list, isList := current.hidden["~refine"].([]Refinement); isList {
			existing = list
		}
	}
	return update(schema, map[string]any{"~refine": append(slices.Clone(existing), refinement)}, nil)
}

// IsRefine is TypeBox's IsRefine: the value carries a `~refine` list of refinements.
func (t typeNamespace) IsRefine(value any) bool {
	schema, ok := value.(*TSchema)
	if !ok || schema == nil {
		return false
	}
	list, isList := schema.hidden["~refine"].([]Refinement)
	return isList && len(list) > 0
}
