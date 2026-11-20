package subprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

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

// detachToolCallInput returns a copy of a tool_call event whose input is the object an extension's handler assigned to event.input, with its own Input map and wire bytes: nothing a later handler does to it reaches the call's input. A value that is not an object is no input a tool accepts, and the dispatch keeps the event it has (nil).
func detachToolCallInput(event any, input json.RawMessage) (*extension.CustomToolCallEvent, error) {
	custom, ok := event.(extension.CustomToolCallEvent)
	if !ok || custom.Input == nil {
		return nil, nil
	}
	var assigned any
	if err := jsstring.UnmarshalKeepingSurrogates(input, &assigned); err != nil {
		return nil, err
	}
	members, ok := assigned.(map[string]any)
	if !ok {
		return nil, nil
	}
	canonical, err := jsonstringify.Canonicalize(input)
	if err != nil {
		return nil, err
	}
	custom.Input = members
	wire := json.RawMessage(canonical)
	custom.WireInput = &wire
	return &custom, nil
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

// eventMessage is the message at position at of the messages event holds: message_start and message_end hold one, turn_end its assistant message and then its tool results, agent_end the run's messages. The message shares its object with the event, so a replacement written into it is what the event's next handler and the agent's transcript read.
func eventMessage(event any, at int) (agent.AgentMessage, bool) {
	switch event := event.(type) {
	case extension.MessageStartEvent:
		return event.Message, at == 0
	case extension.MessageEndEvent:
		return event.Message, at == 0
	case extension.AgentEndEvent:
		if at >= 0 && at < len(event.Messages) {
			return event.Messages[at], true
		}
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
//
// The entries of the second form (a member of the event itself, see [EventEdit]) are checked with the rest and, when all pass, recorded on the dispatch parent belongs to, which writes them over the payload of every later request of the dispatch (applyEventMembers). They are carried by the events memberEvent names only.
func applyEventEdits(parent context.Context, event any, edits []EventEdit) error {
	if len(edits) == 0 {
		return nil
	}
	type resolved struct {
		target, replacement agent.AgentMessage
		envelopeOnly        bool
	}
	var small [4]resolved
	checked := small[:0]
	var members []extension.EventMember
	for i, edit := range edits {
		if edit.isMemberEntry() {
			if edit.At != nil || len(edit.Message) > 0 {
				return fmt.Errorf("edit %d: an entry names a position and a member", i)
			}
			if !memberEvent(eventTypeOf(event)) {
				return fmt.Errorf("edit %d: the event takes no edit of a member", i)
			}
			if edit.Member == "" {
				return fmt.Errorf("edit %d: no member name", i)
			}
			if edit.Deleted == (len(edit.Value) > 0) {
				return fmt.Errorf("edit %d: member %q needs exactly one of value and deleted", i, edit.Member)
			}
			if !edit.Deleted && !json.Valid(edit.Value) {
				return fmt.Errorf("edit %d: member %q has no valid value", i, edit.Member)
			}
			for _, earlier := range members {
				if earlier.Name == edit.Member {
					return fmt.Errorf("edit %d: member %q named twice", i, edit.Member)
				}
			}
			members = append(members, extension.EventMember{Name: edit.Member, Value: edit.Value, Deleted: edit.Deleted})
			continue
		}
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
		// upstream: agent-loop.ts emits message_start of the assistant message over a copy of the partial ({...partial}): the later handlers of the event read a handler's edit of it, and the saved entry and the requests do not hold it, so the edit goes into the event's envelope and not into the message the envelope was copied from.
		_, started := event.(extension.MessageStartEvent)
		checked = append(checked, resolved{target, replacement, started && target.Role() == "assistant"})
	}
	if len(members) > 0 && !extension.AddEventMembers(parent, members) {
		return fmt.Errorf("edit of member %q: the event belongs to no dispatch", members[0].Name)
	}
	for _, r := range checked {
		agent.ReplaceMessageInPlace(r.target, r.replacement)
		if r.envelopeOnly {
			continue
		}
		// message_start carries an envelope copied from the message the transcript and the saved entry hold (agent.AgentMessage.EventView); Pi's handler edits that one object, so the edit reaches the original too.
		if original, ok := r.target.Original(); ok {
			agent.ReplaceMessageInPlace(original, r.replacement)
		}
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

// eventEditsMember is the name of the reply member that carries the edits.
var eventEditsMember = []byte(`"_pigEventEdits"`)

// eventTypeOf is the type of the events memberEvent names, read from the event value the host handed the handler.
func eventTypeOf(event any) string {
	switch event := event.(type) {
	case extension.MessageStartEvent:
		return event.Type
	case extension.MessageUpdateEvent:
		return event.Type
	case extension.TurnStartEvent:
		return event.Type
	case extension.AgentStartEvent:
		return event.Type
	case extension.AgentEndEvent:
		return event.Type
	}
	return ""
}

// applyEventMembers returns payload, the JSON object of an event, with the members the dispatch's earlier handlers left written over it: a member the payload has keeps its place with the value the handler left, a deleted one is gone, and a member the payload has not is appended in the order the handlers added it, as a JavaScript object holds them. payload is not modified.
func applyEventMembers(payload json.RawMessage, members []extension.EventMember) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("the event payload is not an object")
	}
	out := make([]byte, 0, len(payload)+64)
	out = append(out, '{')
	written := make([]bool, len(members))
	first := true
	put := func(name string, value json.RawMessage) error {
		key, err := json.Marshal(name)
		if err != nil {
			return err
		}
		if !first {
			out = append(out, ',')
		}
		first = false
		out = append(out, key...)
		out = append(out, ':')
		out = append(out, value...)
		return nil
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, _ := token.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		at := slices.IndexFunc(members, func(m extension.EventMember) bool { return m.Name == name })
		switch {
		case at < 0:
			err = put(name, value)
		case members[at].Deleted:
			written[at] = true
		default:
			written[at] = true
			err = put(name, members[at].Value)
		}
		if err != nil {
			return nil, err
		}
	}
	for i, member := range members {
		if !written[i] && !member.Deleted {
			if err := put(member.Name, member.Value); err != nil {
				return nil, err
			}
		}
	}
	return append(out, '}'), nil
}
