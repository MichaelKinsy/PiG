package subprocess

import (
	"encoding/json"
	"maps"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// wireEventPayload projects the Go-native models an event carries onto Pi's extension-facing Model shape. Upstream hands extensions the Session's Model objects (agent-session.ts:2372-2384 _emitModelSelect).
func wireEventPayload(event any) any {
	selected, ok := event.(extension.ModelSelectEvent)
	if !ok {
		return event
	}
	selected.Model = wireModel(selected.Model)
	selected.PreviousModel = wireModel(selected.PreviousModel)
	return selected
}

// wireModel returns an untyped nil for an absent model so the omitted previousModel stays undefined.
func wireModel(model extension.Model) extension.Model {
	native, ok := model.(*ai.Model)
	if !ok {
		return model
	}
	if native == nil {
		return nil
	}
	return extension.ModelInfo(native)
}

// applyToolCallInput replaces the members of a tool_call event's input with the input an extension's handler left. The event's Input map is the object the agent loop reads after the handlers ran (coding/session_extension_hooks.go), so it changes in place. A value that is not an object cannot be an input a tool accepts, and the event keeps its own.
func applyToolCallInput(event any, input json.RawMessage) error {
	custom, ok := event.(extension.CustomToolCallEvent)
	if !ok || custom.Input == nil {
		return nil
	}
	var edited any
	if err := json.Unmarshal(input, &edited); err != nil {
		return err
	}
	members, ok := edited.(map[string]any)
	if !ok {
		return nil
	}
	clear(custom.Input)
	maps.Copy(custom.Input, members)
	return nil
}
