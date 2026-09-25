package pico3

import (
	"context"
	"fmt"
	"time"
)

// Registries are the live section and tool registries.
type Registries struct {
	Sections SectionRegistry
	Tools    ToolRegistry
}

// Runtime is what a kind's handlers get. Every commit checks the invocation
// token: a runtime captured by one handler expires when that handler returns.
type Runtime struct {
	TaskId         Id
	ConversationId Id
	Kind           *Kind
	Hooks          HookRunner
	Models         Models
	Registries     Registries
	ProcessHost    ProcessHost
	Plugins        map[string]PluginHandler

	harness   *Harness
	authority invoker
}

// Commit runs fn in one transaction with this invocation's authority; current
// is the live task.
func (rt *Runtime) Commit(ctx context.Context, fn func(ctx context.Context, tx *Tx, current Task) (any, error)) (any, error) {
	session := rt.harness.session
	var docs []DocRef
	if live, ok := session.liveTask(rt.TaskId); ok {
		for _, id := range live.Owns {
			docs = append(docs, RewindableDoc(id), StickyDoc(id))
		}
	}
	result, err := session.commit(ctx, rt.authority, func(lineCtx context.Context, tx *Tx, _ TransactionControl) (any, error) {
		live, _ := session.liveTask(rt.TaskId)
		return fn(lineCtx, tx, live)
	}, commitOptions{docs: docs})
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}

// CommitAs is Runtime.Commit with a typed result.
func CommitAs[T any](ctx context.Context, rt *Runtime, fn func(ctx context.Context, tx *Tx, current Task) (T, error)) (T, error) {
	var zero T
	value, err := rt.Commit(ctx, func(lineCtx context.Context, tx *Tx, current Task) (any, error) {
		return fn(lineCtx, tx, current)
	})
	if err != nil {
		return zero, err
	}
	typed, _ := value.(T)
	return typed, nil
}

// Tools returns the current tool registry.
func (rt *Runtime) Tools() map[string]*ToolDeclaration { return rt.Registries.Tools.Map() }

// KindNamed returns a registered kind.
func (rt *Runtime) KindNamed(name string) *Kind { return rt.harness.kinds.get(name) }

// Now returns the durable clock in milliseconds.
func (rt *Runtime) Now() float64 { return rt.harness.now() }

// Sleep waits until the wall-clock time untilMs.
func (rt *Runtime) Sleep(ctx context.Context, untilMs float64) error {
	return rt.harness.clock.sleepUntil(ctx, untilMs)
}

// WaitForInput waits for an input to settle.
func (rt *Runtime) WaitForInput(ctx context.Context, id Id) (Input, error) {
	return rt.harness.scheduler.waitForInput(ctx, id)
}

// WaitForTask waits for a task to terminalize.
func (rt *Runtime) WaitForTask(ctx context.Context, id Id) (Task, error) {
	return rt.harness.scheduler.waitForTask(ctx, id)
}

// AbortTask aborts a task in this task's conversation or subtree.
func (rt *Runtime) AbortTask(ctx context.Context, id Id) (string, error) {
	h := rt.harness
	_, err := h.session.onLine(ctx, func(lineCtx context.Context) (any, error) {
		if err := h.assertInvocation(rt.authority); err != nil {
			return nil, err
		}
		target, ok := h.session.liveTask(id)
		if !ok {
			stored, err := h.session.storage.Task(lineCtx, id)
			if err != nil {
				return nil, err
			}
			if stored == nil {
				return nil, fmt.Errorf("task %d not found", id)
			}
			target = *stored
		}
		return nil, h.assertTaskConversationScope(rt.TaskId, target.ConversationId)
	})
	if err != nil {
		return "", err
	}
	return h.scheduler.abortTask(ctx, id)
}

// AbortConversation aborts a conversation this task owns.
func (rt *Runtime) AbortConversation(ctx context.Context, id Id) error {
	h := rt.harness
	_, err := h.session.onLine(ctx, func(context.Context) (any, error) {
		if err := h.assertInvocation(rt.authority); err != nil {
			return nil, err
		}
		return nil, h.assertOwnedConversation(rt.TaskId, id)
	})
	if err != nil {
		return err
	}
	handle, err := h.Conversation(ctx, id)
	if err != nil || handle == nil {
		return err
	}
	return handle.Abort(ctx)
}

// CreateOwnedConversation creates a conversation this task owns.
func (rt *Runtime) CreateOwnedConversation(ctx context.Context, spec OwnedConversationSpec) (Id, error) {
	h := rt.harness
	result, err := h.session.commit(ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		if err := h.assertInvocation(rt.authority); err != nil {
			return nil, err
		}
		return tx.CreateOwnedConversation(rt.TaskId, rt.ConversationId, spec)
	}, commitOptions{})
	if err != nil {
		return 0, err
	}
	id, _ := result.Value.(Id)
	return id, nil
}

// SendOwned admits input to a conversation this task owns.
func (rt *Runtime) SendOwned(ctx context.Context, conversationId Id, input SendInput) (Id, error) {
	h := rt.harness
	result, err := h.session.commit(ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		if err := h.assertInvocation(rt.authority); err != nil {
			return nil, err
		}
		if err := h.assertOwnedConversation(rt.TaskId, conversationId); err != nil {
			return nil, err
		}
		return tx.Send(conversationId, input)
	}, commitOptions{docs: []DocRef{RewindableDoc(conversationId), StickyDoc(conversationId)}})
	if err != nil {
		return 0, err
	}
	id, _ := result.Value.(Id)
	return id, nil
}

// Context derives model context off the line.
func (rt *Runtime) Context(ctx context.Context, conversationId Id, at *Id) (ContextView, error) {
	return CommitAs(ctx, rt, func(_ context.Context, tx *Tx, _ Task) (ContextView, error) {
		return tx.Context(conversationId, at)
	})
}

// NewestEntry reads the newest matching entry off the line.
func (rt *Runtime) NewestEntry(ctx context.Context, conversationId Id, options NewestOptions) (*Entry, error) {
	return CommitAs(ctx, rt, func(_ context.Context, tx *Tx, _ Task) (*Entry, error) {
		return tx.NewestEntry(conversationId, options)
	})
}

// Rewindable snapshots a conversation's rewindable document.
func (rt *Runtime) Rewindable(ctx context.Context, conversationId Id) (JsonObject, error) {
	return rt.snapshot(ctx, RewindableDoc(conversationId))
}

// Sticky snapshots a conversation's sticky document.
func (rt *Runtime) Sticky(ctx context.Context, conversationId Id) (JsonObject, error) {
	return rt.snapshot(ctx, StickyDoc(conversationId))
}

func (rt *Runtime) snapshot(ctx context.Context, ref DocRef) (JsonObject, error) {
	result, err := rt.harness.session.commit(ctx, rt.authority, func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		return tx.Snapshot(ref)
	}, commitOptions{docs: []DocRef{ref}})
	if err != nil {
		return nil, err
	}
	value, _ := result.Value.(JsonObject)
	return value, nil
}

// RewindableAsOf reads the rewindable state as of an entry.
func (rt *Runtime) RewindableAsOf(ctx context.Context, conversationId, at Id) (JsonObject, error) {
	return CommitAs(ctx, rt, func(_ context.Context, tx *Tx, _ Task) (JsonObject, error) {
		return tx.RewindableAsOf(conversationId, at)
	})
}

// clock supplies wall time and sleeps; tests substitute a manual clock.
type clock interface {
	now() float64
	sleepUntil(ctx context.Context, untilMs float64) error
}

type wallClock struct{}

func (wallClock) now() float64 { return float64(time.Now().UnixMilli()) }

func (wallClock) sleepUntil(ctx context.Context, untilMs float64) error {
	wait := time.Duration(max(0, untilMs-wallClock{}.now())) * time.Millisecond
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
