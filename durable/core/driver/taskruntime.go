// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// taskRuntimes runs the phase and abort handlers of the custom tasks a registry holds, in answer to the core's phase effects
// (ABI effect 8). One runtime serves every phase of one invocation of a task (scheduler.ts: one Runtime per invocation); the
// core ends the invocation by cancelling its latest phase effect, which the host sees through sqlhost.Options.OnCancel. This is
// the native counterpart of the JS host's customTaskRunner (facade/taskruntime.ts) over the same core operations.
type taskRuntimes struct {
	s        *coreSession
	mu       sync.Mutex
	live     map[string]*liveInvocation
	byEffect map[uint32]string
}

type liveInvocation struct {
	key     string
	rt      *taskRuntime
	effects map[uint32]struct{}
	active  int
	ending  bool
}

func newTaskRuntimes(s *coreSession) *taskRuntimes {
	return &taskRuntimes{s: s, live: map[string]*liveInvocation{}, byEffect: map[uint32]string{}}
}

func (t *taskRuntimes) handler() sqlhost.Handler { return (&Phases{Run: t.run}).Handler() }

// definition is the task definition the core reserved the task under, even if a reload has replaced or removed it since.
func (t *taskRuntimes) definition(effect PhaseEffect) (*durable.AnyTaskDefinition, error) {
	var task durable.AnyTask
	if effect.Handler != nil {
		if item, ok := t.s.table.at(*effect.Handler); ok {
			task, _ = item.(durable.AnyTask)
		}
	} else {
		task = t.s.cfg.Harness.Registry.Snapshot().Task(effect.Kind)
	}
	if task.Definition == nil {
		return nil, fmt.Errorf("Task %s is not registered", effect.Kind)
	}
	return task.AnyDefinition(), nil
}

func (t *taskRuntimes) enter(effect PhaseEffect) (*liveInvocation, error) {
	var record struct {
		ConversationID durable.ConversationId `json:"conversationId"`
	}
	if err := json.Unmarshal(effect.Record, &record); err != nil {
		return nil, fmt.Errorf("phase record: %w", err)
	}
	key := fmt.Sprintf("%d:%t", effect.TaskID, effect.Abort)
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.live[key]
	if entry == nil {
		ctx, cancel := context.WithCancel(context.Background())
		entry = &liveInvocation{key: key, effects: map[uint32]struct{}{}, rt: &taskRuntime{
			invocation: &invocation{s: t.s, task: durable.TaskId(effect.TaskID), conversation: record.ConversationID},
			ctx:        ctx, cancel: cancel, kind: effect.Kind, extension: effect.Extension, invocationID: effect.Invocation,
		}}
		t.live[key] = entry
	}
	entry.effects[effect.EffectID] = struct{}{}
	t.byEffect[effect.EffectID] = key
	entry.active++
	// Each phase sees the registry as it is at its start and resolves its agent anew.
	entry.rt.beginPhase(t.s.cfg.Harness.Registry.Snapshot())
	return entry, nil
}

func (t *taskRuntimes) leave(entry *liveInvocation) {
	t.mu.Lock()
	entry.active--
	finish := entry.active == 0 && entry.ending
	t.mu.Unlock()
	if finish {
		t.finish(entry)
	}
}

func (t *taskRuntimes) finish(entry *liveInvocation) {
	t.mu.Lock()
	if t.live[entry.key] == entry {
		delete(t.live, entry.key)
	}
	for id := range entry.effects {
		delete(t.byEffect, id)
	}
	t.mu.Unlock()
	entry.rt.end()
}

// ended is the core cancelling effect id: when it is an invocation's latest phase effect, the invocation has ended. The
// invocation's signal fires now; its runtime ends once the running handler returns, so a wait the end cancels rejects with
// the invocation-ended error.
func (t *taskRuntimes) ended(id uint32) {
	t.mu.Lock()
	entry := t.live[t.byEffect[id]]
	if entry == nil {
		t.mu.Unlock()
		return
	}
	finish := entry.active == 0
	entry.ending = !finish
	t.mu.Unlock()
	if finish {
		// No handler runs: the runtime's watches stop (reason "stopped") before its signal fires.
		t.finish(entry)
		return
	}
	entry.rt.cancel()
}

// closeAll ends every live invocation: the Harness is closing.
func (t *taskRuntimes) closeAll() {
	t.mu.Lock()
	entries := make([]*liveInvocation, 0, len(t.live))
	for _, entry := range t.live {
		entries = append(entries, entry)
	}
	t.mu.Unlock()
	for _, entry := range entries {
		entry.rt.cancel()
		t.finish(entry)
	}
}

func (t *taskRuntimes) run(ctx context.Context, effect PhaseEffect) error {
	def, err := t.definition(effect)
	if err != nil {
		return err
	}
	entry, err := t.enter(effect)
	if err != nil {
		return err
	}
	defer t.leave(entry)
	// The invocation's signal follows the phase effect the core cancels when the invocation aborts.
	stop := context.AfterFunc(ctx, entry.rt.cancel)
	defer stop()
	var record durable.ErasedRunningTask
	if err := json.Unmarshal(effect.Record, &record); err != nil {
		return fmt.Errorf("phase record: %w", err)
	}
	if effect.Abort {
		if def.Abort == nil {
			return fmt.Errorf("Task %s has no abort handler", effect.Kind)
		}
		return def.Abort(entry.rt.ctx, record, entry.rt)
	}
	var handler durable.ErasedPhaseHandler
	if effect.Phase != nil {
		handler = def.Phases[*effect.Phase]
	}
	if handler == nil {
		phase := "<none>"
		if effect.Phase != nil {
			phase = *effect.Phase
		}
		return fmt.Errorf("Task %s has no phase %s", effect.Kind, phase)
	}
	return handler(entry.rt.ctx, record, entry.rt)
}

// taskRuntime is the durable.TaskRuntime a custom task's handler receives (types.ts TaskRuntime). Its reads, waits and commits
// fail with "Task N invocation has ended" once the core ended the invocation.
type taskRuntime struct {
	*invocation
	ctx    context.Context
	cancel context.CancelFunc
	kind   string
	// extension is the extension that installed the definition this invocation runs; empty for a built-in task.
	extension    string
	invocationID int64
	done         atomic.Bool

	mu       sync.Mutex
	snapshot durable.RegistrySnapshot
	agent    *resolvedAgent
	onEnd    []func()
	// timers holds the disarm of each armed timer until it fires or is disarmed, so a long phase keeps only its live timers
	// (scheduler.ts setTimer deletes on fire and on disarm).
	timers    map[int]func()
	nextTimer int
}

// resolvedAgent is the phase's one agent resolution, started at first use with the invocation's context (scheduler.ts #runtime agent).
// A caller waits on done under its own context (awaitWithContext); a resolution nobody waits for still finishes.
type resolvedAgent struct {
	agent durable.Agent
	err   error
	done  chan struct{}
}

var _ durable.ErasedTaskRuntime = (*taskRuntime)(nil)

func (r *taskRuntime) beginPhase(snapshot durable.RegistrySnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshot = snapshot
	r.agent = nil
}

func (r *taskRuntime) end() {
	r.done.Store(true)
	r.mu.Lock()
	callbacks := r.onEnd
	r.onEnd = nil
	timers := slices.Collect(maps.Values(r.timers))
	clear(r.timers)
	r.mu.Unlock()
	for _, disarm := range timers {
		disarm()
	}
	// The watches stop first, so they end "stopped" rather than "cancelled" by the signal that follows.
	for _, callback := range callbacks {
		callback()
	}
	r.cancel()
}

// onEnded runs callback when the invocation ends, at once when it already has.
func (r *taskRuntime) onEnded(callback func()) {
	r.mu.Lock()
	if r.done.Load() {
		r.mu.Unlock()
		callback()
		return
	}
	r.onEnd = append(r.onEnd, callback)
	r.mu.Unlock()
}

func (r *taskRuntime) check() error {
	if r.done.Load() {
		return fmt.Errorf("Task %d invocation has ended", r.task)
	}
	return nil
}

// ended maps a failure of a read the invocation's end cancelled to the invocation-ended error.
func (r *taskRuntime) ended(err error) error {
	if err != nil {
		if checked := r.check(); checked != nil {
			return checked
		}
	}
	return err
}

func (r *taskRuntime) Signal() context.Context { return r.ctx }

func (r *taskRuntime) Registry() durable.RegistrySnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshot
}

func (r *taskRuntime) Agent(ctx context.Context) (durable.Agent, error) {
	if err := r.check(); err != nil {
		return durable.Agent{}, err
	}
	r.mu.Lock()
	resolution := r.agent
	if resolution == nil {
		// Resolved with the invocation's context, not the first caller's: a caller that stops waiting must not fail the phase's resolution.
		resolution = &resolvedAgent{done: make(chan struct{})}
		r.agent = resolution
		go func() {
			defer close(resolution.done)
			// A resolution that throws (a settings getter) rejects the waiting callers, as a rejected Promise does.
			defer func() {
				if recovered := recover(); recovered != nil {
					resolution.err = fmt.Errorf("%v", recovered)
				}
			}()
			resolution.agent, resolution.err = r.s.agent(r.ctx, r.conversation)
		}()
	}
	r.mu.Unlock()
	// awaitWithContext (chord context/index.ts): a context that is already cancelled rejects with its reason at once.
	if err := ctx.Err(); err != nil {
		return durable.Agent{}, context.Cause(ctx)
	}
	select {
	case <-resolution.done:
		return resolution.agent, resolution.err
	case <-ctx.Done():
		return durable.Agent{}, context.Cause(ctx)
	}
}

func (r *taskRuntime) Settings() durable.Settings { return r.s.resolveSettings() }

func (r *taskRuntime) Models() durable.Models { return r.s.cfg.Harness.CurrentModels() }

func (r *taskRuntime) Env(ctx context.Context) (env.ExecutionEnv, error) {
	if r.s.cfg.Harness.Env == nil {
		return nil, nil
	}
	return r.s.cfg.Harness.Env(ctx, harness.EnvTarget{ConversationId: r.conversation, Read: r})
}

// Now and Report panic once the invocation ended, as upstream's runtime.now() and report() throw (the in-process runtime panics too).
func (r *taskRuntime) Now() float64 {
	if err := r.check(); err != nil {
		panic(err)
	}
	return r.s.now()
}

func (r *taskRuntime) Report(err error, from ...durable.ReportFrom) {
	if checked := r.check(); checked != nil {
		panic(checked)
	}
	task := &durable.ReportTask{Id: r.task, Kind: r.kind, ConversationId: r.conversation}
	if len(from) == 0 {
		r.s.report(err, r.about(durable.ReportTaskError))
		return
	}
	if from[0].Type == durable.ReportFromExtension {
		r.s.report(err, durable.ReportInfo{Type: durable.ReportExtensionError, Task: task, Extension: from[0].Extension})
		return
	}
	r.s.report(err, durable.ReportInfo{Type: durable.ReportHostError, Callback: from[0].Callback, Task: task})
}

// about is the report info of a failure of this invocation's task: the task, and the extension that installed the definition
// the invocation runs (scheduler.ts #aboutTask).
func (r *taskRuntime) about(reportType durable.ReportType) durable.ReportInfo {
	return durable.ReportInfo{Type: reportType, Task: &durable.ReportTask{Id: r.task, Kind: r.kind, ConversationId: r.conversation}, Extension: r.extension}
}

// Hooks returns the handlers of this task's name from the extensions its conversation selects, in extension order. A throw is
// reported and the next handler runs, unless the invocation was signalled.
func (r *taskRuntime) Hooks() durable.HookRunner[any] { return taskHooks{r} }

type taskHooks struct{ r *taskRuntime }

func (h taskHooks) Each(name string, invoke func(handlers any) error) error {
	// scheduler.ts:1132-1137 agent() takes no caller context: it rejects only once the invocation has ended, not when it is signalled, so the handlers of a
	// signalled invocation still run and their error propagates (scheduler.ts:1141).
	agent, err := h.r.Agent(context.WithoutCancel(h.r.ctx))
	if err != nil {
		return err
	}
	for _, hook := range harness.AgentHooks(agent, h.r.kind) {
		if !hasHook(hook.Handlers, name) {
			continue
		}
		if err := callDefinition(func() error { return invoke(hook.Handlers) }); err != nil {
			if h.r.ctx.Err() != nil {
				return err
			}
			h.r.s.report(err, durable.ReportInfo{Type: durable.ReportExtensionError, Extension: hook.Extension, Task: &durable.ReportTask{Id: h.r.task, Kind: h.r.kind, ConversationId: h.r.conversation}})
		}
	}
	return nil
}

func hasHook(handlers any, name string) bool {
	return slices.Contains(hookNames(handlers), name)
}

func (r *taskRuntime) SnapshotErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.JsonObject, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	value, err := r.invocation.SnapshotErased(ctx, token, args...)
	return value, r.ended(err)
}

func (r *taskRuntime) SnapshotAsOfErased(ctx context.Context, token durable.AnyDocToken, at durable.EntryId, args ...any) (durable.JsonObject, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	value, err := r.invocation.SnapshotAsOfErased(ctx, token, at, args...)
	return value, r.ended(err)
}

// WatchDocErased returns a watch that stops when the invocation ends.
func (r *taskRuntime) WatchDocErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.WatchHandle[durable.JsonObject], error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	watch, err := r.invocation.WatchDocErased(ctx, token, args...)
	if err != nil || watch == nil {
		return watch, err
	}
	r.onEnded(func() { _, _ = watch.Stop() })
	return watch, r.check()
}

func (r *taskRuntime) Memo(ctx context.Context, name string) (durable.JsonValue, bool, error) {
	if err := r.check(); err != nil {
		return nil, false, err
	}
	value, ok, err := r.invocation.Memo(ctx, name)
	return value, ok, r.ended(err)
}

func (r *taskRuntime) MemoCandidate(ctx context.Context, name string, candidate durable.JsonValue) (durable.JsonValue, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	value, err := r.invocation.MemoCandidate(ctx, name, candidate)
	return value, r.ended(err)
}

func (r *taskRuntime) GetTask(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	record, err := r.invocation.GetTask(ctx, id)
	return record, r.ended(err)
}

func (r *taskRuntime) WaitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error) {
	var settled durable.SettledTask[durable.JsonValue]
	if err := r.check(); err != nil {
		return settled, err
	}
	raw, err := r.s.client.API(ctx, "waitForTask", map[string]any{"taskId": id, "by": r.task})
	if err != nil {
		return settled, r.ended(err)
	}
	return settled, json.Unmarshal(raw, &settled)
}

// AbortOwned aborts a task this task owns and returns once it is terminal (scheduler.ts abortOwned).
func (r *taskRuntime) AbortOwned(ctx context.Context, id durable.TaskId) error {
	if err := r.check(); err != nil {
		return err
	}
	record, err := r.invocation.GetTask(ctx, id)
	if err != nil {
		return r.ended(err)
	}
	if record == nil || record.Owner == nil || *record.Owner != r.task {
		return fmt.Errorf("Task %d is not owned by task %d", id, r.task)
	}
	if record.State.Status == durable.TaskTerminal {
		return nil
	}
	if _, err := r.s.abortTask(ctx, id, true); err != nil {
		return r.ended(err)
	}
	_, err = r.WaitForTask(ctx, id)
	return err
}

// OwnedTasks lists the live tasks this task owns directly, in ID order, also those an earlier invocation created (scheduler.ts
// ownedTasks): the core serves them from its ownership index.
func (r *taskRuntime) OwnedTasks(ctx context.Context) ([]durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	raw, err := r.s.client.API(ctx, "ownedTasks", map[string]any{"by": r.task})
	if err != nil {
		return nil, r.ended(err)
	}
	var records []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (r *taskRuntime) Outcomes(ctx context.Context, ids []durable.TaskId) ([]durable.TaskOutcome[durable.JsonValue], error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	raw, err := r.s.client.API(ctx, "outcomes", map[string]any{"taskIds": ids, "by": r.task})
	if err != nil {
		return nil, r.ended(err)
	}
	var outcomes []durable.TaskOutcome[durable.JsonValue]
	return outcomes, json.Unmarshal(raw, &outcomes)
}

func (r *taskRuntime) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryRecord, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	raw, err := r.s.client.API(ctx, "entry", map[string]any{"conversationId": r.conversation, "entryId": id})
	if err != nil {
		return nil, r.ended(err)
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var record durable.EntryRecord
	return &record, json.Unmarshal(raw, &record)
}

func (r *taskRuntime) Context(ctx context.Context, conversationId durable.ConversationId, options *durable.ContextOptions) (durable.ContextView, error) {
	if err := r.check(); err != nil {
		return durable.ContextView{}, err
	}
	args := map[string]any{"taskId": r.task, "conversationId": conversationId}
	if options != nil && options.At != nil {
		args["at"] = *options.At
	}
	raw, err := r.s.client.API(ctx, "context", args)
	if err != nil {
		return durable.ContextView{}, r.ended(err)
	}
	return decodeContextView(raw)
}

// SleepUntil commits a sleep: the core stores it and answers once it is committed; the task then holds no invocation.
func (r *taskRuntime) SleepUntil(ctx context.Context, until float64, checkpoint durable.JsonValue) error {
	if err := r.check(); err != nil {
		return err
	}
	// A time JSON cannot carry never reaches the core: the message is the one the core gives for a negative time.
	if math.IsNaN(until) || math.IsInf(until, 0) {
		shown := "NaN"
		switch {
		case math.IsInf(until, 1):
			shown = "Infinity"
		case math.IsInf(until, -1):
			shown = "-Infinity"
		}
		return fmt.Errorf("Task %d cannot sleep until %s: not a time in milliseconds", r.task, shown)
	}
	_, err := r.s.client.API(ctx, "sleepUntil", map[string]any{"taskId": r.task, "until": until, "checkpoint": checkpoint})
	return r.ended(err)
}

// SetTimer calls callback once after about delayMs on the Harness's timer; the invocation's end disarms it, and an ended
// invocation arms nothing (scheduler.ts setTimer).
func (r *taskRuntime) SetTimer(callback func(), delayMs float64) func() {
	if r.done.Load() {
		return func() {}
	}
	r.mu.Lock()
	r.nextTimer++
	key := r.nextTimer
	r.mu.Unlock()
	fired := false
	disarm := r.s.timers.Foreground(func() {
		r.mu.Lock()
		fired = true
		delete(r.timers, key)
		r.mu.Unlock()
		if !r.done.Load() {
			callback()
		}
	}, delayMs)
	r.mu.Lock()
	if r.done.Load() {
		r.mu.Unlock()
		disarm()
		return func() {}
	}
	if !fired {
		if r.timers == nil {
			r.timers = make(map[int]func())
		}
		r.timers[key] = disarm
	}
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		_, live := r.timers[key]
		delete(r.timers, key)
		r.mu.Unlock()
		if live {
			disarm()
		}
	}
}

// Conversation returns an invocation-bound handle: its operations fail once the invocation ends.
func (r *taskRuntime) Conversation(ctx context.Context, id durable.ConversationId) (durable.ConversationHandle, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	handle, err := r.invocation.Conversation(ctx, id)
	if err != nil || handle == nil {
		return handle, err
	}
	return boundHandle{ConversationHandle: handle, r: r}, nil
}

type boundHandle struct {
	durable.ConversationHandle
	r *taskRuntime
}

func (h boundHandle) Submit(ctx context.Context, draft durable.InputSubmissionDraft) (durable.Submission, error) {
	if err := h.r.check(); err != nil {
		return nil, err
	}
	submission, err := h.ConversationHandle.Submit(ctx, draft)
	return submission, h.r.ended(err)
}

func (h boundHandle) Abort(ctx context.Context, options *durable.ConversationAbortOptions) error {
	if err := h.r.check(); err != nil {
		return err
	}
	return h.r.ended(h.ConversationHandle.Abort(ctx, options))
}

func (h boundHandle) WaitForIdle(ctx context.Context) error {
	if err := h.r.check(); err != nil {
		return err
	}
	return h.r.ended(h.ConversationHandle.WaitForIdle(ctx))
}

// Commit commits on the Session line after rereading the task: the transaction begins for this invocation, so the core rejects
// it once the invocation ended or the task carries an abort mark, hands the task record it read (ready.current) to change, and
// takes the returned state as the task's next state in the same commit (nil leaves it).
func (r *taskRuntime) Commit(ctx context.Context, change func(tx durable.Tx, current durable.ErasedRunningTask) (*durable.NextTaskState[durable.JsonValue, durable.JsonValue], error)) error {
	if err := r.check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := r.s.admit()
	if err != nil {
		return err
	}
	defer release()
	holding := false
	defer func() {
		if holding {
			r.s.watches.leaveCommit(ctx)
		}
	}()
	tx, err := r.s.client.Begin(ctx, map[string]any{"invocation": r.invocationID, "conversationId": r.conversation})
	if err != nil {
		return r.ended(err)
	}
	r.s.watches.enterCommit(ctx)
	holding = true
	proxy := newTxProxy(ctx, tx)
	var next *durable.NextTaskState[durable.JsonValue, durable.JsonValue]
	err = callDefinition(func() error {
		var current durable.ErasedRunningTask
		if len(tx.Current) > 0 {
			if err := json.Unmarshal(tx.Current, &current); err != nil {
				return fmt.Errorf("task record: %w", err)
			}
		}
		var err error
		next, err = change(proxy, current)
		return err
	})
	if err == nil {
		err = proxy.flush()
	}
	if err != nil {
		proxy.discard()
		_ = tx.Abort(context.WithoutCancel(ctx), err)
		return r.endedOr(err)
	}
	var state any
	if next != nil {
		state = next
	}
	return r.endedOr(tx.End(ctx, state))
}

// endedOr is the invocation-ended error when the failure came with the invocation's end, else err.
func (r *taskRuntime) endedOr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return r.ended(err)
}
