package subprocess

import (
	"encoding/json"
	"fmt"
	"maps"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// wireEventPayload projects the Go-native models an event carries onto Pi's extension-facing Model shape. Upstream hands extensions the Session's Model objects (agent-session.ts:2457-2469 _emitModelSelect). A before_provider_request payload that a provider holds as a map (Google, Vertex, pi-messages) is written in the member order Pi's object has, which a Go map cannot give.
func wireEventPayload(event any) any {
	if request, ok := event.(extension.BeforeProviderRequestEvent); ok {
		if encoded, held, err := ai.MarshalHookPayload(request.Payload); err == nil && held {
			request.Payload = json.RawMessage(encoded)
		}
		return request
	}
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
	return extension.ModelValue{Model: model}
}

// applyToolCallInput replaces the members of a tool_call event's input with the input an extension's handler left, in the order it left them (JSON.stringify of the edited object). The event's Input map and the wire bytes it points at are what the agent loop reads after the handlers ran (coding/session_extension_hooks.go), so both change in place and the next handler and the tool see the edit. A value that is not an object cannot be an input a tool accepts, and the event keeps its own.
func applyToolCallInput(event any, input json.RawMessage) error {
	custom, ok := event.(extension.CustomToolCallEvent)
	if !ok || custom.Input == nil {
		return nil
	}
	var edited any
	if err := jsstring.UnmarshalKeepingSurrogates(input, &edited); err != nil {
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

// eventMessage is the message at position at of the messages event holds: message_end holds one. The message shares its object with the event, so a replacement written into it is what the event's next handler and the agent's transcript read.
func eventMessage(event any, at int) (agent.AgentMessage, bool) {
	switch event := event.(type) {
	case extension.MessageEndEvent:
		return event.Message, at == 0
	}
	return agent.AgentMessage{}, false
}

// applyEventEdits writes the messages the handlers of an event left into the messages the event carries, as Pi's handlers edit the objects the transcript, the later handlers and the stored entries share (runner.ts emit, agent-session.ts appendMessage). Every edit is checked before any is applied: its position must be one the event holds, once, and its message must be of the role of the message it replaces.
func applyEventEdits(event any, edits []EventEdit) error {
	if len(edits) == 0 {
		return nil
	}
	targets := make([]agent.AgentMessage, len(edits))
	replacements := make([]agent.AgentMessage, len(edits))
	seen := make(map[int]bool, len(edits))
	for i, edit := range edits {
		target, ok := eventMessage(event, edit.At)
		if !ok {
			return fmt.Errorf("edit at %d: the event holds no message there", edit.At)
		}
		if seen[edit.At] {
			return fmt.Errorf("edit at %d: named twice", edit.At)
		}
		seen[edit.At] = true
		if err := json.Unmarshal(edit.Message, &replacements[i]); err != nil {
			return fmt.Errorf("edit at %d: %w", edit.At, err)
		}
		if role := replacements[i].Role(); role != target.Role() {
			return fmt.Errorf("edit at %d: the message has role %q, the event's has %q", edit.At, role, target.Role())
		}
		targets[i] = target
	}
	for i, target := range targets {
		agent.ReplaceMessageInPlace(target, replacements[i])
	}
	return nil
}
