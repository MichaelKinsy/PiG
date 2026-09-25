package pico3

import (
	"context"
	"errors"
	"fmt"
)

// ToolProgress is the free part of a tool slot a tool may mutate; identity
// fields (callId, name, args, output) are protected.
type ToolProgress struct {
	Progress    *string
	Details     JsonValue
	ContinuedBy *Id
}

// ToolApi is what a tool or plugin handler gets.
type ToolApi struct {
	TaskId         Id
	ConversationId Id
	CallId         string

	rt       *Runtime
	stream   func(chunk []byte)
	progress func(ctx context.Context, update func(slot *ToolProgress)) error
	memo     func(ctx context.Context, name string, candidate *JsonValue) (JsonValue, bool, error)
}

// Stream pipes raw output to the kernel, which bounds it per the tool's
// declaration, flushes it on a throttle, and uses it as the result content
// unless the tool returns its own. It is synchronous.
func (api *ToolApi) Stream(chunk []byte) error {
	if api.stream == nil {
		return errors.New("stream is only available to tools")
	}
	api.stream(chunk)
	return nil
}

// Progress mutates the live slot's free fields.
func (api *ToolApi) Progress(ctx context.Context, update func(slot *ToolProgress)) error {
	if api.progress == nil {
		return errors.New("progress is only available to tools")
	}
	return api.progress(ctx, update)
}

// Memo stores candidate under name unless a value exists; it returns the
// durable winner. A stored null wins.
func (api *ToolApi) Memo(ctx context.Context, name string, candidate JsonValue) (JsonValue, error) {
	if api.memo == nil {
		return nil, errors.New("memo is only available to tools")
	}
	value, _, err := api.memo(ctx, name, &candidate)
	return value, err
}

// MemoGet reads a memo; ok is false when unset.
func (api *ToolApi) MemoGet(ctx context.Context, name string) (JsonValue, bool, error) {
	if api.memo == nil {
		return nil, false, errors.New("memo is only available to tools")
	}
	return api.memo(ctx, name, nil)
}

// OwnedConversation is the narrow handle a task gets to a conversation it
// owns.
type OwnedConversation struct {
	Id Id
	rt *Runtime
}

// OwnedInput tracks input sent to an owned conversation.
type OwnedInput struct {
	Id Id
	rt *Runtime
}

// Conversation creates a conversation this task owns.
func (api *ToolApi) Conversation(ctx context.Context, spec OwnedConversationSpec) (*OwnedConversation, error) {
	id, err := api.rt.CreateOwnedConversation(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &OwnedConversation{Id: id, rt: api.rt}, nil
}

// Send admits input to the owned conversation.
func (conversation *OwnedConversation) Send(ctx context.Context, input SendInput) (*OwnedInput, error) {
	id, err := conversation.rt.SendOwned(ctx, conversation.Id, input)
	if err != nil {
		return nil, err
	}
	return &OwnedInput{Id: id, rt: conversation.rt}, nil
}

// Abort aborts the owned conversation.
func (conversation *OwnedConversation) Abort(ctx context.Context) error {
	return conversation.rt.AbortConversation(ctx, conversation.Id)
}

// Wait waits for the input to settle.
func (input *OwnedInput) Wait(ctx context.Context) (Input, error) {
	return input.rt.WaitForInput(ctx, input.Id)
}

// Result reads the input's current record.
func (input *OwnedInput) Result(ctx context.Context) (*Input, error) {
	return CommitAs(ctx, input.rt, func(_ context.Context, tx *Tx, _ Task) (*Input, error) { return tx.Input(input.Id) })
}

// Task creates a task from a kind token in this task's conversation.
func (api *ToolApi) Task(ctx context.Context, kind *Kind, input JsonValue, options TaskOptions) (TaskRef, error) {
	if kind == nil {
		return TaskRef{}, errors.New("task(): pass a kind token")
	}
	options.ConversationId = nil
	return CommitAs(ctx, api.rt, func(_ context.Context, tx *Tx, _ Task) (TaskRef, error) {
		return tx.CreateTask(kind, input, options)
	})
}

// GetTask reads a task.
func (api *ToolApi) GetTask(ctx context.Context, ref TaskRef) (*Task, error) {
	return CommitAs(ctx, api.rt, func(_ context.Context, tx *Tx, _ Task) (*Task, error) { return tx.Task(ref.Id) })
}

// WaitForTask waits for a task to terminalize.
func (api *ToolApi) WaitForTask(ctx context.Context, ref TaskRef) (Task, error) {
	return api.rt.WaitForTask(ctx, ref.Id)
}

// Slot snapshots another task's live slot in this task's conversation or
// subtree; nil when absent.
func (api *ToolApi) Slot(ctx context.Context, ref TaskRef) (JsonObject, error) {
	return CommitAs(ctx, api.rt, func(_ context.Context, tx *Tx, _ Task) (JsonObject, error) {
		stored, err := tx.Task(ref.Id)
		if err != nil || stored == nil {
			return nil, err
		}
		sticky, err := tx.Snapshot(StickyDoc(stored.ConversationId))
		if err != nil {
			return nil, err
		}
		slot, _ := obj(sticky, "tasks")[fmt.Sprint(ref.Id)].(map[string]any)
		return slot, nil
	})
}

// taskApi is the ToolApi an ordinary plugin handler gets: no stream,
// progress, or memo.
func taskApi(task Task, rt *Runtime) *ToolApi {
	return &ToolApi{TaskId: task.Id, ConversationId: task.ConversationId, rt: rt}
}
