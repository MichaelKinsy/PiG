package harness

import "github.com/MichaelKinsy/PiG/ai"

// FieldPatch is one optional patch field. Present=false leaves the field
// unchanged; Present with a nil Value deletes it (upstream `key: undefined`);
// Present with a Value sets it.
type FieldPatch[T any] struct {
	Present bool
	Value   *T
}

// SetField returns a patch that sets a field.
func SetField[T any](value T) FieldPatch[T] { return FieldPatch[T]{Present: true, Value: &value} }

// DeleteField returns a patch that deletes a field.
func DeleteField[T any]() FieldPatch[T] { return FieldPatch[T]{Present: true} }

// MapPatch patches a string-keyed map. Present=false leaves the map unchanged.
// Present with nil Entries clears the whole map (upstream `headers: undefined`).
// Present with non-nil Entries applies each entry: a nil value deletes the key,
// a non-nil value sets it. An empty non-nil Entries map is the upstream `{}`
// patch, which creates an empty map when none existed.
type MapPatch[V any] struct {
	Present bool
	Entries map[string]*V
}

// AgentHarnessStreamOptionsPatch is a partial stream-options update returned
// by before_request hooks (upstream AgentHarnessStreamOptionsPatch).
type AgentHarnessStreamOptionsPatch struct {
	Transport       FieldPatch[ai.Transport]
	TimeoutMs       FieldPatch[int]
	MaxRetries      FieldPatch[int]
	MaxRetryDelayMs FieldPatch[int]
	CacheRetention  FieldPatch[string]
	Deferred        FieldPatch[AgentHarnessDeferredOption]
	Headers         MapPatch[string]
	Metadata        MapPatch[any]
}
