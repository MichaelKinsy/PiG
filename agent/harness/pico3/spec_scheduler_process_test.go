package pico3

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Upstream: packages/agent/test/harness/pico3/spec-scheduler-process.test.ts.
func TestSchedulerTerminalFailureSettlesSuccessorGroups(t *testing.T) {
	gate := &testGate{}
	models := newFake(fakeOptions{respond: func([]JsonObject, int) fakeResponse { return errorResponse("provider down") }, gate: gate})
	env := openEnv(t, openOptions{models: models, root: &RootSpec{Rewindable: JsonObject{"model": testModel}, Sticky: JsonObject{"retry": JsonObject{"enabled": false, "maxRetries": 0, "baseDelayMs": 1}, "followUpMode": "all", "steeringMode": "all"}}})
	first := env.send(env.root, "first")
	gate.Arrivals(t, 1)
	followA := env.send(env.root, "follow-a")
	steer := must(env.root.Send(bg, SendInput{Content: "steer", WhenBusy: "steer"}))
	followB := env.send(env.root, "follow-b")
	passive := must(env.root.Write(bg, NewEntry{Kind: "passive.note", Data: JsonObject{"durable": true}}))
	check(t, env.root.Config().Reset(bg, []string{"model"}))
	gate.Open()
	for _, input := range []*InputHandle{first, followA, steer, followB} {
		settled := env.wait(input)
		equal(t, settled.Status, InputUnanswered, "every trigger settles")
		equal(t, settled.Reason, "failed", "trigger failure")
	}
	equal(t, env.input(passive).Status, InputDone, "passive write settles")
	equal(t, arr(env.sticky(), "inbox"), []any{}, "inbox drained")
	var reasons []string
	for _, task := range env.tasks() {
		if task.Kind == "pi.generation" {
			reasons = append(reasons, str(failureOf(&task), "reason"))
		}
	}
	equal(t, reasons, []string{"provider", "no_model"}, "queued triggers form one successor group")
	equal(t, models.Calls(), 1, "one provider call")
}

func TestSchedulerDisplayErrorsExcludedFromNextRequest(t *testing.T) {
	models := newFake(fakeOptions{respond: func(messages []JsonObject, call int) fakeResponse {
		if call == 0 {
			return errorResponse("fatal")
		}
		return echoScript(messages, call)
	}})
	env := openEnv(t, openOptions{models: models, root: &RootSpec{Rewindable: JsonObject{"model": testModel}, Sticky: JsonObject{"retry": JsonObject{"enabled": false, "maxRetries": 0, "baseDelayMs": 1}}}})
	equal(t, env.wait(env.send(env.root, "first")).Reason, "failed", "first fails")
	var display *Entry
	for _, entry := range env.entries() {
		if entry.Kind == "pi.assistant" && str(asObject(entry.Data), "reason") == "error" {
			display = &entry
		}
	}
	if display == nil || display.Model != nil || asObject(display.Data)["display"] == nil {
		t.Fatalf("error must remain display-only: %+v", display)
	}
	equal(t, env.wait(env.send(env.root, "second")).Status, InputDone, "second succeeds")
	requests := models.Requests()
	equal(t, len(requests), 2, "request count")
	for _, message := range requests[1] {
		if str(message, "role") == "assistant" && (str(message, "stopReason") == "error" || str(message, "stopReason") == "aborted") {
			t.Fatalf("poisoned message: %v", message)
		}
	}
	if strings.Contains(string(mustJSON(requests[1])), "fatal") {
		t.Fatal("display error leaked into provider request")
	}
}

func TestSchedulerGenerationSingleton(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	first := env.send(env.root, "one")
	gate.Arrivals(t, 1)
	var second, third *InputHandle
	var secondErr, thirdErr error
	var wg sync.WaitGroup
	wg.Go(func() { second, secondErr = env.root.Send(bg, SendInput{Content: "two"}) })
	wg.Go(func() { third, thirdErr = env.root.Send(bg, SendInput{Content: "three"}) })
	wg.Wait()
	check(t, secondErr)
	check(t, thirdErr)
	var live []Task
	for _, task := range env.liveTasks() {
		if task.Kind == "pi.generation" {
			live = append(live, task)
		}
	}
	equal(t, len(live), 1, "singleton concurrent admission")
	equal(t, arr(asObject(live[0].Input), "inputs"), []any{first.Id}, "active input group")
	gate.Open()
	for _, input := range []*InputHandle{first, second, third} {
		env.wait(input)
	}
	env.idle()
	retained := 0
	for _, task := range env.tasks() {
		if task.Kind == "pi.generation" {
			retained++
			equal(t, task.Status, TaskTerminal, "retained generation")
		}
	}
	equal(t, retained, 3, "three serialized generations")
	equal(t, env.wait(env.send(env.root, "four")).Status, InputDone, "new generation after retention")
}

func TestSchedulerCollapseSingleton(t *testing.T) {
	gate := &testGate{}
	models := newFake(fakeOptions{respond: func(messages []JsonObject, call int) fakeResponse {
		for _, message := range messages {
			if str(message, "role") == "user" && strings.Contains(str(message, "content"), "Respond with the summary only") {
				return textResponse("summary")
			}
		}
		return echoScript(messages, call)
	}})
	env := openEnv(t, openOptions{models: models, root: &RootSpec{Rewindable: JsonObject{"model": testModel, "keepRecent": 1}}, hooks: &hooksByKind{collapse: &CollapseHooks{BeforeCollapse: func(ctx context.Context, _ string, _ Id, _ []Entry, _ HookApi) (*CollapseDecision, error) {
		return nil, gate.Wait(ctx)
	}}}})
	for _, prompt := range []string{"one", "two", "three"} {
		env.wait(env.send(env.root, prompt))
	}
	first := must(env.root.Collapse(bg, nil))
	gate.Arrivals(t, 1)
	_, err := env.root.Collapse(bg, nil)
	if _, ok := errors.AsType[*CollapseInProgress](err); !ok {
		t.Fatalf("overlap: %v", err)
	}
	gate.Open()
	equal(t, env.untilTerminal(first).Status, TaskTerminal, "collapse retained")
	equal(t, must(env.h.GetTask(bg, first)).Id, first, "retained id")
}

type schedulerProcessHost struct {
	start  func(context.Context, string, ProcessSpec) error
	status func(context.Context, string) (ProcessStatus, error)
	kill   func(context.Context, string, string) error
}

func (h schedulerProcessHost) Start(ctx context.Context, key string, spec ProcessSpec) error {
	if h.start != nil {
		return h.start(ctx, key, spec)
	}
	return nil
}
func (h schedulerProcessHost) Status(ctx context.Context, key string) (ProcessStatus, error) {
	if h.status != nil {
		return h.status(ctx, key)
	}
	return ProcessStatus{Status: "running"}, nil
}
func (h schedulerProcessHost) Kill(ctx context.Context, key, signal string) error {
	if h.kill != nil {
		return h.kill(ctx, key, signal)
	}
	return nil
}

func TestSchedulerJobSpawnFailure(t *testing.T) {
	env := openEnv(t, openOptions{processHost: schedulerProcessHost{start: func(context.Context, string, ProcessSpec) error { return errors.New("spawn denied") }}})
	ref := createTestTask(t, env, Kinds.Job, JsonObject{"command": "x", "args": []any{}, "cwd": "/", "notify": true, "rerun": false})
	task := env.untilTerminal(ref.Id)
	equal(t, task.Outcome.Status, OutcomeFailed, "spawn outcome")
	equal(t, task.Outcome.Failure, JsonObject{"reason": "spawn", "detail": "Error: spawn denied"}, "declared failure")
	equal(t, arr(env.sticky(), "inbox"), []any{}, "no poisoned inbox")
	var notice *Entry
	for _, entry := range env.entries() {
		if entry.Kind == "pi.notice" {
			notice = &entry
		}
	}
	if !strings.Contains(contentOf(notice), "failed to start") {
		t.Fatalf("notice: %s", contentOf(notice))
	}
}

func TestSchedulerJobHostStatusFailure(t *testing.T) {
	var starts atomic.Int64
	env := openEnv(t, openOptions{processHost: schedulerProcessHost{start: func(context.Context, string, ProcessSpec) error { starts.Add(1); return nil }, status: func(context.Context, string) (ProcessStatus, error) {
		return ProcessStatus{}, errors.New("host unavailable")
	}}})
	task := env.untilTerminal(createTestTask(t, env, Kinds.Job, JsonObject{"command": "x", "args": []any{}, "cwd": "/", "notify": false, "rerun": true}).Id)
	equal(t, starts.Load(), int64(1), "status error does not rerun")
	equal(t, task.Outcome.Status, OutcomeFailed, "status failure")
	equal(t, task.Outcome.Failure, JsonObject{"reason": "interrupted", "detail": "host status failed: Error: host unavailable"}, "interrupted detail")
}

func TestSchedulerJobAbortGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type signalAt struct {
			Signal string
			At     time.Time
		}
		kills := make(chan signalAt, 4)
		started := make(chan struct{})
		env := openEnv(t, openOptions{processHost: schedulerProcessHost{start: func(context.Context, string, ProcessSpec) error { close(started); return nil }, kill: func(_ context.Context, _, signal string) error { kills <- signalAt{signal, time.Now()}; return nil }}})
		ref := createTestTask(t, env, Kinds.Job, JsonObject{"command": "x", "args": []any{}, "cwd": "/", "notify": false, "rerun": false})
		<-started
		synctest.Wait()
		must(env.h.AbortTask(bg, ref.Id))
		term := <-kills
		equal(t, term.Signal, "SIGTERM", "first signal")
		time.Sleep(4999 * time.Millisecond)
		synctest.Wait()
		equal(t, len(kills), 0, "no KILL before grace")
		time.Sleep(time.Millisecond)
		task := must(env.h.WaitForTask(bg, ref.Id))
		kill := <-kills
		equal(t, kill.Signal, "SIGKILL", "second signal")
		equal(t, kill.At.Sub(term.At), 5*time.Second, "fixed grace")
		equal(t, task.Outcome.Status, OutcomeAborted, "abort outcome")
		equal(t, task.Outcome.Result, JsonObject{"killed": true}, "abort result")
		env.close()
	})
}

func TestSchedulerSuspendRecoversDurableInvocation(t *testing.T) {
	gate := &testGate{}
	tool := newTool("recoverable", toolOptions{replay: "safe", gate: gate})
	env := openEnv(t, openOptions{backend: "jsonl", tools: []*ToolDeclaration{tool.ToolDeclaration}})
	input := env.send(env.root, "tool:recoverable")
	running := env.untilPhase("pi.tool", "started")
	gate.Arrivals(t, 1)
	check(t, env.h.Suspend(bg))
	env.closed = true
	replacement := newTool("recoverable", toolOptions{replay: "safe"})
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, tools: []*ToolDeclaration{replacement.ToolDeclaration}})
	next.idle()
	equal(t, replacement.calls.Load(), int64(1), "replacement invoked once")
	equal(t, must(next.h.GetTask(bg, running.Id)).Outcome.Status, OutcomeCompleted, "recovered task")
	equal(t, next.input(input.Id).Status, InputDone, "input completion")
}

func TestSchedulerHoldReplacementRunsPendingRecord(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	makeKind := func(label string) *Kind {
		return &Kind{Name: "spec.held-kind", Initial: func(context.Context, Task, *Runtime) (Step, error) {
			mu.Lock()
			ran = append(ran, label)
			mu.Unlock()
			return Step{Next: Checkpoint{"phase": "done"}}, nil
		}, Phases: map[string]PhaseHandler{"done": func(context.Context, Task, *Runtime) (Step, error) { return done(Completed(label)), nil }}}
	}
	env := openEnv(t, openOptions{})
	equal(t, env.h.Quiescent(), true, "initially quiescent")
	release := must(env.h.Hold())
	old := makeKind("old")
	off := must(env.h.RegisterTaskKind(old))
	ref := createTestTask(t, env, old, nil)
	must(env.root.Write(bg, NewEntry{Kind: "commit.while.held"}))
	equal(t, must(env.h.GetTask(bg, ref.Id)).Status, TaskPending, "held record")
	mu.Lock()
	equal(t, len(ran), 0, "dispatch waits")
	mu.Unlock()
	off()
	must(env.h.RegisterTaskKind(makeKind("replacement")))
	release()
	task := env.untilTerminal(ref.Id)
	equal(t, task.Outcome.Status, OutcomeCompleted, "replacement outcome")
	equal(t, task.Outcome.Result, "replacement", "replacement code")
	mu.Lock()
	equal(t, slices.Clone(ran), []string{"replacement"}, "only replacement runs")
	mu.Unlock()
	equal(t, env.h.Quiescent(), true, "terminal quiescence")
}

func TestSchedulerSleepingJobIsNotQuiescent(t *testing.T) {
	env := openEnv(t, openOptions{processHost: schedulerProcessHost{}})
	ref := createTestTask(t, env, Kinds.Job, JsonObject{"command": "x", "args": []any{}, "cwd": "/", "notify": false, "rerun": false})
	env.untilPhase("pi.job", "running")
	equal(t, env.h.Quiescent(), false, "sleeping invocation remains live")
	must(env.h.AbortTask(bg, ref.Id))
}
