package pico3

import (
	"context"
	"errors"
	"slices"
)

// ConversationHandle is the host's handle to one conversation.
type ConversationHandle struct {
	Id      Id
	harness *Harness
	docs    []DocRef
}

func (h *Harness) handle(conversation Conversation) *ConversationHandle {
	return &ConversationHandle{Id: conversation.Id, harness: h, docs: []DocRef{RewindableDoc(conversation.Id), StickyDoc(conversation.Id)}}
}

func (handle *ConversationHandle) host(ctx context.Context, fn func(ctx context.Context, tx *Tx) (any, error)) (any, error) {
	result, err := handle.harness.session.commit(ctx, hostInvoker(handle.Id), func(lineCtx context.Context, tx *Tx, _ TransactionControl) (any, error) {
		return fn(lineCtx, tx)
	}, commitOptions{docs: handle.docs})
	return result.Value, err
}

func (handle *ConversationHandle) kernel(ctx context.Context, fn func(ctx context.Context, tx *Tx) (any, error)) (any, error) {
	conversationId := handle.Id
	result, err := handle.harness.session.commit(ctx, kernelInvoker(&conversationId), func(lineCtx context.Context, tx *Tx, _ TransactionControl) (any, error) {
		return fn(lineCtx, tx)
	}, commitOptions{docs: handle.docs})
	return result.Value, err
}

// Commit runs fn in one host transaction.
func (handle *ConversationHandle) Commit(ctx context.Context, fn func(ctx context.Context, tx *Tx) (any, error)) (any, error) {
	return handle.host(ctx, fn)
}

// HostCommit is ConversationHandle.Commit with a typed result.
func HostCommit[T any](ctx context.Context, handle *ConversationHandle, fn func(ctx context.Context, tx *Tx) (T, error)) (T, error) {
	var zero T
	value, err := handle.host(ctx, func(lineCtx context.Context, tx *Tx) (any, error) { return fn(lineCtx, tx) })
	if err != nil {
		return zero, err
	}
	typed, _ := value.(T)
	return typed, nil
}

// Config returns the flat configuration facade.
func (handle *ConversationHandle) Config() *ConfigFacade { return &ConfigFacade{handle: handle} }

// Send admits input and returns its handle.
func (handle *ConversationHandle) Send(ctx context.Context, input SendInput) (*InputHandle, error) {
	value, err := handle.kernel(ctx, func(_ context.Context, tx *Tx) (any, error) { return tx.Send(handle.Id, input) })
	if err != nil {
		return nil, err
	}
	return handle.harness.inputHandle(value.(Id)), nil
}

// Write writes a passive entry and returns its input id.
func (handle *ConversationHandle) Write(ctx context.Context, entry NewEntry) (Id, error) {
	value, err := handle.host(ctx, func(_ context.Context, tx *Tx) (any, error) { return tx.Write(handle.Id, entry) })
	id, _ := value.(Id)
	return id, err
}

// Rewindable snapshots the rewindable document.
func (handle *ConversationHandle) Rewindable(ctx context.Context) (JsonObject, error) {
	value, err := handle.host(ctx, func(_ context.Context, tx *Tx) (any, error) { return tx.Snapshot(RewindableDoc(handle.Id)) })
	document, _ := value.(JsonObject)
	return document, err
}

// Sticky snapshots the sticky document.
func (handle *ConversationHandle) Sticky(ctx context.Context) (JsonObject, error) {
	value, err := handle.host(ctx, func(_ context.Context, tx *Tx) (any, error) { return tx.Snapshot(StickyDoc(handle.Id)) })
	document, _ := value.(JsonObject)
	return document, err
}

// Context derives model context at the tip.
func (handle *ConversationHandle) Context(ctx context.Context) (ContextView, error) {
	value, err := handle.host(ctx, func(_ context.Context, tx *Tx) (any, error) { return tx.Context(handle.Id, nil) })
	view, _ := value.(ContextView)
	return view, err
}

// Fork forks this conversation at an entry, or at the start when at is nil.
func (handle *ConversationHandle) Fork(ctx context.Context, at *Id, spec ConversationSpec) (*ConversationHandle, error) {
	id, err := handle.harness.session.fork(ctx, handle.Id, at, spec)
	if err != nil {
		return nil, err
	}
	return handle.harness.Conversation(ctx, id)
}

// Collapse starts a manual background collapse and returns its task id.
func (handle *ConversationHandle) Collapse(ctx context.Context, instructions *string) (Id, error) {
	value, err := handle.kernel(ctx, func(_ context.Context, tx *Tx) (any, error) {
		view, err := tx.Context(handle.Id, nil)
		if err != nil {
			return nil, err
		}
		state, err := tx.raw(RewindableDoc(handle.Id))
		if err != nil {
			return nil, err
		}
		through := chooseThrough(view.Entries, numberOr(state["keepRecent"], 0))
		if through == nil {
			return nil, errors.New("nothing to collapse")
		}
		input := JsonObject{"reason": "manual", "through": float64(*through)}
		if instructions != nil {
			input["instructions"] = *instructions
		}
		conversationId := handle.Id
		return tx.CreateTaskSpec(TaskSpec{Kind: "pi.collapse", ConversationId: &conversationId, Background: true, Input: input})
	})
	id, _ := value.(Id)
	return id, err
}

// Reset writes a head: a bare reset, or a handoff carrying text.
func (handle *ConversationHandle) Reset(ctx context.Context, handoff *string) error {
	_, err := handle.kernel(ctx, func(_ context.Context, tx *Tx) (any, error) {
		entry := NewEntry{Kind: "pi.reset", Head: SelfHead()}
		if handoff != nil {
			entry = NewEntry{Kind: "pi.handoff", Head: SelfHead(), Model: []JsonObject{{"role": "user", "content": *handoff, "timestamp": handle.harness.now()}}}
		}
		return tx.Write(handle.Id, entry)
	})
	return err
}

// Abort withdraws queued steer/followUp input, marks and aborts every
// foreground task, and waits for this conversation and its owned children.
func (handle *ConversationHandle) Abort(ctx context.Context) error {
	var owned []Id
	value, err := handle.kernel(ctx, func(_ context.Context, tx *Tx) (any, error) {
		marked, children, err := handle.withdrawAndMark(tx)
		owned = children
		return marked, err
	})
	if err != nil {
		return err
	}
	scheduler := handle.harness.scheduler
	for _, id := range value.([]Id) {
		if _, err := scheduler.abortTask(ctx, id); err != nil {
			return err
		}
	}
	conversationId := handle.Id
	if err := scheduler.waitForIdle(ctx, &conversationId); err != nil {
		return err
	}
	return waitAllIdle(ctx, scheduler, owned)
}

func waitAllIdle(ctx context.Context, scheduler *scheduler, conversations []Id) error {
	errs := make(chan error, len(conversations))
	for _, id := range conversations {
		go func() { errs <- scheduler.waitForIdle(ctx, &id) }()
	}
	var joined error
	for range conversations {
		joined = errors.Join(joined, <-errs)
	}
	return joined
}

func (handle *ConversationHandle) withdrawAndMark(tx *Tx) ([]Id, []Id, error) {
	sticky, err := tx.raw(StickyDoc(handle.Id))
	if err != nil {
		return nil, nil, err
	}
	var withdrawn []Id
	var kept []any
	for _, item := range arr(sticky, "inbox") {
		object := asObject(item)
		if str(object, "mode") == "write" {
			kept = append(kept, item)
			continue
		}
		id, _ := asID(object["id"])
		withdrawn = append(withdrawn, id)
	}
	if kept == nil {
		kept = []any{}
	}
	sticky["inbox"] = kept
	if err := tx.resolveInputs(withdrawn, InputResolution{Status: InputUnanswered, Reason: "aborted"}); err != nil {
		return nil, nil, err
	}
	for _, id := range withdrawn {
		tx.addEvent(handle.Id, ViewEvent{"type": "input.aborted", "input": float64(id)})
	}
	var marked, owned []Id
	for _, task := range handle.harness.session.liveTaskList() {
		if task.ConversationId != handle.Id || task.Background {
			continue
		}
		owned = append(owned, task.Owns...)
		if !task.Abort {
			if err := tx.MarkTask(task.Id); err != nil {
				return nil, nil, err
			}
		}
		marked = append(marked, task.Id)
	}
	return marked, owned, nil
}

// WaitForIdle waits until no foreground task is live in this conversation.
func (handle *ConversationHandle) WaitForIdle(ctx context.Context) error {
	conversationId := handle.Id
	return handle.harness.scheduler.waitForIdle(ctx, &conversationId)
}

// Hooks registers handlers scoped to this conversation, and to its owned
// subtree when subtree is true.
func (handle *ConversationHandle) Hooks(namespace *Namespace, kind *Kind, handlers any, subtree bool) (func(), error) {
	h := handle.harness
	if err := h.checkNamespace(namespace); err != nil {
		return nil, err
	}
	if err := h.checkKind(kind); err != nil {
		return nil, err
	}
	conversationId := handle.Id
	return h.hooks.add(&hookRegistration{namespace: namespace, kind: kind, handlers: handlers, conversationId: &conversationId, subtree: subtree}), nil
}

// Watch captures the view and subscribes in one line operation.
func (handle *ConversationHandle) Watch(ctx context.Context) (*Watch, error) {
	h := handle.harness
	value, err := handle.host(ctx, func(_ context.Context, tx *Tx) (any, error) {
		entries, err := CaptureActiveTranscript(tx.ScanEntries, handle.Id)
		if err != nil {
			return nil, err
		}
		conversation, ok := h.session.conversationRecord(handle.Id)
		if !ok {
			return nil, errors.New("conversation is not loaded")
		}
		return h.views.watch(conversation, entries)
	})
	watch, _ := value.(*Watch)
	return watch, err
}

// ConfigFacade reads and writes every registered kind's configuration flat.
type ConfigFacade struct {
	handle *ConversationHandle
}

// Get returns every routed key's effective value.
func (facade *ConfigFacade) Get(ctx context.Context) (JsonObject, error) {
	value, err := facade.handle.host(ctx, func(_ context.Context, tx *Tx) (any, error) {
		access, err := tx.Config(facade.handle.Id)
		if err != nil {
			return nil, err
		}
		out := JsonObject{}
		for _, key := range facade.handle.harness.session.defaults.keys() {
			value, err := access.Get(key)
			if err != nil {
				return nil, err
			}
			out[key] = value
		}
		return out, nil
	})
	config, _ := value.(JsonObject)
	return config, err
}

// Set writes a patch in one commit, routed to the declaring documents.
func (facade *ConfigFacade) Set(ctx context.Context, patch JsonObject) error {
	_, err := facade.handle.host(ctx, func(_ context.Context, tx *Tx) (any, error) {
		access, err := tx.Config(facade.handle.Id)
		if err != nil {
			return nil, err
		}
		for _, key := range sortedKeys(patch) {
			if err := access.Set(key, patch[key]); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

// Reset deletes persisted overrides.
func (facade *ConfigFacade) Reset(ctx context.Context, keys []string) error {
	_, err := facade.handle.host(ctx, func(_ context.Context, tx *Tx) (any, error) {
		access, err := tx.Config(facade.handle.Id)
		if err != nil {
			return nil, err
		}
		for _, key := range slices.Clone(keys) {
			if err := access.Reset(key); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

// InputHandle tracks one admitted input.
type InputHandle struct {
	Id      Id
	harness *Harness
}

func (h *Harness) inputHandle(id Id) *InputHandle { return &InputHandle{Id: id, harness: h} }

// Result reads the input's current record.
func (handle *InputHandle) Result(ctx context.Context) (*Input, error) {
	value, err := handle.harness.session.read(ctx, func(lineCtx context.Context, storage Storage) (any, error) {
		return storage.Input(lineCtx, handle.Id)
	})
	input, _ := value.(*Input)
	return input, err
}

// Wait waits until the input is done or unanswered.
func (handle *InputHandle) Wait(ctx context.Context) (Input, error) {
	return handle.harness.scheduler.waitForInput(ctx, handle.Id)
}

// Abort withdraws the input if it is still queued.
func (handle *InputHandle) Abort(ctx context.Context) (string, error) {
	result, err := handle.harness.session.commit(ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		return tx.WithdrawInput(handle.Id)
	}, commitOptions{})
	status, _ := result.Value.(string)
	return status, err
}
