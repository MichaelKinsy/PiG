package durable

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
)

// JSON codecs for records whose members hold the pi-ai Message union, the "self" entry head, or a present JSON-null
// task result. Field order follows the upstream type declarations.

type entryRecordWire struct {
	Id             EntryId         `json:"id"`
	ConversationId ConversationId  `json:"conversationId"`
	Kind           string          `json:"kind"`
	Model          json.RawMessage `json:"model,omitempty"`
	Data           JsonValue       `json:"data,omitempty"`
	Head           *EntryId        `json:"head,omitempty"`
	Edits          []ContextEdit   `json:"edits,omitempty"`
	ByTaskId       *TaskId         `json:"byTaskId,omitempty"`
}

// MarshalJSON encodes Model when it is not nil, even when empty.
func (entry EntryRecord) MarshalJSON() ([]byte, error) {
	model, err := marshalModel(entry.Model)
	if err != nil {
		return nil, err
	}
	return json.Marshal(entryRecordWire{
		Id: entry.Id, ConversationId: entry.ConversationId, Kind: entry.Kind, Model: model, Data: entry.Data,
		Head: entry.Head, Edits: entry.Edits, ByTaskId: entry.ByTaskId,
	})
}

// UnmarshalJSON decodes Model by message role.
func (entry *EntryRecord) UnmarshalJSON(data []byte) error {
	var wire entryRecordWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	model, err := unmarshalModel(wire.Model)
	if err != nil {
		return fmt.Errorf("entry %d model: %w", wire.Id, err)
	}
	*entry = EntryRecord{
		Id: wire.Id, ConversationId: wire.ConversationId, Kind: wire.Kind, Model: model, Data: wire.Data,
		Head: wire.Head, Edits: wire.Edits, ByTaskId: wire.ByTaskId,
	}
	return nil
}

type entryDraftWire struct {
	Kind  string          `json:"kind"`
	Model json.RawMessage `json:"model,omitempty"`
	Data  JsonValue       `json:"data,omitempty"`
	Head  json.RawMessage `json:"head,omitempty"`
	Edits []ContextEdit   `json:"edits,omitempty"`
}

// MarshalJSON encodes HeadSelf as head "self".
func (draft EntryDraft) MarshalJSON() ([]byte, error) {
	model, err := marshalModel(draft.Model)
	if err != nil {
		return nil, err
	}
	wire := entryDraftWire{Kind: draft.Kind, Model: model, Data: draft.Data, Edits: draft.Edits}
	switch {
	case draft.HeadSelf:
		wire.Head = json.RawMessage(`"self"`)
	case draft.Head != nil:
		wire.Head, err = json.Marshal(*draft.Head)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(wire)
}

// UnmarshalJSON decodes head "self" as HeadSelf.
func (draft *EntryDraft) UnmarshalJSON(data []byte) error {
	var wire entryDraftWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	model, err := unmarshalModel(wire.Model)
	if err != nil {
		return fmt.Errorf("entry draft model: %w", err)
	}
	decoded := EntryDraft{Kind: wire.Kind, Model: model, Data: wire.Data, Edits: wire.Edits}
	if len(wire.Head) > 0 && string(wire.Head) != "null" {
		if string(wire.Head) == `"self"` {
			decoded.HeadSelf = true
		} else {
			var head EntryId
			if err := json.Unmarshal(wire.Head, &head); err != nil {
				return fmt.Errorf("entry draft head: %w", err)
			}
			decoded.Head = &head
		}
	}
	*draft = decoded
	return nil
}

type contextEditWire struct {
	Target   EntryId           `json:"target"`
	Action   ContextEditAction `json:"action"`
	Messages json.RawMessage   `json:"messages,omitempty"`
}

// MarshalJSON encodes Messages when they are not nil.
func (edit ContextEdit) MarshalJSON() ([]byte, error) {
	messages, err := marshalModel(edit.Messages)
	if err != nil {
		return nil, err
	}
	return json.Marshal(contextEditWire{Target: edit.Target, Action: edit.Action, Messages: messages})
}

// UnmarshalJSON decodes Messages by message role.
func (edit *ContextEdit) UnmarshalJSON(data []byte) error {
	var wire contextEditWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	messages, err := unmarshalModel(wire.Messages)
	if err != nil {
		return fmt.Errorf("context edit messages: %w", err)
	}
	*edit = ContextEdit{Target: wire.Target, Action: wire.Action, Messages: messages}
	return nil
}

func marshalModel(messages []ai.Message) (json.RawMessage, error) {
	if messages == nil {
		return nil, nil
	}
	return json.Marshal(messages)
}

func unmarshalModel(raw json.RawMessage) ([]ai.Message, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	messages, err := DecodeMessages(raw)
	if err != nil {
		return nil, err
	}
	if messages == nil {
		messages = []ai.Message{}
	}
	return messages, nil
}

type taskOutcomeWire struct {
	Status TaskOutcomeStatus `json:"status"`
	Result json.RawMessage   `json:"result,omitempty"`
	Error  *TaskOutcomeError `json:"error,omitempty"`
	Reason *string           `json:"reason,omitempty"`
}

// MarshalJSON encodes a present Result, including JSON null; a completed outcome always carries one.
func (outcome TaskOutcome[R]) MarshalJSON() ([]byte, error) {
	wire := taskOutcomeWire{Status: outcome.Status, Error: outcome.Error, Reason: outcome.Reason}
	switch {
	case outcome.Result != nil:
		result, err := json.Marshal(*outcome.Result)
		if err != nil {
			return nil, err
		}
		wire.Result = result
	case outcome.Status == OutcomeCompleted:
		wire.Result = json.RawMessage("null")
	}
	return json.Marshal(wire)
}

// UnmarshalJSON keeps a present JSON-null result as a non-nil Result.
func (outcome *TaskOutcome[R]) UnmarshalJSON(data []byte) error {
	var wire taskOutcomeWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	decoded := TaskOutcome[R]{Status: wire.Status, Error: wire.Error, Reason: wire.Reason}
	if len(wire.Result) > 0 {
		result := new(R)
		if err := json.Unmarshal(wire.Result, result); err != nil {
			return fmt.Errorf("task outcome result: %w", err)
		}
		decoded.Result = result
	}
	*outcome = decoded
	return nil
}

type documentContentWire struct {
	Version int                 `json:"version"`
	Kind    DocumentContentKind `json:"kind"`
	Value   *JsonObject         `json:"value,omitempty"`
	Ops     *[]Op               `json:"ops,omitempty"`
}

// MarshalJSON encodes Value for a base and Ops for a delta even when empty: both members are required by their union
// member.
func (content DocumentContent) MarshalJSON() ([]byte, error) {
	wire := documentContentWire{Version: content.Version, Kind: content.Kind}
	if content.Value != nil || content.Kind == ContentBase {
		value := content.Value
		if value == nil {
			value = JsonObject{}
		}
		wire.Value = &value
	}
	if content.Ops != nil || content.Kind == ContentDelta {
		ops := content.Ops
		if ops == nil {
			ops = []Op{}
		}
		wire.Ops = &ops
	}
	return json.Marshal(wire)
}

type taskStateWire[S, R any] struct {
	Status     TaskStatus      `json:"status"`
	Checkpoint *S              `json:"checkpoint,omitempty"`
	On         *[]TaskId       `json:"on,omitempty"`
	Policy     JoinPolicy      `json:"policy,omitempty"`
	Outcome    *TaskOutcome[R] `json:"outcome,omitempty"`
}

// MarshalJSON encodes On for a waiting state even when it is empty: the waiting member requires it.
func (state TaskState[S, R]) MarshalJSON() ([]byte, error) {
	wire := taskStateWire[S, R]{Status: state.Status, Checkpoint: state.Checkpoint, Policy: state.Policy, Outcome: state.Outcome}
	if state.On != nil || state.Status == TaskWaiting {
		on := state.On
		if on == nil {
			on = []TaskId{}
		}
		wire.On = &on
	}
	return json.Marshal(wire)
}
