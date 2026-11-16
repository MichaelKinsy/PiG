package subprocess

import (
	"bytes"
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

// eventMessage is the message at position at of the messages event holds: message_end holds one, turn_end its assistant message and then its tool results. The message shares its object with the event, so a replacement written into it is what the event's next handler and the agent's transcript read.
func eventMessage(event any, at int) (agent.AgentMessage, bool) {
	switch event := event.(type) {
	case extension.MessageEndEvent:
		return event.Message, at == 0
	case extension.TurnEndEvent:
		switch {
		case at == 0:
			return event.Message, true
		case at > 0 && at <= len(event.ToolResults):
			return agent.AgentMessage{ToolResult: &event.ToolResults[at-1]}, true
		}
	}
	return agent.AgentMessage{}, false
}

// applyEventEdits writes the messages the handlers of an event left into the messages the event carries, as Pi's handlers edit the objects the transcript, the later handlers and the stored entries share (runner.ts emit, agent-session.ts appendMessage). Every edit is checked before any is applied: its position must be one the event holds, once, and its message must be of the role of the message it replaces.
func applyEventEdits(event any, edits []EventEdit) error {
	if len(edits) == 0 {
		return nil
	}
	type resolved struct{ target, replacement agent.AgentMessage }
	var small [4]resolved
	checked := small[:0]
	for i, edit := range edits {
		if edit.At == nil {
			return fmt.Errorf("edit %d: no position (at)", i)
		}
		at := *edit.At
		if text := bytes.TrimSpace(edit.Message); len(text) == 0 || text[0] != '{' {
			return fmt.Errorf("edit at %d: no message object", at)
		}
		target, ok := eventMessage(event, at)
		if !ok {
			return fmt.Errorf("edit at %d: the event holds no message there", at)
		}
		for _, earlier := range edits[:i] {
			if earlier.At != nil && *earlier.At == at {
				return fmt.Errorf("edit at %d: named twice", at)
			}
		}
		var replacement agent.AgentMessage
		if err := json.Unmarshal(edit.Message, &replacement); err != nil {
			return fmt.Errorf("edit at %d: %w", at, err)
		}
		if role := replacement.Role(); role != target.Role() {
			return fmt.Errorf("edit at %d: the message has role %q, the event's has %q", at, role, target.Role())
		}
		checked = append(checked, resolved{target, replacement})
	}
	for _, r := range checked {
		agent.ReplaceMessageInPlace(r.target, r.replacement)
	}
	return nil
}

// decodeBoundaryEditsStrictly refuses a turn_end reply that carries edits and a member this version does not know, in the reply or in an edit, as every other event's reply with edits is refused. A reply that edits nothing is read as before, ignoring members it does not name.
func decodeBoundaryEditsStrictly(raw json.RawMessage) error {
	var reply struct {
		Entries json.RawMessage `json:"_pigBoundaryEntries"`
		Result  json.RawMessage `json:"_pigBoundaryResult"`
		Edits   []EventEdit     `json:"_pigEventEdits"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(&reply)
}
