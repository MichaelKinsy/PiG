package subprocess

import (
	"encoding/json"
	"maps"

	"github.com/MichaelKinsy/PiG/ai"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// wireEventPayload projects the Go-native models an event carries onto Pi's extension-facing Model shape. Upstream hands extensions the Session's Model objects (agent-session.ts:2457-2469 _emitModelSelect).
func wireEventPayload(event any) any {
	selected, ok := event.(extension.ModelSelectEvent)
	if !ok {
		return event
	}
	return modelSelectWire{Type: selected.Type, Model: wireModel(selected.Model), PreviousModel: wireModel(selected.PreviousModel), Source: selected.Source}
}

// modelSelectWire is extension.ModelSelectEvent with its models in the wire shape; the members keep the event's order.
type modelSelectWire struct {
	Type          string                      `json:"type"`
	Model         any                         `json:"model"`
	PreviousModel any                         `json:"previousModel,omitempty"`
	Source        extension.ModelSelectSource `json:"source"`
}

// wireModel returns an untyped nil for an absent model so the omitted previousModel stays undefined.
func wireModel(model *ai.Model) any {
	if model == nil {
		return nil
	}
	return extension.ModelInfo(model)
}

// applyToolCallInput replaces the members of a tool_call event's input with the input an extension's handler left, in the order it left them (JSON.stringify of the edited object). The event's Input map and the wire bytes it points at are what the agent loop reads after the handlers ran (coding/session_extension_hooks.go), so both change in place and the next handler and the tool see the edit. A value that is not an object cannot be an input a tool accepts, and the event keeps its own.
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
	canonical, err := jsonstringify.Canonicalize(input)
	if err != nil {
		return err
	}
	clear(custom.Input)
	maps.Copy(custom.Input, members)
	if custom.WireInput != nil {
		*custom.WireInput = canonical
	}
	return nil
}
