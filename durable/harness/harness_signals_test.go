// Ports packages/durable/test/harness-signals.test.ts (Pi 473515f9c): signals and the wakes of parked tasks: delivery
// results, sleeps on signals and deadlines, `any` waits, kept wakes.
//
// Go mappings forced by language mechanics. A TypeScript expectation of `undefined` is a nil pointer here (JSON null in
// the comparison). A Promise.all of three tx.signal() calls starts all three reads before the first write; the calls
// run on goroutines, entered in call order (internal/entered), with the first one's storage read held until all three
// are in, as the three reads are in flight in Pi.

package harness

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/schema"
)

type sigAnswer struct {
	Text string `json:"text"`
}

var sigAnswerToken = durable.DefineSignal(durable.SignalDefinition[sigAnswer]{
	Name:    "test.answer",
	Version: 1,
	Schema:  schema.Obj(schema.Properties{schema.Prop("text", schema.Str())}),
})

type sigAskInput struct {
	Keys  []string `json:"keys"`
	Until *float64 `json:"until,omitempty"`
}

// sigSeen is a SignalState read by Ask: nil is `null` (nothing came).
type sigSeen struct {
	Value   *sigAnswer `json:"value,omitempty"`
	Expired bool       `json:"expired,omitempty"`
}

type sigAskResult struct {
	Wake   *durable.WakeReason `json:"wake,omitempty"`
	States []*sigSeen          `json:"states"`
}

type (
	sigAskRuntime = durable.TaskRuntime[sigAskInput, stepState, sigAskResult, any]
	sigAskRecord  = durable.RunningTask[sigAskInput, stepState, sigAskResult]
)

// sigAsk sleeps on `Answer` under `keys` (and `until`), then completes with its wake and every key's state (nil: none).
var sigAsk = durable.DefineTask(durable.TaskDefinition[sigAskInput, stepState, sigAskResult, any]{
	Name:    "test.ask",
	Version: 1,
	Initial: func(sigAskInput) stepState { return stepState{Phase: "ask"} },
	Phases: map[string]durable.PhaseHandler[sigAskInput, stepState, sigAskResult, any]{
		"ask": func(ctx context.Context, task sigAskRecord, runtime sigAskRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, sigAskRecord) (*durable.NextTaskState[stepState, sigAskResult], error) {
				signals := make([]durable.SignalKey, len(task.Input.Keys))
				for index, key := range task.Input.Keys {
					signals[index] = sigAnswerToken.Key(key)
				}
				return &durable.NextTaskState[stepState, sigAskResult]{Status: durable.TaskSleeping, Checkpoint: &stepState{Phase: "answered"}, Signals: signals, Until: task.Input.Until}, nil
			})
		},
		"answered": func(ctx context.Context, task sigAskRecord, runtime sigAskRuntime) error {
			states := make([]*sigSeen, 0, len(task.Input.Keys))
			for _, key := range task.Input.Keys {
				state, err := durable.ReadSignal(ctx, runtime, sigAnswerToken, key)
				if err != nil {
					return err
				}
				switch {
				case state == nil:
					states = append(states, nil)
				case state.Expired:
					states = append(states, &sigSeen{Expired: true})
				default:
					states = append(states, &sigSeen{Value: &state.Value})
				}
			}
			wake := task.State.Wake
			return runtime.Commit(ctx, func(durable.Tx, sigAskRecord) (*durable.NextTaskState[stepState, sigAskResult], error) {
				return completed[stepState, sigAskResult](sigAskResult{Wake: wake, States: states}), nil
			})
		},
	},
	Abort: func(ctx context.Context, _ sigAskRecord, runtime sigAskRuntime) error {
		return runtime.Commit(ctx, func(durable.Tx, sigAskRecord) (*durable.NextTaskState[stepState, sigAskResult], error) {
			return abortedWith[stepState, sigAskResult]("test"), nil
		})
	},
})

// sigStart creates a conversation-owned Ask in root.
func sigStart(t *testing.T, root Conversation, input sigAskInput) durable.TaskId {
	t.Helper()
	id, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, sigAsk, input, durable.TaskOptions{Ownership: conversationOwned})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func sigStatus(t *testing.T, harness Harness, id durable.TaskId) durable.TaskStatus {
	t.Helper()
	record, err := harness.GetTask(testContext, id)
	if err != nil {
		t.Fatal(err)
	}
	if record == nil {
		return ""
	}
	return record.State.Status
}

func sigSignal(t *testing.T, harness Harness, id durable.TaskId, key, text string) durable.SignalResult {
	t.Helper()
	result, err := Signal(testContext, harness, id, sigAnswerToken, key, sigAnswer{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// sigWake is the wake of a signal.
func sigWakeSignal(key string) map[string]any {
	return map[string]any{"reason": "signal", "signal": "test.answer", "key": key}
}

func sigValue(text string) map[string]any {
	return map[string]any{"value": map[string]any{"text": text}}
}

// sigResultOf is the JSON form of the result a task completed with.
func sigResultOf(t *testing.T, outcome durable.TaskOutcome[durable.JsonValue]) map[string]any {
	t.Helper()
	if outcome.Status != durable.OutcomeCompleted || outcome.Result == nil {
		t.Fatalf("outcome %s, want completed", describeOutcome(outcome))
	}
	result, ok := jsonOf(t, *outcome.Result).(map[string]any)
	if !ok {
		t.Fatalf("result %v is no object", *outcome.Result)
	}
	return result
}

func sigPanics(t *testing.T, fragment string, call func()) {
	t.Helper()
	defer func() {
		t.Helper()
		recovered := recover()
		if recovered == nil {
			t.Fatalf("no panic, want one containing %q", fragment)
		}
		if !strings.Contains(fmt.Sprint(recovered), fragment) {
			t.Fatalf("panic %q does not contain %q", fmt.Sprint(recovered), fragment)
		}
	}()
	call()
}

// sigOtherToken is a signal no task reads: delivering it only writes the task, which holds the reads of the deliveries after
// it until it is published.
var sigOtherToken = durable.DefineSignal(durable.SignalDefinition[durable.JsonValue]{Name: "test.other", Version: 1, Schema: schema.Unknown()})

func TestDefineSignal(t *testing.T) {
	t.Run("rejects names with a slash, bad versions, non-JSON schemas, and empty keys", func(t *testing.T) {
		define := func(name string, version int, signalSchema any) {
			durable.DefineSignal(durable.SignalDefinition[durable.JsonValue]{Name: name, Version: version, Schema: signalSchema})
		}
		sigPanics(t, `without "/"`, func() { define("a/b", 1, schema.Str()) })
		sigPanics(t, "positive integer", func() { define("a", 0, schema.Str()) })
		sigPanics(t, "bigint", func() { define("a", 1, schema.Obj(schema.Properties{schema.Prop("n", schema.BigInt())})) })
		sigPanics(t, "bytes", func() { define("a", 1, schema.Obj(schema.Properties{schema.Prop("b", schema.Bytes())})) })
		dependent := map[string]any{"type": "object", "dependencies": map[string]any{"a": []any{"b"}, "b": schema.Obj(schema.Properties{schema.Prop("n", schema.BigInt())})}}
		sigPanics(t, "bigint", func() { define("a", 1, dependent) })
		// Annotations are values, not schemas.
		define("a", 1, schema.Obj(schema.Properties{schema.Prop("type", schema.Str())}, schema.Examples(map[string]any{"type": "bigint"})))
		sigPanics(t, "non-empty", func() { sigAnswerToken.Key("") })
		if got := sigAnswerToken.Key("k"); got != (durable.SignalKey{Signal: "test.answer", Key: "k"}) {
			t.Fatalf("Key(k) = %+v", got)
		}
	})
}

func TestSignalDelivery(t *testing.T) {
	t.Run("answers not_found, invalid, delivered, duplicate, and closed", func(t *testing.T) {
		opened := tkOpenRoot(t, []durable.AnyTask{sigAsk.Erased()})
		harness := opened.harness
		id := sigStart(t, opened.root, sigAskInput{Keys: []string{"a"}})
		if got := sigSignal(t, harness, 999_999, "a", "x"); got != durable.SignalNotFound {
			t.Fatalf("unknown task: %s", got)
		}
		// The schema wants a string.
		wrong, err := durable.ToJsonValue(map[string]any{"text": 1})
		if err != nil {
			t.Fatal(err)
		}
		if got, err := harness.SignalErased(testContext, id, sigAnswerToken, "a", wrong); err != nil || got != durable.SignalInvalid {
			t.Fatalf("wrong value: %s %v", got, err)
		}
		// The schema admits it, but NaN is not JSON.
		open := durable.DefineSignal(durable.SignalDefinition[map[string]any]{
			Name: "test.open", Version: 1,
			Schema: schema.Obj(schema.Properties{schema.Prop("text", schema.Str())}, schema.Open()),
		})
		if got, err := Signal(testContext, harness, id, open, "a", map[string]any{"text": "x", "n": math.NaN()}); err != nil || got != durable.SignalInvalid {
			t.Fatalf("NaN: %s %v", got, err)
		}
		if got := sigSignal(t, harness, id, "a", "yes"); got != durable.SignalDelivered {
			t.Fatalf("first: %s", got)
		}
		if got := sigSignal(t, harness, id, "a", "again"); got != durable.SignalDuplicate {
			t.Fatalf("second: %s", got)
		}
		if outcome := tkWaitOutcome(t, harness, id); outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("outcome %s", describeOutcome(outcome))
		}
		if got := sigSignal(t, harness, id, "b", "late"); got != durable.SignalClosed {
			t.Fatalf("late: %s", got)
		}
		mustClose(t, harness)
	})

	t.Run("answers closed for an abort-marked task", func(t *testing.T) {
		opened := tkOpenRoot(t, []durable.AnyTask{sigAsk.Erased()})
		id := sigStart(t, opened.root, sigAskInput{Keys: []string{"a"}})
		if _, err := opened.harness.AbortTask(testContext, id); err != nil {
			t.Fatal(err)
		}
		if got := sigSignal(t, opened.harness, id, "a", "yes"); got != durable.SignalClosed {
			t.Fatalf("delivery: %s", got)
		}
		mustClose(t, opened.harness)
	})

	t.Run("decides deliveries started together in one commit against each other", func(t *testing.T) {
		store := newControlledStorage()
		opened := tkOpenRoot(t, []durable.AnyTask{sigAsk.Erased()}, tkOptions{storage: store})
		harness := opened.harness
		id := sigStart(t, opened.root, sigAskInput{Keys: []string{"a", "b"}})
		// An earlier commit of the task is in storage, as a storage call is in flight in Pi: the committed read of every
		// delivery below waits for it, so all three reads are in flight before the first write.
		held := store.holdCommits()
		earlier := make(chan error, 1)
		callAsync(func() {
			_, err := durable.Commit(testContext, harness, func(tx durable.Tx) (durable.SignalResult, error) {
				return durable.Signal(tx, id, sigOtherToken, "x", durable.JsonValue(nil))
			})
			earlier <- err
		})
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		calls := []struct{ key, text string }{{"a", "first"}, {"a", "second"}, {"b", "bee"}}
		results := make([]durable.SignalResult, len(calls))
		failures := make([]error, len(calls))
		_, err := durable.Commit(testContext, harness, func(tx durable.Tx) (struct{}, error) {
			var group sync.WaitGroup
			for index, call := range calls {
				group.Add(1)
				// Each call runs up to its first wait before the next starts, as the array of a Promise.all is evaluated.
				wait := entered.Go(func() {
					defer group.Done()
					results[index], failures[index] = durable.Signal(tx, id, sigAnswerToken, call.key, sigAnswer{Text: call.text})
				})
				wait()
			}
			held.release()
			group.Wait()
			return struct{}{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := <-earlier; err != nil {
			t.Fatal(err)
		}
		if err := errors.Join(failures...); err != nil {
			t.Fatal(err)
		}
		if want := []durable.SignalResult{durable.SignalDelivered, durable.SignalDuplicate, durable.SignalDelivered}; !reflect.DeepEqual(results, want) {
			t.Fatalf("results %v, want %v", results, want)
		}
		states := sigResultOf(t, tkWaitOutcome(t, harness, id))["states"]
		if want := []any{sigValue("first"), sigValue("bee")}; !reflect.DeepEqual(states, want) {
			t.Fatalf("states %v, want %v", states, want)
		}
		mustClose(t, harness)
	})

	t.Run("reads the task, so it throws ReadAfterWrite after a table write", func(t *testing.T) {
		opened := tkOpenRoot(t, []durable.AnyTask{sigAsk.Erased()})
		id := sigStart(t, opened.root, sigAskInput{Keys: []string{"a"}})
		_, err := durable.Commit(testContext, opened.harness, func(tx durable.Tx) (struct{}, error) {
			if _, err := durable.Signal(tx, id, sigAnswerToken, "a", sigAnswer{Text: "one"}); err != nil {
				return struct{}{}, err
			}
			_, err := durable.Signal(tx, id, sigAnswerToken, "b", sigAnswer{Text: "two"})
			return struct{}{}, err
		})
		if _, ok := errors.AsType[*durable.ReadAfterWrite](err); !ok {
			t.Fatalf("second delivery: %v, want ReadAfterWrite", err)
		}
		mustClose(t, opened.harness)
	})
}

func TestSleepingOnSignals(t *testing.T) {
	t.Run("keeps a value delivered before the sleep, which then wakes at once", func(t *testing.T) {
		opened := tkOpenRoot(t, []durable.AnyTask{sigAsk.Erased()})
		id := sigStart(t, opened.root, sigAskInput{Keys: []string{"a"}})
		// Not running yet: the value waits in the task.
		if got := sigSignal(t, opened.harness, id, "a", "early"); got != durable.SignalDelivered {
			t.Fatalf("delivery: %s", got)
		}
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeCompleted(map[string]any{
			"wake":   sigWakeSignal("a"),
			"states": []any{sigValue("early")},
		}))
		mustClose(t, opened.harness)
	})

	t.Run("holds no invocation while it waits, and wakes on the first listed key holding a value", func(t *testing.T) {
		opened := tkOpenRoot(t, []durable.AnyTask{sigAsk.Erased()}, tkOptions{onWake: func(Wake) {}})
		harness := opened.harness
		graph := startedGraph(harness)
		id := sigStart(t, opened.root, sigAskInput{Keys: []string{"a", "b"}})
		harness.Resume()
		eventually(t, func() bool { return sigStatus(t, harness, id) == durable.TaskSleeping })
		eventually(t, func() bool { return !harness.Wake().Active })
		node := graphValue(harness, graph).Tasks[strconv.Itoa(int(id))]
		wantState := map[string]any{
			"status": "sleeping",
			"phase":  "answered",
			"signals": []any{
				map[string]any{"signal": "test.answer", "key": "a"},
				map[string]any{"signal": "test.answer", "key": "b"},
			},
		}
		if got := jsonOf(t, node.State); !reflect.DeepEqual(got, wantState) {
			t.Fatalf("graph state %v, want %v", got, wantState)
		}
		_, _ = graph.Stop()
		// Without until there is no deadline to wake for.
		if wake := harness.Wake(); wake.Active || wake.At != nil {
			t.Fatalf("wake %+v, want {active: false}", wake)
		}
		inspection, err := harness.Inspect(testContext)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, task := range inspection.Tasks {
			if task.Record.Id == id {
				found = true
				if want := (TaskInspectionState{Kind: TaskInspectionSleeping}); !reflect.DeepEqual(task.State, want) {
					t.Fatalf("inspection state %+v, want %+v", task.State, want)
				}
			}
		}
		if !found {
			t.Fatal("the sleeping task is not in the inspection")
		}
		if got := sigSignal(t, harness, id, "b", "bee"); got != durable.SignalDelivered {
			t.Fatalf("delivery: %s", got)
		}
		expectOutcome(t, tkWaitOutcome(t, harness, id), outcomeCompleted(map[string]any{
			"wake":   sigWakeSignal("b"),
			"states": []any{nil, sigValue("bee")},
		}))
		mustClose(t, harness)
	})

	t.Run("expires its keys at the deadline, so a late delivery is closed", func(t *testing.T) {
		clock := &clockAt{}
		clock.set(1_000)
		opened := tkOpenRoot(t, []durable.AnyTask{sigAsk.Erased()}, tkOptions{now: clock.now, onWake: func(Wake) {}})
		harness := opened.harness
		until := 2_000.0
		id := sigStart(t, opened.root, sigAskInput{Keys: []string{"a"}, Until: &until})
		harness.Resume()
		eventually(t, func() bool { return sigStatus(t, harness, id) == durable.TaskSleeping })
		eventually(t, func() bool { return !harness.Wake().Active })
		if wake := harness.Wake(); wake.Active || wake.At == nil || *wake.At != 2_000 {
			t.Fatalf("wake %+v, want {active: false, at: 2000}", wake)
		}
		clock.set(2_000)
		harness.Resume()
		expectOutcome(t, tkWaitOutcome(t, harness, id), outcomeCompleted(map[string]any{
			"wake":   map[string]any{"reason": "expired"},
			"states": []any{map[string]any{"expired": true}},
		}))
		mustClose(t, harness)
	})

	t.Run("answers a delivery after the deadline closed and expires the key, before the sleep is reserved", func(t *testing.T) {
		clock := &clockAt{}
		clock.set(1_000)
		opened := tkOpenRoot(t, []durable.AnyTask{sigAsk.Erased()}, tkOptions{now: clock.now, onWake: func(Wake) {}})
		harness := opened.harness
		until := 2_000.0
		id := sigStart(t, opened.root, sigAskInput{Keys: []string{"a"}, Until: &until})
		harness.Resume()
		eventually(t, func() bool { return sigStatus(t, harness, id) == durable.TaskSleeping })
		// Due, but no pass ran since: the delivery itself finds the deadline passed.
		clock.set(2_500)
		if got := sigSignal(t, harness, id, "a", "late"); got != durable.SignalClosed {
			t.Fatalf("late delivery: %s", got)
		}
		result := sigResultOf(t, tkWaitOutcome(t, harness, id))
		if want := map[string]any{"reason": "expired"}; !reflect.DeepEqual(result["wake"], want) {
			t.Fatalf("wake %v, want %v", result["wake"], want)
		}
		if want := []any{map[string]any{"expired": true}}; !reflect.DeepEqual(result["states"], want) {
			t.Fatalf("states %v, want %v", result["states"], want)
		}
		mustClose(t, harness)
	})

	t.Run("rejects a sleep on a key that already woke a wait, and one without until or signals", func(t *testing.T) {
		type twiceInput struct {
			Signals bool `json:"signals"`
		}
		type (
			twiceRuntime = durable.TaskRuntime[twiceInput, stepState, durable.JsonValue, any]
			twiceRecord  = durable.RunningTask[twiceInput, stepState, durable.JsonValue]
		)
		outcomes := &syncList[string]{}
		twice := durable.DefineTask(durable.TaskDefinition[twiceInput, stepState, durable.JsonValue, any]{
			Name:    "test.twice",
			Version: 1,
			Initial: func(twiceInput) stepState { return stepState{Phase: "first"} },
			Phases: map[string]durable.PhaseHandler[twiceInput, stepState, durable.JsonValue, any]{
				"first": func(ctx context.Context, task twiceRecord, runtime twiceRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, twiceRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						next := &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskSleeping, Checkpoint: &stepState{Phase: "second"}}
						if task.Input.Signals {
							next.Signals = []durable.SignalKey{sigAnswerToken.Key("a")}
						}
						return next, nil
					})
				},
				"second": func(ctx context.Context, _ twiceRecord, runtime twiceRuntime) error {
					err := runtime.Commit(ctx, func(durable.Tx, twiceRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskSleeping, Checkpoint: &stepState{Phase: "first"}, Signals: []durable.SignalKey{sigAnswerToken.Key("a")}}, nil
					})
					if err == nil {
						return nil
					}
					outcomes.add(err.Error())
					return runtime.Commit(ctx, func(durable.Tx, twiceRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return completed[stepState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(ctx context.Context, _ twiceRecord, runtime twiceRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, twiceRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return abortedWith[stepState, durable.JsonValue]("test"), nil
				})
			},
		})
		opened := tkOpenRoot(t, []durable.AnyTask{twice.Erased()})
		harness := opened.harness
		create := func(signals bool) durable.TaskId {
			id, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
				return durable.CreateTask(tx, twice, twiceInput{Signals: signals}, durable.TaskOptions{Ownership: conversationOwned})
			})
			if err != nil {
				t.Fatal(err)
			}
			return id
		}
		consumed := create(true)
		sigSignal(t, harness, consumed, "a", "once")
		tkWaitOutcome(t, harness, consumed)
		if want := []string{fmt.Sprintf("Task %d cannot sleep on signal test.answer key a: it already woke a wait", consumed)}; !reflect.DeepEqual(outcomes.all(), want) {
			t.Fatalf("errors %v, want %v", outcomes.all(), want)
		}
		empty := create(false)
		outcome := tkWaitOutcome(t, harness, empty)
		if outcome.Status != durable.OutcomeFaulted || outcome.Error == nil || outcome.Error.Message != fmt.Sprintf("Task %d cannot sleep without until or signals", empty) {
			t.Fatalf("outcome %s", describeOutcome(outcome))
		}
		mustClose(t, harness)
	})

	t.Run("wakes on the first listed key in order, and keeps the other key's value for a later sleep", func(t *testing.T) {
		type twoState struct {
			Phase string               `json:"phase"`
			Wakes []durable.WakeReason `json:"wakes,omitempty"`
		}
		type (
			twoRuntime = durable.TaskRuntime[durable.JsonValue, twoState, []durable.WakeReason, any]
			twoRecord  = durable.RunningTask[durable.JsonValue, twoState, []durable.WakeReason]
		)
		sleepOn := func(next twoState, keys ...string) func(durable.Tx, twoRecord) (*durable.NextTaskState[twoState, []durable.WakeReason], error) {
			return func(durable.Tx, twoRecord) (*durable.NextTaskState[twoState, []durable.WakeReason], error) {
				signals := make([]durable.SignalKey, len(keys))
				for index, key := range keys {
					signals[index] = sigAnswerToken.Key(key)
				}
				return &durable.NextTaskState[twoState, []durable.WakeReason]{Status: durable.TaskSleeping, Checkpoint: &next, Signals: signals}, nil
			}
		}
		two := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, twoState, []durable.WakeReason, any]{
			Name:    "test.two",
			Version: 1,
			Initial: func(durable.JsonValue) twoState { return twoState{Phase: "first"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, twoState, []durable.WakeReason, any]{
				"first": func(ctx context.Context, _ twoRecord, runtime twoRuntime) error {
					return runtime.Commit(ctx, sleepOn(twoState{Phase: "second", Wakes: []durable.WakeReason{}}, "a", "b"))
				},
				"second": func(ctx context.Context, task twoRecord, runtime twoRuntime) error {
					return runtime.Commit(ctx, sleepOn(twoState{Phase: "done", Wakes: []durable.WakeReason{*task.State.Wake}}, "b"))
				},
				"done": func(ctx context.Context, task twoRecord, runtime twoRuntime) error {
					wakes := append(slices.Clone(task.State.Checkpoint.Wakes), *task.State.Wake)
					return runtime.Commit(ctx, func(durable.Tx, twoRecord) (*durable.NextTaskState[twoState, []durable.WakeReason], error) {
						return completed[twoState, []durable.WakeReason](wakes), nil
					})
				},
			},
			Abort: func(ctx context.Context, _ twoRecord, runtime twoRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, twoRecord) (*durable.NextTaskState[twoState, []durable.WakeReason], error) {
					return abortedWith[twoState, []durable.WakeReason]("test"), nil
				})
			},
		})
		opened := tkOpenRoot(t, []durable.AnyTask{two.Erased()})
		harness := opened.harness
		id, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, two, nil, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		// Both before the first sleep; b is delivered first, but a is listed first.
		for _, delivery := range []struct{ key, text string }{{"b", "bee"}, {"a", "ay"}} {
			result, err := durable.Commit(testContext, harness, func(tx durable.Tx) (durable.SignalResult, error) {
				return durable.Signal(tx, id, sigAnswerToken, delivery.key, sigAnswer{Text: delivery.text})
			})
			if err != nil || result != durable.SignalDelivered {
				t.Fatalf("delivery under %s: %s %v", delivery.key, result, err)
			}
		}
		expectOutcome(t, tkWaitOutcome(t, harness, id), outcomeCompleted([]any{sigWakeSignal("a"), sigWakeSignal("b")}))
		mustClose(t, harness)
	})

	t.Run("rejects a sleep on a key that expired", func(t *testing.T) {
		clock := &clockAt{}
		clock.set(1_000)
		errs := &syncList[string]{}
		type (
			againRuntime = durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, any]
			againRecord  = durable.RunningTask[durable.JsonValue, stepState, durable.JsonValue]
		)
		again := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
			Name:    "test.again",
			Version: 1,
			Initial: func(durable.JsonValue) stepState { return stepState{Phase: "first"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
				"first": func(ctx context.Context, _ againRecord, runtime againRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, againRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						until := 2_000.0
						return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskSleeping, Checkpoint: &stepState{Phase: "second"}, Signals: []durable.SignalKey{sigAnswerToken.Key("a")}, Until: &until}, nil
					})
				},
				"second": func(ctx context.Context, _ againRecord, runtime againRuntime) error {
					if err := runtime.Commit(ctx, func(durable.Tx, againRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskSleeping, Checkpoint: &stepState{Phase: "first"}, Signals: []durable.SignalKey{sigAnswerToken.Key("a")}}, nil
					}); err != nil {
						errs.add(err.Error())
					}
					return tkComplete(ctx, runtime)
				},
			},
			Abort: func(ctx context.Context, _ againRecord, runtime againRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, againRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return abortedWith[stepState, durable.JsonValue]("test"), nil
				})
			},
		})
		opened := tkOpenRoot(t, []durable.AnyTask{again.Erased()}, tkOptions{now: clock.now, onWake: func(Wake) {}})
		harness := opened.harness
		id, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, again, nil, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		harness.Resume()
		eventually(t, func() bool { return sigStatus(t, harness, id) == durable.TaskSleeping })
		clock.set(2_000)
		harness.Resume()
		tkWaitOutcome(t, harness, id)
		if want := []string{fmt.Sprintf("Task %d cannot sleep on signal test.answer key a: it expired", id)}; !reflect.DeepEqual(errs.all(), want) {
			t.Fatalf("errors %v, want %v", errs.all(), want)
		}
		mustClose(t, harness)
	})

	t.Run("ends a sleep on signals at once when aborted", func(t *testing.T) {
		opened := tkOpenRoot(t, []durable.AnyTask{sigAsk.Erased()})
		harness := opened.harness
		id := sigStart(t, opened.root, sigAskInput{Keys: []string{"a"}})
		harness.Resume()
		eventually(t, func() bool { return sigStatus(t, harness, id) == durable.TaskSleeping })
		if _, err := harness.AbortTask(testContext, id); err != nil {
			t.Fatal(err)
		}
		expectOutcome(t, tkWaitOutcome(t, harness, id), outcomeAborted("test"))
		mustClose(t, harness)
	})

	t.Run("throws on reading a value of a newer version, or of an older one without migrate", func(t *testing.T) {
		type count struct {
			N int `json:"n"`
		}
		schemaOf := schema.Obj(schema.Properties{schema.Prop("n", schema.Int())})
		v1 := durable.DefineSignal(durable.SignalDefinition[count]{Name: "test.versioned", Version: 1, Schema: schemaOf})
		v2 := durable.DefineSignal(durable.SignalDefinition[count]{Name: "test.versioned", Version: 2, Schema: schemaOf})
		type readInput struct {
			Token int `json:"token"`
		}
		type (
			readRuntime = durable.TaskRuntime[readInput, stepState, durable.JsonValue, any]
			readRecord  = durable.RunningTask[readInput, stepState, durable.JsonValue]
		)
		// Collected by the token the task reads with: the two tasks run in no fixed order.
		errs := &syncMap[string]{}
		read := durable.DefineTask(durable.TaskDefinition[readInput, stepState, durable.JsonValue, any]{
			Name:    "test.read-versions",
			Version: 1,
			Initial: func(readInput) stepState { return stepState{Phase: "read"} },
			Phases: map[string]durable.PhaseHandler[readInput, stepState, durable.JsonValue, any]{
				"read": func(ctx context.Context, task readRecord, runtime readRuntime) error {
					token := v1
					if task.Input.Token == 2 {
						token = v2
					}
					if _, err := durable.ReadSignal(ctx, runtime, token, "k"); err != nil {
						errs.put(strconv.Itoa(task.Input.Token), err.Error())
					}
					return runtime.Commit(ctx, func(durable.Tx, readRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return completed[stepState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(ctx context.Context, _ readRecord, runtime readRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, readRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return abortedWith[stepState, durable.JsonValue]("test"), nil
				})
			},
		})
		opened := tkOpenRoot(t, []durable.AnyTask{read.Erased()})
		harness := opened.harness
		create := func(token int) durable.TaskId {
			id, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
				return durable.CreateTask(tx, read, readInput{Token: token}, durable.TaskOptions{Ownership: conversationOwned})
			})
			if err != nil {
				t.Fatal(err)
			}
			return id
		}
		newer := create(1)
		if result, err := Signal(testContext, harness, newer, v2, "k", count{N: 2}); err != nil || result != durable.SignalDelivered {
			t.Fatalf("newer: %s %v", result, err)
		}
		older := create(2)
		if result, err := Signal(testContext, harness, older, v1, "k", count{N: 1}); err != nil || result != durable.SignalDelivered {
			t.Fatalf("older: %s %v", result, err)
		}
		tkWaitOutcome(t, harness, newer)
		tkWaitOutcome(t, harness, older)
		found := errs.all()
		got := []string{found["1"], found["2"]}
		want := []string{
			"Signal test.versioned value has version 2, newer than 1",
			"Signal test.versioned has no migration from version 1",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("errors %v, want %v", got, want)
		}
		mustClose(t, harness)
	})

	t.Run("migrates a value stored by an older version when it is read", func(t *testing.T) {
		type oldCount struct {
			N string `json:"n"`
		}
		type newCount struct {
			N int `json:"n"`
		}
		old := durable.DefineSignal(durable.SignalDefinition[oldCount]{Name: "test.count", Version: 1, Schema: schema.Obj(schema.Properties{schema.Prop("n", schema.Str())})})
		current := durable.DefineSignal(durable.SignalDefinition[newCount]{
			Name: "test.count", Version: 2, Schema: schema.Obj(schema.Properties{schema.Prop("n", schema.Int())}),
			Migrate: func(value durable.JsonValue, _ int) (newCount, error) {
				stored, err := durable.FromJsonValue[oldCount](value)
				if err != nil {
					return newCount{}, err
				}
				n, err := strconv.Atoi(stored.N)
				return newCount{N: n}, err
			},
		})
		reads := &syncList[*durable.SignalState[newCount]]{}
		read := tkOneStep("test.read", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			state, err := durable.ReadSignal(ctx, runtime, current, "k")
			if err != nil {
				return err
			}
			reads.add(state)
			return tkComplete(ctx, runtime)
		})
		opened := tkOpenRoot(t, []durable.AnyTask{read.Erased()})
		harness := opened.harness
		id := tkStart(t, opened.root, read.Erased())
		if result, err := Signal(testContext, harness, id, old, "k", oldCount{N: "42"}); err != nil || result != durable.SignalDelivered {
			t.Fatalf("delivery: %s %v", result, err)
		}
		tkWaitOutcome(t, harness, id)
		if got, want := reads.all(), []*durable.SignalState[newCount]{{Value: newCount{N: 42}}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("reads %v, want %v", got, want)
		}
		mustClose(t, harness)
	})

	t.Run("composes a signal with the task's own state change in one commit", func(t *testing.T) {
		type (
			selfRuntime = durable.TaskRuntime[durable.JsonValue, stepState, *durable.WakeReason, any]
			selfRecord  = durable.RunningTask[durable.JsonValue, stepState, *durable.WakeReason]
		)
		self := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, *durable.WakeReason, any]{
			Name:    "test.self",
			Version: 1,
			Initial: func(durable.JsonValue) stepState { return stepState{Phase: "start"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, *durable.WakeReason, any]{
				"start": func(ctx context.Context, _ selfRecord, runtime selfRuntime) error {
					return runtime.Commit(ctx, func(tx durable.Tx, _ selfRecord) (*durable.NextTaskState[stepState, *durable.WakeReason], error) {
						if result, err := durable.Signal(tx, runtime.TaskId(), sigAnswerToken, "self", sigAnswer{Text: "me"}); err != nil || result != durable.SignalDelivered {
							return nil, fmt.Errorf("self delivery: %s %w", result, err)
						}
						return &durable.NextTaskState[stepState, *durable.WakeReason]{Status: durable.TaskSleeping, Checkpoint: &stepState{Phase: "woke"}, Signals: []durable.SignalKey{sigAnswerToken.Key("self")}}, nil
					})
				},
				"woke": func(ctx context.Context, task selfRecord, runtime selfRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, selfRecord) (*durable.NextTaskState[stepState, *durable.WakeReason], error) {
						return completed[stepState, *durable.WakeReason](task.State.Wake), nil
					})
				},
			},
			Abort: func(ctx context.Context, _ selfRecord, runtime selfRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, selfRecord) (*durable.NextTaskState[stepState, *durable.WakeReason], error) {
					return abortedWith[stepState, *durable.WakeReason]("test"), nil
				})
			},
		})
		opened := tkOpenRoot(t, []durable.AnyTask{self.Erased()})
		id, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, self, nil, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeCompleted(sigWakeSignal("self")))
		mustClose(t, opened.harness)
	})
}

type (
	sigJoinRuntime = durable.TaskRuntime[durable.JsonValue, sigJoinState, *durable.WakeReason, any]
	sigJoinRecord  = durable.RunningTask[durable.JsonValue, sigJoinState, *durable.WakeReason]
)

type sigJoinState struct {
	Phase string           `json:"phase"`
	On    []durable.TaskId `json:"on,omitempty"`
}

// sigParent waits `any` on two `Ask` children; once woken, aborts the one still live and completes with its wake.
var sigParent = durable.DefineTask(durable.TaskDefinition[durable.JsonValue, sigJoinState, *durable.WakeReason, any]{
	Name:    "test.any",
	Version: 1,
	Initial: func(durable.JsonValue) sigJoinState { return sigJoinState{Phase: "spawn"} },
	Phases: map[string]durable.PhaseHandler[durable.JsonValue, sigJoinState, *durable.WakeReason, any]{
		"spawn": func(ctx context.Context, _ sigJoinRecord, runtime sigJoinRuntime) error {
			return runtime.Commit(ctx, func(tx durable.Tx, _ sigJoinRecord) (*durable.NextTaskState[sigJoinState, *durable.WakeReason], error) {
				own := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: runtime.TaskId()}}
				on := make([]durable.TaskId, 0, 2)
				for range 2 {
					id, err := durable.CreateTask(tx, sigAsk, sigAskInput{Keys: []string{"a"}}, own)
					if err != nil {
						return nil, err
					}
					on = append(on, id)
				}
				return &durable.NextTaskState[sigJoinState, *durable.WakeReason]{Status: durable.TaskWaiting, Checkpoint: &sigJoinState{Phase: "joined", On: on}, On: on, Policy: durable.JoinAny}, nil
			})
		},
		"joined": func(ctx context.Context, task sigJoinRecord, runtime sigJoinRuntime) error {
			wake := task.State.Wake
			for _, id := range task.State.Checkpoint.On {
				if err := runtime.AbortOwned(ctx, id); err != nil {
					return err
				}
			}
			return runtime.Commit(ctx, func(durable.Tx, sigJoinRecord) (*durable.NextTaskState[sigJoinState, *durable.WakeReason], error) {
				return completed[sigJoinState, *durable.WakeReason](wake), nil
			})
		},
	},
	Abort: func(ctx context.Context, _ sigJoinRecord, runtime sigJoinRuntime) error {
		return runtime.Commit(ctx, func(durable.Tx, sigJoinRecord) (*durable.NextTaskState[sigJoinState, *durable.WakeReason], error) {
			return abortedWith[sigJoinState, *durable.WakeReason]("test"), nil
		})
	},
})

// sigStartParent starts Parent and waits until it waits on its two children, both asleep; returns its ID and theirs.
func sigStartParent(t *testing.T, harness Harness, root Conversation) (id, first, second durable.TaskId) {
	t.Helper()
	id, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, sigParent, nil, durable.TaskOptions{Ownership: conversationOwned})
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.Resume()
	eventually(t, func() bool { return sigStatus(t, harness, id) == durable.TaskWaiting })
	record, err := harness.GetTask(testContext, id)
	if err != nil || record == nil || len(record.State.On) != 2 {
		t.Fatalf("parent record %+v %v", record, err)
	}
	first, second = record.State.On[0], record.State.On[1]
	eventually(t, func() bool { return sigStatus(t, harness, first) == durable.TaskSleeping })
	eventually(t, func() bool { return sigStatus(t, harness, second) == durable.TaskSleeping })
	return id, first, second
}

func TestWaitsWithPolicyAny(t *testing.T) {
	tasks := []durable.AnyTask{sigAsk.Erased(), sigParent.Erased()}

	t.Run("resumes once one task in on is terminal, with the ended ones as its wake", func(t *testing.T) {
		opened := tkOpenRoot(t, tasks)
		harness := opened.harness
		id, first, second := sigStartParent(t, harness, opened.root)
		sigSignal(t, harness, second, "a", "done")
		expectOutcome(t, tkWaitOutcome(t, harness, id), outcomeCompleted(map[string]any{"reason": "tasks", "ended": []any{float64(second)}}))
		record, err := harness.GetTask(testContext, first)
		if err != nil || record == nil || record.State.Outcome == nil || record.State.Outcome.Status != durable.OutcomeAborted {
			t.Fatalf("first child %+v %v, want aborted", record, err)
		}
		mustClose(t, harness)
	})

	t.Run("is active in wake() once a task in on is terminal, not while all are live", func(t *testing.T) {
		store := storage.NewMemoryStorage()
		opened := tkOpenRoot(t, tasks, tkOptions{storage: store})
		id, first, _ := sigStartParent(t, opened.harness, opened.root)
		mustClose(t, opened.harness)
		// Reopened paused, without Ask: the children are blocked, and only the waiter can become active.
		store.Reopen()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{sigParent.Erased()}, openTasksOptions{onWake: func(Wake) {}})
		eventually(t, func() bool { return !harness.Wake().Active })
		if wake := harness.Wake(); wake.Active || wake.At != nil {
			t.Fatalf("wake %+v, want {active: false}", wake)
		}
		// Aborting a blocked child settles it orphaned at once, without scheduling.
		if _, err := harness.AbortTask(testContext, first); err != nil {
			t.Fatal(err)
		}
		if status := sigStatus(t, harness, first); status != durable.TaskTerminal {
			t.Fatalf("aborted child is %s, want terminal", status)
		}
		eventually(t, func() bool { return harness.Wake().Active })
		harness.Resume()
		expectOutcome(t, tkWaitOutcome(t, harness, id), outcomeCompleted(map[string]any{"reason": "tasks", "ended": []any{float64(first)}}))
		mustClose(t, harness)
	})
}

func TestWakes(t *testing.T) {
	t.Run("keeps a sleep on signals asleep across reopen, active once a value is delivered", func(t *testing.T) {
		path := sqlitePath(t)
		opened := tkOpenRoot(t, []durable.AnyTask{sigAsk.Erased()}, tkOptions{storage: tkOpenSqlite(t, path), onWake: func(Wake) {}})
		id := sigStart(t, opened.root, sigAskInput{Keys: []string{"a"}})
		opened.harness.Resume()
		eventually(t, func() bool { return sigStatus(t, opened.harness, id) == durable.TaskSleeping })
		mustClose(t, opened.harness)
		harness, _, _ := openTasks(t, tkOpenSqlite(t, path), []durable.AnyTask{sigAsk.Erased()}, openTasksOptions{onWake: func(Wake) {}})
		// Scheduling is paused: once open's recovery pass landed, the sleep needs no process.
		eventually(t, func() bool { return !harness.Wake().Active })
		if wake := harness.Wake(); wake.Active || wake.At != nil {
			t.Fatalf("wake %+v, want {active: false}", wake)
		}
		// A commit of its own delivers without enabling scheduling: the task can run now.
		if result, err := durable.Commit(testContext, harness, func(tx durable.Tx) (durable.SignalResult, error) {
			return durable.Signal(tx, id, sigAnswerToken, "a", sigAnswer{Text: "later"})
		}); err != nil || result != durable.SignalDelivered {
			t.Fatalf("delivery: %s %v", result, err)
		}
		eventually(t, func() bool { return harness.Wake().Active })
		harness.Resume()
		states := sigResultOf(t, tkWaitOutcome(t, harness, id))["states"]
		if want := []any{sigValue("later")}; !reflect.DeepEqual(states, want) {
			t.Fatalf("states %v, want %v", states, want)
		}
		mustClose(t, harness)
	})

	t.Run("counts no progress for a woken task that commits its checkpoint again", func(t *testing.T) {
		path := sqlitePath(t)
		restarts := &syncList[int]{}
		type (
			recommitRuntime = durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, any]
			recommitRecord  = durable.RunningTask[durable.JsonValue, stepState, durable.JsonValue]
		)
		running := func(phase string) func(durable.Tx, recommitRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
			return func(durable.Tx, recommitRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: &stepState{Phase: phase}}, nil
			}
		}
		again := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
			Name:    "test.recommit",
			Version: 1,
			Initial: func(durable.JsonValue) stepState { return stepState{Phase: "woke"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
				"woke": func(ctx context.Context, _ recommitRecord, runtime recommitRuntime) error {
					if err := runtime.Commit(ctx, running("woke")); err != nil {
						return err
					}
					record, err := runtime.GetTask(ctx, runtime.TaskId())
					if err != nil || record == nil {
						return fmt.Errorf("task record %v: %w", record, err)
					}
					restarts.add(record.Restarts)
					if record.State.Wake == nil || !reflect.DeepEqual(*record.State.Wake, durable.WakeReason{Reason: durable.WakeTime}) {
						return fmt.Errorf("wake %+v, want time", record.State.Wake)
					}
					return runtime.Commit(ctx, running("next"))
				},
				"next": func(ctx context.Context, _ recommitRecord, runtime recommitRuntime) error {
					record, err := runtime.GetTask(ctx, runtime.TaskId())
					if err != nil || record == nil {
						return fmt.Errorf("task record %v: %w", record, err)
					}
					restarts.add(record.Restarts)
					return tkComplete(ctx, runtime)
				},
			},
			Abort: func(ctx context.Context, _ recommitRecord, runtime recommitRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, recommitRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return abortedWith[stepState, durable.JsonValue]("test"), nil
				})
			},
		})
		first, _, _ := openTasks(t, tkOpenSqlite(t, path), []durable.AnyTask{again.Erased()})
		mustRoot(t, first, nil)
		mustClose(t, first)
		// A woken task cut off by a crash: running with its wake, as a dead process left it.
		store := tkOpenSqlite(t, path)
		minted, err := store.MintId()
		if err != nil {
			t.Fatal(err)
		}
		id := durable.TaskId(minted)
		checkpoint := durable.JsonValue(map[string]any{"phase": "woke"})
		commitWrites(t, store, durable.TaskWrite{Value: durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{
			Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "test.recommit", Version: 1,
			State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: &checkpoint, Wake: &durable.WakeReason{Reason: durable.WakeTime}},
		}})
		harness, _, _ := openTasks(t, store, []durable.AnyTask{again.Erased()})
		wantState := map[string]any{"status": "pending", "checkpoint": map[string]any{"phase": "woke"}, "wake": map[string]any{"reason": "time"}}
		if got := jsonOf(t, tkTaskRecord(t, harness, id).State); !reflect.DeepEqual(got, wantState) {
			t.Fatalf("state %v, want %v", got, wantState)
		}
		tkWaitOutcome(t, harness, id)
		// The same checkpoint again keeps the count; the next one starts it over.
		if got, want := restarts.all(), []int{1, 0}; !reflect.DeepEqual(got, want) {
			t.Fatalf("restarts %v, want %v", got, want)
		}
		mustClose(t, harness)
	})

	t.Run("keeps the wake through a commit of the same checkpoint and a reopen, and drops it with the next checkpoint", func(t *testing.T) {
		path := sqlitePath(t)
		seen := &syncList[*durable.WakeReason]{}
		var runs atomic.Int32
		type (
			keepRuntime = durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, any]
			keepRecord  = durable.RunningTask[durable.JsonValue, stepState, durable.JsonValue]
		)
		recorded := func(ctx context.Context, runtime keepRuntime) (*durable.WakeReason, error) {
			record, err := runtime.GetTask(ctx, runtime.TaskId())
			if err != nil || record == nil {
				return nil, fmt.Errorf("task record %v: %w", record, err)
			}
			return record.State.Wake, nil
		}
		keep := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
			Name:    "test.keep",
			Version: 1,
			Initial: func(durable.JsonValue) stepState { return stepState{Phase: "sleep"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
				"sleep": func(ctx context.Context, _ keepRecord, runtime keepRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, keepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskSleeping, Checkpoint: &stepState{Phase: "woke"}, Signals: []durable.SignalKey{sigAnswerToken.Key("k")}}, nil
					})
				},
				"woke": func(ctx context.Context, task keepRecord, runtime keepRuntime) error {
					seen.add(task.State.Wake)
					// Intent: the same checkpoint again keeps the wake.
					if err := runtime.Commit(ctx, func(durable.Tx, keepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: &stepState{Phase: "woke"}}, nil
					}); err != nil {
						return err
					}
					wake, err := recorded(ctx, runtime)
					if err != nil {
						return err
					}
					seen.add(wake)
					// The first run is stopped by the close.
					if runs.Add(1) == 1 {
						return abortedBy(runtime.Signal())
					}
					return runtime.Commit(ctx, func(durable.Tx, keepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: &stepState{Phase: "after"}}, nil
					})
				},
				"after": func(ctx context.Context, _ keepRecord, runtime keepRuntime) error {
					wake, err := recorded(ctx, runtime)
					if err != nil {
						return err
					}
					seen.add(wake)
					return tkComplete(ctx, runtime)
				},
			},
			Abort: func(ctx context.Context, _ keepRecord, runtime keepRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, keepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return abortedWith[stepState, durable.JsonValue]("test"), nil
				})
			},
		})
		opened := tkOpenRoot(t, []durable.AnyTask{keep.Erased()}, tkOptions{storage: tkOpenSqlite(t, path)})
		harness := opened.harness
		id, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, keep, nil, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		sigSignal(t, harness, id, "k", "go")
		eventually(t, func() bool { return seen.len() == 2 })
		wake := map[string]any{"reason": "signal", "signal": "test.answer", "key": "k"}
		wantRunning := map[string]any{"status": "running", "checkpoint": map[string]any{"phase": "woke"}, "wake": wake}
		if got := jsonOf(t, tkTaskRecord(t, harness, id).State); !reflect.DeepEqual(got, wantRunning) {
			t.Fatalf("state %v, want %v", got, wantRunning)
		}
		// Close stops the phase and writes the task back as pending, with its wake.
		mustClose(t, harness)
		signalWake := &durable.WakeReason{Reason: durable.WakeSignal, Signal: "test.answer", Key: "k"}
		if got, want := jsonOf(t, seen.all()), jsonOf(t, []*durable.WakeReason{signalWake, signalWake}); !reflect.DeepEqual(got, want) {
			t.Fatalf("seen %v, want %v", got, want)
		}
		reopened, _, _ := openTasks(t, tkOpenSqlite(t, path), []durable.AnyTask{keep.Erased()})
		wantPending := map[string]any{"status": "pending", "checkpoint": map[string]any{"phase": "woke"}, "wake": wake}
		if got := jsonOf(t, tkTaskRecord(t, reopened, id).State); !reflect.DeepEqual(got, wantPending) {
			t.Fatalf("state %v, want %v", got, wantPending)
		}
		reopened.Resume()
		tkWaitOutcome(t, reopened, id)
		if got, want := jsonOf(t, seen.all()), jsonOf(t, []*durable.WakeReason{signalWake, signalWake, signalWake, signalWake, nil}); !reflect.DeepEqual(got, want) {
			t.Fatalf("seen %v, want %v", got, want)
		}
		mustClose(t, reopened)
	})
}
