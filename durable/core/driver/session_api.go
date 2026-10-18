// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// invocation is what extension code reaches: the reads, memos and commits of a tool, a hook, a section or an environment
// builder, bound to one task and conversation. Every operation is one request on a channel of the ABI.
type invocation struct {
	s            *coreSession
	task         durable.TaskId
	conversation durable.ConversationId
}

var (
	_ ToolBackend     = (*invocation)(nil)
	_ SectionBackend  = (*invocation)(nil)
	_ harness.HookApi = (*invocation)(nil)
)

func (i *invocation) TaskId() durable.TaskId                 { return i.task }
func (i *invocation) ConversationId() durable.ConversationId { return i.conversation }

func (i *invocation) SnapshotErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.JsonObject, error) {
	return i.s.snapshot(ctx, token, nil, args)
}

func (i *invocation) SnapshotAsOfErased(ctx context.Context, token durable.AnyDocToken, at durable.EntryId, args ...any) (durable.JsonObject, error) {
	return i.s.snapshot(ctx, token, &at, args)
}

func (i *invocation) WatchDocErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.WatchHandle[durable.JsonObject], error) {
	return i.s.watchDoc(ctx, token, args)
}

func (i *invocation) Registry() durable.RegistrySnapshot { return i.s.cfg.Harness.Registry.Snapshot() }

func (i *invocation) Agent(ctx context.Context) (durable.Agent, error) {
	return i.s.agent(ctx, i.conversation)
}

func (i *invocation) Models() durable.Models { return i.s.cfg.Harness.CurrentModels() }

func (i *invocation) Env() env.ExecutionEnv { return i.s.envs.Get(i.task) }

func (i *invocation) Now() float64 { return i.s.now() }

func (i *invocation) Commit(ctx context.Context, change func(tx durable.Tx) (any, error)) (any, error) {
	return i.s.commit(ctx, map[string]any{"conversationId": i.conversation}, change)
}

func (i *invocation) Memo(ctx context.Context, name string) (durable.JsonValue, bool, error) {
	raw, err := i.s.client.API(ctx, "memo.get", map[string]any{"taskId": i.task, "name": name})
	if err != nil || string(raw) == "null" {
		return nil, false, err
	}
	var value durable.JsonValue
	return value, true, json.Unmarshal(raw, &value)
}

func (i *invocation) MemoCandidate(ctx context.Context, name string, candidate durable.JsonValue) (durable.JsonValue, error) {
	raw, err := i.s.client.API(ctx, "memo.put", map[string]any{"taskId": i.task, "name": name, "candidate": candidate})
	if err != nil {
		return nil, err
	}
	var value durable.JsonValue
	return value, json.Unmarshal(raw, &value)
}

func (i *invocation) CreateTaskErased(ctx context.Context, task durable.AnyTask, input durable.JsonValue, options durable.TaskOptions) (durable.TaskId, error) {
	options.ConversationId = nil
	id, err := i.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.CreateTaskErased(task, input, options) })
	if err != nil {
		return 0, err
	}
	return id.(durable.TaskId), nil
}

func (i *invocation) GetTask(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	return i.s.GetTask(ctx, id)
}

func (i *invocation) WaitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error) {
	return i.s.WaitForTask(ctx, id)
}

func (i *invocation) Conversation(ctx context.Context, id durable.ConversationId) (durable.ConversationHandle, error) {
	c, err := i.s.Conversation(ctx, id)
	if err != nil || c == nil {
		return nil, err
	}
	return handle{c.(*conversation)}, nil
}

// handle is the invocation-bound view of a conversation.
type handle struct{ c *conversation }

func (h handle) Id() durable.ConversationId { return h.c.id }
func (h handle) Submit(ctx context.Context, draft durable.InputSubmissionDraft) (durable.Submission, error) {
	return h.c.Submit(ctx, draft)
}
func (h handle) Abort(ctx context.Context, options *durable.ConversationAbortOptions) error {
	return h.c.Abort(ctx, options)
}
func (h handle) WaitForIdle(ctx context.Context) error { return h.c.WaitForIdle(ctx) }

// snapshot reads one document's committed value (api snapshot, snapshotAsOf).
func (s *coreSession) snapshot(ctx context.Context, token durable.AnyDocToken, at *durable.EntryId, args []any) (durable.JsonObject, error) {
	// Admission is sealed synchronously, before the owner is asked (session.ts snapshot :164 and snapshotAsOf :318 #assertUsable).
	release, err := s.admit()
	if err != nil {
		return nil, err
	}
	defer release()
	def := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(def, args)
	if err != nil {
		return nil, err
	}
	// The core checks the stored record against the definition's semantics (as the tx doc op does): without them a
	// rewindable document such as pi.agent fails "does not match the supplied definition semantics".
	op, body := "snapshot", map[string]any{"doc": wireAddress(resolved.Address), "def": wireDef(def)}
	if at != nil {
		op, body["at"] = "snapshotAsOf", *at
	}
	raw, err := migrateRead(def, func(migrated durable.JsonObject) (json.RawMessage, error) {
		if migrated != nil {
			body["migrated"] = migrated
		}
		return s.client.API(ctx, op, body)
	})
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return nil, err
	}
	var value durable.JsonObject
	return value, json.Unmarshal(raw, &value)
}

// agent is Pi's runtime.agent(): the committed pi.agent of the conversation resolved against the registry and settings
// (harness.ResolveAgent), as the resolveAgent hook answers it. The host holds the registry, so it resolves here; the
// core has no agent api op to answer, and every section render and api.Agent() failed "session: not implemented".
func (s *coreSession) agent(ctx context.Context, id durable.ConversationId) (durable.Agent, error) {
	state, err := durable.Snapshot[harness.AgentState](ctx, s, harness.AgentDoc, id)
	if err != nil {
		return durable.Agent{}, err
	}
	return harness.ResolveAgent(state, s.cfg.Harness.Registry.Snapshot(), s.resolveSettings(), func(err error, extension string) {
		s.report(err, durable.ReportInfo{Type: durable.ReportExtensionError, ConversationId: &id, Extension: extension})
	}), nil
}

// commit runs one Session commit on the extension transaction channel; binding carries the invocation's conversation.
func (s *coreSession) commit(ctx context.Context, binding map[string]any, change func(tx durable.Tx) (any, error)) (any, error) {
	release, err := s.admit()
	if err != nil {
		return nil, err
	}
	defer release()
	holding := false
	defer func() {
		if holding {
			s.watches.leaveCommit(ctx)
		}
	}()
	return runCommit(ctx, func(ctx context.Context) (*Tx, error) {
		tx, err := s.client.Begin(ctx, binding)
		if err == nil {
			// The commit holds the Session line from `ready` until its answer: the publications that arrive meanwhile are its own.
			s.watches.enterCommit(ctx)
			holding = true
		}
		return tx, err
	}, change)
}

// SnapshotErased, SnapshotAsOfErased and WatchDocErased make Session a durable.DocumentReader and DocumentObserver.
func (s *coreSession) SnapshotErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.JsonObject, error) {
	return s.snapshot(ctx, token, nil, args)
}
func (s *coreSession) SnapshotAsOfErased(ctx context.Context, token durable.AnyDocToken, at durable.EntryId, args ...any) (durable.JsonObject, error) {
	return s.snapshot(ctx, token, &at, args)
}
func (s *coreSession) WatchDocErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.WatchHandle[durable.JsonObject], error) {
	return s.watchDoc(ctx, token, args)
}

// Commit runs one atomic transaction on the Session mutation line.
func (s *coreSession) Commit(ctx context.Context, change func(tx durable.Tx) (any, error)) (any, error) {
	return s.commit(ctx, nil, change)
}

// SubscribeClose observes close when it begins (session.ts close: the close listeners run synchronously in close(), before
// the line drains). The returned function unsubscribes.
func (s *coreSession) SubscribeClose(listener func()) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return func() {}
	}
	s.nextCloseListener++
	id := s.nextCloseListener
	if s.closeListeners == nil {
		s.closeListeners = map[int]func(){}
	}
	s.closeListeners[id] = listener
	return func() {
		s.mu.Lock()
		delete(s.closeListeners, id)
		s.mu.Unlock()
	}
}

// Root returns the reserved root conversation, creating it with options.Agent and options.Init in one commit when absent.
func (s *coreSession) Root(ctx context.Context, options *harness.RootOptions) (harness.Conversation, error) {
	var create harness.ConversationCreateOptions
	if options != nil {
		create.Agent, create.Init = options.Agent, options.Init
	}
	existed := false
	id, err := s.create(ctx, func(tx *txProxy) (durable.ConversationRecord, error) {
		if existing, err := tx.Conversation(RootConversationID); err != nil || existing != nil {
			// harness.ts #create: an existing root returns before the agent change and init, which run only for a creation.
			existed = true
			return durable.ConversationRecord{Id: RootConversationID}, err
		}
		return tx.createRoot()
	}, create, &existed)
	if err != nil {
		return nil, err
	}
	return &conversation{s: s, id: id}, nil
}

// CreateConversation creates an independent conversation.
func (s *coreSession) CreateConversation(ctx context.Context, options harness.ConversationCreateOptions) (harness.Conversation, error) {
	id, err := s.create(ctx, func(tx *txProxy) (durable.ConversationRecord, error) {
		return tx.CreateConversation(durable.CreateConversationOptions{Ownership: options.Ownership})
	}, options, nil)
	if err != nil {
		return nil, err
	}
	return &conversation{s: s, id: id}, nil
}

// create commits one conversation creation; skip, when set by make, ends the commit before the agent change and init.
func (s *coreSession) create(ctx context.Context, make func(tx *txProxy) (durable.ConversationRecord, error), options harness.ConversationCreateOptions, skip *bool) (durable.ConversationId, error) {
	result, err := s.commit(ctx, nil, func(tx durable.Tx) (any, error) {
		proxy := tx.(*txProxy)
		record, err := make(proxy)
		if err != nil {
			return nil, err
		}
		if skip != nil && *skip {
			return record.Id, nil
		}
		if options.Agent != nil {
			if err := harness.Configure(tx, record.Id, *options.Agent); err != nil {
				return nil, err
			}
		}
		if options.Init != nil {
			if err := options.Init(tx, record.Id); err != nil {
				return nil, err
			}
		}
		return record.Id, nil
	})
	if err != nil {
		return 0, err
	}
	return result.(durable.ConversationId), nil
}

// Conversation returns nil when absent.
func (s *coreSession) Conversation(ctx context.Context, id durable.ConversationId) (harness.Conversation, error) {
	release, err := s.admit()
	if err != nil {
		return nil, err
	}
	defer release()
	raw, err := s.client.API(ctx, "conversation", map[string]any{"conversationId": id})
	if err != nil || string(raw) == "null" {
		return nil, err
	}
	return &conversation{s: s, id: id}, nil
}

// GetTask reads Pi's tasks table: it needs no core state (CONTRACT section 3.10). nil when absent.
func (s *coreSession) GetTask(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	rows, err := s.host.Query(ctx, "SELECT record FROM tasks WHERE id = ?", abi.Int(int64(id)))
	if err != nil || len(rows) == 0 {
		return nil, s.client.poisonedOr(err)
	}
	var record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	return &record, json.Unmarshal(rows[0][0].Bytes, &record)
}

// submissionRecord reads one submission; nil when absent.
func (s *coreSession) submissionRecord(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	rows, err := s.host.Query(ctx, "SELECT record FROM submissions WHERE id = ?", abi.Int(int64(id)))
	if err != nil || len(rows) == 0 {
		return nil, s.client.poisonedOr(err)
	}
	var record durable.SubmissionRecord
	return &record, json.Unmarshal(rows[0][0].Bytes, &record)
}

// Submission reacquires a submission; nil when absent.
func (s *coreSession) Submission(ctx context.Context, id durable.SubmissionId) (durable.Submission, error) {
	record, err := s.submissionRecord(ctx, id)
	if err != nil || record == nil {
		return nil, err
	}
	return &submission{s: s, id: id}, nil
}

// AbortSubmission asks the core to withdraw a submission.
func (s *coreSession) AbortSubmission(ctx context.Context, id durable.SubmissionId, conversationId *durable.ConversationId) (durable.SubmissionAbortResult, error) {
	s.kick()
	body := map[string]any{"submissionId": id}
	if conversationId != nil {
		body["conversationId"] = *conversationId
	}
	raw, err := s.client.Abort(ctx, body)
	if err != nil {
		return "", err
	}
	var result durable.SubmissionAbortResult
	return result, json.Unmarshal(raw, &result)
}

// RetryTask runs a task parked by settings.restartLimit again: it clears its restart count and enables scheduling
// (scheduler.ts retry). It does nothing for a task that is not parked, and fails for an unknown one.
func (s *coreSession) RetryTask(ctx context.Context, id durable.TaskId) error {
	release, err := s.admit()
	if err != nil {
		return err
	}
	defer release()
	_, err = s.client.API(ctx, "retryTask", map[string]any{"taskId": id})
	return err
}

// AbortTask commits the abort mark and schedules the abort invocation.
func (s *coreSession) AbortTask(ctx context.Context, id durable.TaskId) (string, error) {
	return s.abortTask(ctx, id, false)
}

// abortTask is AbortTask; with keepRestart, a task marked restart keeps its mark and waits on, as for an owner's own cleanup
// (scheduler.ts abort).
func (s *coreSession) abortTask(ctx context.Context, id durable.TaskId, keepRestart bool) (string, error) {
	// Harness.abortTask() does not resume scheduling (harness.ts abortTask): the mark commits and a paused scheduler stays paused.
	body := map[string]any{"taskId": id}
	if keepRestart {
		body["keepRestart"] = true
	}
	raw, err := s.client.Abort(ctx, body)
	if err != nil {
		return "", err
	}
	var result string
	return result, json.Unmarshal(raw, &result)
}

// WaitForTask returns the terminal receipt of a task; cancelling ctx cancels only this wait.
func (s *coreSession) WaitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error) {
	var record durable.SettledTask[durable.JsonValue]
	// scheduler.ts waitForTask: a closed Harness rejects at the call, and the seal rejects a wait that is pending.
	ctx, done, err := s.closeAware(ctx)
	if err != nil {
		return record, err
	}
	defer done()
	s.kick()
	raw, err := s.client.API(ctx, "waitForTask", map[string]any{"taskId": id})
	if err != nil {
		return record, s.closedOr(ctx, err)
	}
	return record, json.Unmarshal(raw, &record)
}

// WaitForIdle returns when the ordinary ownership scope of every ownerless conversation has no live non-background task.
func (s *coreSession) WaitForIdle(ctx context.Context) error {
	s.kick()
	return s.idle(ctx, nil)
}

// taskGraphState is the api taskGraph answer: every live task with its owner edge and owned conversations.
type taskGraphState struct {
	Tasks map[string]struct {
		ConversationID int64   `json:"conversationId"`
		Owner          *int64  `json:"owner"`
		Background     bool    `json:"background"`
		Conversations  []int64 `json:"conversations"`
	} `json:"tasks"`
}

func (g taskGraphState) liveIn(root *durable.ConversationId) bool {
	scope := map[int64]bool{}
	var visit func(c int64)
	visit = func(c int64) {
		if scope[c] {
			return
		}
		scope[c] = true
		for _, t := range g.Tasks {
			if t.ConversationID == c && !t.Background {
				for _, child := range t.Conversations {
					visit(child)
				}
			}
		}
	}
	if root != nil {
		visit(int64(*root))
	} else {
		owned := map[int64]bool{}
		for _, t := range g.Tasks {
			for _, c := range t.Conversations {
				owned[c] = true
			}
		}
		for _, t := range g.Tasks {
			if !owned[t.ConversationID] {
				visit(t.ConversationID)
			}
		}
	}
	for _, t := range g.Tasks {
		if !t.Background && scope[t.ConversationID] {
			return true
		}
	}
	return false
}

// closeAware returns a context that Close cancels with harness.ErrClosed, and done releases it. It subscribes before it checks, so a
// Close between the two cannot leave the wait unrejected; a closed Session returns harness.ErrClosed at once.
func (s *coreSession) closeAware(ctx context.Context) (context.Context, func(), error) {
	ctx, cancel := context.WithCancelCause(ctx)
	unsubscribe := s.SubscribeClose(func() { cancel(harness.ErrClosed) })
	done := func() { unsubscribe(); cancel(nil) }
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		done()
		return ctx, nil, harness.ErrClosed
	}
	return ctx, done, nil
}

// closedOr reports a failure of a wait that Close ended as harness.ErrClosed. The wait's own context ending and the host closing both
// follow Close, so which error the wait saw first is a race; Pi rejects with closedError() in both.
func (s *coreSession) closedOr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(context.Cause(ctx), harness.ErrClosed) {
		return harness.ErrClosed
	}
	if errors.Is(err, sqlhost.ErrClosed) {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return harness.ErrClosed
		}
	}
	return err
}

// idle waits until the scope has no live non-background task. The wait for the next publication is registered before the
// graph is read, so a task that ends in between cannot be missed.
func (s *coreSession) idle(ctx context.Context, root *durable.ConversationId) error {
	// scheduler.ts waitForIdle: a closed Harness rejects at the call, and the seal rejects a wait that is pending.
	ctx, done, err := s.closeAware(ctx)
	if err != nil {
		return err
	}
	defer done()
	return s.closedOr(ctx, s.idleLoop(ctx, root))
}

func (s *coreSession) idleLoop(ctx context.Context, root *durable.ConversationId) error {
	for {
		exp, err := s.host.Expect(func(n abi.Notice) bool { p, ok := published(n); return ok && len(p.Tasks) > 0 })
		if err != nil {
			return err
		}
		raw, err := s.client.API(ctx, "taskGraph", nil)
		if err != nil {
			exp.Cancel()
			return err
		}
		var graph taskGraphState
		if err := json.Unmarshal(raw, &graph); err != nil {
			exp.Cancel()
			return err
		}
		if !graph.liveIn(root) {
			exp.Cancel()
			return nil
		}
		if _, err := exp.Wait(ctx); err != nil {
			return err
		}
	}
}

// Usage is the Session total: every conversation's pi.usage summed.
func (s *coreSession) Usage(ctx context.Context) (harness.UsageState, error) {
	raw, err := s.client.API(ctx, "usage", nil)
	var usage harness.UsageState
	if err != nil {
		return usage, err
	}
	return usage, json.Unmarshal(raw, &usage)
}

// submit admits one submission and waits for the core's answer, which names its ID.
func (s *coreSession) submit(ctx context.Context, conversationID durable.ConversationId, draft durable.SubmissionDraft) (durable.Submission, error) {
	release, err := s.admit()
	if err != nil {
		return nil, err
	}
	defer release()
	s.kick()
	rid := ""
	if draft.RequestId != nil {
		rid = *draft.RequestId
	} else {
		rid = requestID()
	}
	event := map[string]any{"conversationId": int64(conversationID), "type": string(draft.Type), "requestId": rid}
	if draft.WhenBusy != "" {
		event["whenBusy"] = string(draft.WhenBusy)
	}
	if draft.Type == durable.SubmissionTypeInput {
		event["content"] = draft.Content
		if draft.InputEntry != nil {
			event["entry"] = wireInputEntry(*draft.InputEntry)
		}
	} else if draft.Entry != nil {
		event["entry"] = wireSubmittedDraft(*draft.Entry)
	}
	raw, err := s.client.Submit(ctx, event)
	if err != nil {
		return nil, reviveError(err)
	}
	var admitted struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &admitted); err != nil {
		return nil, err
	}
	return &submission{s: s, id: durable.SubmissionId(admitted.ID)}, nil
}

// wireInputEntry is the {kind, data?} an input is placed as.
func wireInputEntry(entry durable.InputEntry) map[string]any {
	out := map[string]any{"kind": entry.Kind}
	if entry.Data != nil {
		out["data"] = entry.Data
	}
	return out
}

// wireSubmittedDraft is wireDraft for a submitted write, which keeps an empty model or edits list a caller passed: a present
// member makes the write affect model context, so it queues instead of being appended at once (inbox.ts isDisplayOnly).
func wireSubmittedDraft(d durable.EntryDraft) map[string]any {
	out := wireDraft(d)
	if d.Model != nil {
		out["model"] = d.Model
	}
	if d.Edits != nil {
		out["edits"] = d.Edits
	}
	return out
}

// reviveError maps the core's rejection of a submit to Pi's error classes where the harness package has them.
func reviveError(err error) error { return err }

type submission struct {
	s  *coreSession
	id durable.SubmissionId
}

func (b *submission) Id() durable.SubmissionId { return b.id }

func (b *submission) Status(ctx context.Context) (durable.SubmissionRecord, error) {
	record, err := b.s.submissionRecord(ctx, b.id)
	if err != nil {
		return durable.SubmissionRecord{}, err
	}
	if record == nil {
		return durable.SubmissionRecord{}, fmt.Errorf("submission %d is not in the store", b.id)
	}
	return *record, nil
}

func (b *submission) Wait(ctx context.Context) (durable.SettledSubmissionRecord, error) {
	// submissions.ts wait: a closed Harness rejects at the call, and close rejects a wait that is pending (#waiters.rejectAll(closedError())).
	ctx, done, err := b.s.closeAware(ctx)
	if err != nil {
		return durable.SubmissionRecord{}, err
	}
	defer done()
	record, err := b.wait(ctx)
	return record, b.s.closedOr(ctx, err)
}

func (b *submission) wait(ctx context.Context) (durable.SettledSubmissionRecord, error) {
	b.s.kick()
	for {
		exp, err := b.s.host.Expect(func(n abi.Notice) bool {
			p, ok := published(n)
			if !ok {
				return false
			}
			for _, sub := range p.Submissions {
				if sub.ID == int64(b.id) {
					return true
				}
			}
			return false
		})
		if err != nil {
			return durable.SubmissionRecord{}, err
		}
		record, err := b.s.submissionRecord(ctx, b.id)
		if err != nil {
			exp.Cancel()
			return durable.SubmissionRecord{}, err
		}
		if record != nil && record.Status != durable.SubmissionQueued && record.Status != durable.SubmissionPlaced {
			exp.Cancel()
			return *record, nil
		}
		if _, err := exp.Wait(ctx); err != nil {
			return durable.SubmissionRecord{}, err
		}
	}
}

func (b *submission) Abort(ctx context.Context) (durable.SubmissionAbortResult, error) {
	return b.s.AbortSubmission(ctx, b.id, nil)
}

type conversation struct {
	s  *coreSession
	id durable.ConversationId
}

var _ harness.Conversation = (*conversation)(nil)

func (c *conversation) Id() durable.ConversationId { return c.id }

func (c *conversation) Agent(ctx context.Context) (durable.Agent, error) { return c.s.agent(ctx, c.id) }

func (c *conversation) Configure(ctx context.Context, change harness.AgentChange) error {
	_, err := c.s.commit(ctx, nil, func(tx durable.Tx) (any, error) { return nil, harness.Configure(tx, c.id, change) })
	return err
}

func (c *conversation) Submit(ctx context.Context, draft durable.SubmissionDraft) (durable.Submission, error) {
	return c.s.submit(ctx, c.id, draft)
}

func (c *conversation) Reset(ctx context.Context, handoff *string) error {
	entry := durable.EntryDraft{Kind: "pi.reset", HeadSelf: true}
	if handoff != nil {
		entry.Model = []ai.Message{ai.UserMessage{Content: ai.UserText(*handoff), Timestamp: int64(c.s.now())}}
	}
	_, err := c.s.submit(ctx, c.id, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &entry})
	return err
}

func (c *conversation) Compact(ctx context.Context, instructions *string) (durable.TaskId, error) {
	c.s.kick()
	// harness.ts compact: createCompaction also lists the task in pi.live.compactions, which its events, retry status and
	// failure record read.
	input := harness.CompactionInput{Reason: durable.CompactionManual, Instructions: instructions}
	conversationID := c.id
	result, err := c.s.commit(ctx, nil, func(tx durable.Tx) (any, error) {
		return harness.CreateCompaction(tx, conversationID, input, nil)
	})
	if err != nil {
		return 0, err
	}
	return result.(durable.TaskId), nil
}

func (c *conversation) Commit(ctx context.Context, change func(tx durable.Tx) (any, error)) (any, error) {
	return c.s.commit(ctx, map[string]any{"conversationId": c.id}, change)
}

func (c *conversation) Context(ctx context.Context, options *durable.ContextOptions) (durable.ContextView, error) {
	query := map[string]any{"conversationId": c.id}
	if options != nil && options.At != nil {
		query["at"] = *options.At
	}
	raw, err := c.s.client.API(ctx, "context", query)
	if err != nil {
		return durable.ContextView{}, err
	}
	return decodeContextView(raw)
}

func (c *conversation) Entries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	query.ConversationId = c.id
	result, err := c.s.commit(ctx, nil, func(tx durable.Tx) (any, error) { return tx.ScanEntries(query, limit, cursor) })
	if err != nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
	}
	return result.(durable.Page[durable.EntryRecord, durable.Cursor]), nil
}

func (c *conversation) Fork(ctx context.Context, at durable.EntryId, options harness.ConversationCreateOptions) (harness.Conversation, error) {
	id, err := c.s.create(ctx, func(tx *txProxy) (durable.ConversationRecord, error) {
		return tx.ForkConversation(c.id, at, durable.CreateConversationOptions{Ownership: options.Ownership})
	}, options, nil)
	if err != nil {
		return nil, err
	}
	return &conversation{s: c.s, id: id}, nil
}

func (c *conversation) Abort(ctx context.Context, options *durable.ConversationAbortOptions) error {
	c.s.kick()
	body := map[string]any{"conversationId": c.id}
	if options != nil && options.Background {
		body["background"] = true
	}
	if _, err := c.s.client.Abort(ctx, body); err != nil {
		return err
	}
	return c.s.idle(ctx, &c.id)
}

func (c *conversation) WaitForIdle(ctx context.Context) error {
	c.s.kick()
	return c.s.idle(ctx, &c.id)
}

var _ harness.Harness = (*coreSession)(nil)

// ConversationHandle returns the handle of a conversation without checking that it exists.
func (s *coreSession) ConversationHandle(id durable.ConversationId) harness.Conversation {
	return &conversation{s: s, id: id}
}
