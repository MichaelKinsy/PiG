// Ports packages/durable/test/examples/39-ask-a-person.ts: a plugin task that asks a person and sleeps until the answer
// comes or a deadline passes, holding no process meanwhile. The first question is answered; the second expires, and its
// late answer is refused. The script's printing is the transcript this test reads.
//
// Go mapping: upstream's pauses of a few milliseconds become settledWake and a wait for the last onWake; the Harness
// clock is read from several goroutines, so it is atomic.

package examples_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/schema"
)

type answerValue struct {
	Text string `json:"text"`
}

type askInput struct {
	Question string `json:"question"`
}

type askState struct {
	Phase string `json:"phase"`
}

type askResult struct {
	Answer *string `json:"answer"`
}

// Pi: packages/durable/src/signals.ts, packages/durable/src/harness/scheduler.ts (sleep deadlines, onWake).
func TestExample39AskAPerson(t *testing.T) {
	const hour = 60 * 60 * 1000

	// The Harness clock, moved by hand so the deadline comes without waiting four hours.
	var clock atomic.Int64
	start := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.UTC).UnixMilli()
	clock.Store(start)
	answerSignal := durable.DefineSignal(durable.SignalDefinition[answerValue]{
		Name: "example.answer", Version: 1, Schema: schema.Obj(schema.Properties{schema.Prop("text", schema.Str())}),
	})

	// What shows the question to a person (an inbox, a card, a chat bridge) keeps its own record of it: Durable stores
	// no description of what a sleep waits for, only its keys and deadline.
	var inboxMu sync.Mutex
	inbox := map[durable.TaskId]string{}
	inboxQuestions := func() []string {
		inboxMu.Lock()
		defer inboxMu.Unlock()
		questions := []string{}
		for _, question := range inbox {
			questions = append(questions, question)
		}
		slices.Sort(questions)
		return questions
	}

	type askRuntime = durable.TaskRuntime[askInput, askState, askResult, any]
	type askTask = durable.RunningTask[askInput, askState, askResult]
	type askNext = durable.NextTaskState[askState, askResult]
	var askLines printed
	askPerson := durable.DefineTask(durable.TaskDefinition[askInput, askState, askResult, any]{
		Name:    "example.ask-person",
		Version: 1,
		Initial: func(askInput) askState { return askState{Phase: "ask"} },
		Phases: map[string]durable.PhaseHandler[askInput, askState, askResult, any]{
			"ask": func(ctx context.Context, task askTask, runtime askRuntime) error {
				inboxMu.Lock()
				inbox[runtime.TaskId()] = task.Input.Question
				inboxMu.Unlock()
				// Sleep until the answer arrives or four hours pass, whichever comes first.
				return runtime.Commit(ctx, func(durable.Tx, askTask) (*askNext, error) {
					until := runtime.Now() + 4*hour
					return &askNext{Status: durable.TaskSleeping, Signals: []durable.SignalKey{answerSignal.Key("answer")}, Until: &until, Checkpoint: &askState{Phase: "answered"}}, nil
				})
			},
			"answered": func(ctx context.Context, task askTask, runtime askRuntime) error {
				inboxMu.Lock()
				delete(inbox, runtime.TaskId())
				inboxMu.Unlock()
				// `signal` when answered, `expired` when the deadline came first.
				state, err := durable.ReadSignal(ctx, runtime, answerSignal, "answer")
				if err != nil {
					return err
				}
				var answer *string
				if state != nil && !state.Expired {
					answer = &state.Value.Text
				}
				reason := "undefined"
				if task.State.Wake != nil {
					reason = string(task.State.Wake.Reason)
				}
				askLines.add(fmt.Sprintf("  [ask] woke: %s, read: %s", reason, signalStateText(state)))
				return runtime.Commit(ctx, func(durable.Tx, askTask) (*askNext, error) {
					return &askNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[askResult]{Status: durable.OutcomeCompleted, Result: &askResult{Answer: answer}}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ askTask, runtime askRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, askTask) (*askNext, error) {
				return &askNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[askResult]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})

	// A host that wakes the process itself, as a Durable Object alarm does: it is told when work needs a process.
	var wakesMu sync.Mutex
	var wakes []harness.Wake
	registry := harness.CreateRegistry()
	installed(t, registry, &durable.Extension{Name: "ask", Tasks: []durable.AnyTask{askPerson.Erased()}})
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{
		Models: ai.CreateModels(), Registry: registry,
		Now: func() float64 { return float64(clock.Load()) },
		OnWake: func(wake harness.Wake) {
			wakesMu.Lock()
			defer wakesMu.Unlock()
			wakes = append(wakes, wake)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, nil))
	ask := func(question string) durable.TaskId {
		return commit(t, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, askPerson, askInput{Question: question}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		})
	}

	var script printed
	// 1. Answered in time.
	first := ask("Deploy to production?")
	opened.Resume()
	waitSleeping(t, opened, first)
	script.add(fmt.Sprintf("inbox: %q", inboxQuestions()))
	// Nothing runs: no process needed until the deadline, four hours from now.
	wake := settledWake(opened)
	if wake.At == nil {
		t.Fatalf("wake while asleep = %s, want a deadline", wakeText(wake))
	}
	script.add(fmt.Sprintf("wake while asleep: %s deadline in %v h", wakeText(wake), (*wake.At-float64(clock.Load()))/hour))
	script.add("deliver: " + string(must(harness.Signal(background, opened, first, answerSignal, "answer", answerValue{Text: "yes"}))))
	script.add("result: " + outcomeText(waitTask(t, opened, first)))

	// 2. Nobody answers. The host's alarm fires at the deadline and resumes the Harness.
	second := ask("Delete the staging database?")
	waitSleeping(t, opened, second)
	script.add(fmt.Sprintf("inbox: %q", inboxQuestions()))
	wake = settledWake(opened)
	if wake.At == nil {
		t.Fatalf("wake while asleep = %s, want a deadline", wakeText(wake))
	}
	clock.Store(int64(*wake.At))
	opened.Resume()
	script.add("result: " + outcomeText(waitTask(t, opened, second)))
	// The key expired with the deadline: a late answer is refused.
	script.add("late deliver: " + string(must(harness.Signal(background, opened, second, answerSignal, "answer", answerValue{Text: "yes"}))))
	// onWake runs off the commit that changed the value, in a goroutine of its own.
	eventually(t, func() bool {
		wakesMu.Lock()
		defer wakesMu.Unlock()
		return len(wakes) > 0 && wakes[len(wakes)-1] == harness.Wake{}
	})
	wakesMu.Lock()
	script.add("last onWake: " + wakeText(wakes[len(wakes)-1]))
	wakesMu.Unlock()

	expectEqual(t, "script", script.all(), []string{
		`inbox: ["Deploy to production?"]`,
		fmt.Sprintf(`wake while asleep: {"active":false,"at":%d} deadline in 4 h`, start+4*hour),
		"deliver: delivered",
		`result: {"status":"completed","result":{"answer":"yes"}}`,
		`inbox: ["Delete the staging database?"]`,
		`result: {"status":"completed","result":{"answer":null}}`,
		"late deliver: closed",
		`last onWake: {"active":false}`,
	})
	expectEqual(t, "ask", askLines.all(), []string{
		`  [ask] woke: signal, read: {"value":{"text":"yes"}}`,
		`  [ask] woke: expired, read: {"expired":true}`,
	})
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}
