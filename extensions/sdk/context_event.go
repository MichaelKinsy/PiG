package sdk

import (
	"encoding/json"
	"reflect"
	"slices"
)

// Context transforms carry message-list identity across JSON without exposing
// transport metadata to handlers. Go handlers return a replacement slice when
// changing its length; mutations to message objects also survive a nil result.
func snapshotContextMessages(event string, data map[string]any) []any {
	if event != "context" && event != "context_with_system" {
		return nil
	}
	messages, ok := data["messages"].([]any)
	if !ok {
		return nil
	}
	return append([]any{}, messages...)
}

func contextEventResult(data map[string]any, snapshot []any, result any) any {
	messages, _ := data["messages"].([]any)
	if returned, ok := result.(map[string]any); ok {
		if replacement, ok := returned["messages"].([]any); ok {
			messages = replacement
		}
	} else if result != nil {
		// Typed user results remain valid event results; their value semantics do
		// not retain the incoming message objects' identity.
		raw, err := json.Marshal(result)
		if err != nil {
			return result
		}
		var returned struct {
			Messages []any `json:"messages"`
		}
		if json.Unmarshal(raw, &returned) != nil {
			return result
		}
		if returned.Messages != nil {
			return map[string]any{"messages": returned.Messages, "_pigContextUnchanged": false}
		}
	}
	unchanged := slices.EqualFunc(messages, snapshot, func(a, b any) bool {
		av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
		if !av.IsValid() || !bv.IsValid() {
			return av.IsValid() == bv.IsValid()
		}
		if av.Type() != bv.Type() {
			return false
		}
		if av.Kind() == reflect.Map {
			return av.Pointer() == bv.Pointer()
		}
		return reflect.DeepEqual(a, b)
	})
	return map[string]any{"messages": messages, "_pigContextUnchanged": unchanged}
}
