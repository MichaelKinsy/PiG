package turn_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// abortTask marks the task and runs the loop until the abort invocation ends.
func (r *rig) abortTask(id session.ID) string {
	r.t.Helper()
	var got string
	done := false
	r.loop.Line(func() error { return nil }, func(error) {
		r.sched.Abort(id, false, nil, func(result string, err error) {
			if err != nil {
				r.t.Fatalf("abort: %v", err)
			}
			got, done = result, true
		})
	})
	r.run()
	if !done {
		r.t.Fatal("abort did not finish")
	}
	return got
}

func taskID(t *testing.T, task *jv.Object) session.ID {
	id, _ := task.Get("id")
	return session.ID(id.(float64))
}

const twoCalls = `{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"echo","arguments":{"text":"a"}},{"type":"toolCall","id":"call_2","name":"echo","arguments":{"text":"b"}}],"provider":"faux","model":"m","stopReason":"toolUse","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5}`

// TestAbortKeepsThePartialAsAnAbortedEntry: aborting a streaming generation cancels the request, appends the committed
// partial as an aborted assistant entry, ends the run unanswered and ends the task aborted (generation.ts abort).
func TestAbortKeepsThePartialAsAnAbortedEntry(t *testing.T) {
	r := newRig(t)
	conv, submission := r.start("hi")
	r.resume()
	r.requests[0].onPartial(mustRaw(t, `{"role":"assistant","content":[{"type":"text","text":"half"}],"provider":"faux","model":"m","stopReason":"stop","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5}`))
	r.run()
	r.advance(100)
	task := r.tasks(turn.KindGeneration)[0]
	if got := r.abortTask(taskID(t, task)); got != "marked" {
		t.Fatalf("abort result = %q", got)
	}
	if !r.requests[0].cancel.Aborted() {
		t.Fatal("the model request was not cancelled")
	}
	if got := lastEntryOf(r, "pi.assistant"); !strings.Contains(got, "half") || !strings.Contains(got, `"stopReason":"aborted"`) {
		t.Fatalf("partial entry = %s (reports %v)", got, r.reports)
	}
	if got := outcomeOf(t, r.tasks(turn.KindGeneration)[0]); got != `{"status":"aborted"}` {
		t.Fatalf("outcome = %s", got)
	}
	if got := r.doc(turn.LiveDef, conv); got != `{}` {
		t.Fatalf("live = %s", got)
	}
	sub, _ := r.mem.Submission(submission)
	if got := jv.Stringify(sub); !strings.Contains(got, `"status":"unanswered"`) || !strings.Contains(got, `"reason":"aborted"`) {
		t.Fatalf("submission = %s", got)
	}
}

// TestAbortAfterASequentialRoundStartedEndsUnstartedCallsAborted: in a sequential round the second call never started when
// the abort lands; the tool that ran is aborted by its owner and the unstarted call gets an `aborted` harness result in call order.
func TestAbortAfterASequentialRoundStartedEndsUnstartedCallsAborted(t *testing.T) {
	r := newRig(t)
	r.settings.ToolExecution = "sequential"
	r.agent.Tools = []turn.Tool{echoTool()}
	r.start("hi")
	r.resume()
	r.finish(twoCalls)
	if len(r.toolRuns) != 1 {
		t.Fatalf("tool runs = %d (reports %v)", len(r.toolRuns), r.reports)
	}
	task := r.tasks(turn.KindGeneration)[0]
	r.abortTask(taskID(t, task))
	if !r.toolRuns[0].cancel.Aborted() {
		t.Fatal("the running tool was not cancelled")
	}
	results := 0
	var texts []string
	for _, e := range r.entries(1) {
		if str(e, "kind") == "pi.tool-result" {
			results++
			texts = append(texts, jv.Stringify(e))
		}
	}
	if results != 2 {
		t.Fatalf("tool results = %d: %v (reports %v)", results, texts, r.reports)
	}
	if !strings.Contains(texts[0], `"toolCallId":"call_1"`) || !strings.Contains(texts[1], `"toolCallId":"call_2"`) || !strings.Contains(texts[1], "Tool echo was aborted") {
		t.Fatalf("results = %v", texts)
	}
	if got := outcomeOf(t, r.tasks(turn.KindGeneration)[0]); got != `{"status":"aborted"}` {
		t.Fatalf("outcome = %s", got)
	}
	if len(r.requests) != 0 {
		t.Fatal("an aborted generation asked the model again")
	}
}

// TestSchedulerOutcomeSettlesTheRunUnanswered: a generation the scheduler faults (its phase threw) or orphans settles
// its run's input unanswered in the settling transaction, with the error message as detail, after its partial turned
// into an aborted entry (live.ts settleSchedulerOutcome).
func TestSchedulerOutcomeSettlesTheRunUnanswered(t *testing.T) {
	for _, tc := range []struct{ outcome, want string }{
		{`{"status":"faulted","error":{"message":"boom"}}`, `"reason":"faulted","detail":"boom"`},
		{`{"status":"orphaned","reason":"missing_task"}`, `"reason":"missing_task"`},
	} {
		r := newRig(t)
		conv, submission := r.start("hi")
		r.resume()
		r.requests[0].onPartial(mustRaw(t, `{"role":"assistant","content":[{"type":"text","text":"half"}],"provider":"faux","model":"m","stopReason":"stop","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5}`))
		r.run()
		r.advance(100)
		task := r.tasks(turn.KindGeneration)[0]
		if _, err := r.sess.Commit(func(tx *session.Tx) error {
			return turn.SettleSchedulerOutcome(tx, task, mustParse(t, tc.outcome))
		}, session.TxScope{}); err != nil {
			t.Fatal(err)
		}
		sub, _ := r.mem.Submission(submission)
		if got := jv.Stringify(sub); !strings.Contains(got, `"status":"unanswered"`) || !strings.Contains(got, tc.want) {
			t.Fatalf("%s: submission = %s", tc.outcome, got)
		}
		if got := lastEntryOf(r, "pi.assistant"); !strings.Contains(got, "half") || !strings.Contains(got, `"stopReason":"aborted"`) {
			t.Fatalf("%s: partial entry = %s", tc.outcome, got)
		}
		if got := r.doc(turn.LiveDef, conv); got != `{}` {
			t.Fatalf("%s: live = %s", tc.outcome, got)
		}
	}
}

// A task of another kind, or a generation that does not hold `run`, leaves pi.live alone.
func TestSchedulerOutcomeIgnoresWhatIsNotTheRun(t *testing.T) {
	r := newRig(t)
	conv, _ := r.start("hi")
	r.resume()
	r.run()
	before := r.doc(turn.LiveDef, conv)
	other := mustParse(t, `{"id":999,"kind":"app.other","conversationId":`+jv.Stringify(float64(conv))+`}`)
	stale := mustParse(t, `{"id":998,"kind":"pi.generation","conversationId":`+jv.Stringify(float64(conv))+`}`)
	if _, err := r.sess.Commit(func(tx *session.Tx) error {
		for _, rec := range []*jv.Object{other, stale} {
			if err := turn.SettleSchedulerOutcome(tx, rec, mustParse(t, `{"status":"faulted","error":{"message":"x"}}`)); err != nil {
				return err
			}
		}
		return nil
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if after := r.doc(turn.LiveDef, conv); after != before {
		t.Fatalf("live changed: %s -> %s", before, after)
	}
}

// runtime.hooks.each (scheduler.ts): a handler that throws is reported and the next one runs; the run still answers.
func TestAHookThatThrowsIsReportedAndTheNextOneRuns(t *testing.T) {
	r := newRig(t)
	r.hookCount = map[string]int{"afterResponse": 2}
	var called []int
	r.hooks["afterResponse"] = func(i int, _ *jv.Object) (any, error) {
		called = append(called, i)
		if i == 0 {
			return nil, errors.New("observer failed")
		}
		return nil, nil
	}
	conv, submission := r.start("hi")
	r.resume()
	r.finish(assistantMessage("stop", "hello"))
	if len(called) != 2 {
		t.Fatalf("handlers called: %v", called)
	}
	if len(r.reports) != 1 || r.reports[0] != "observer failed" {
		t.Fatalf("reports: %v", r.reports)
	}
	sub, _ := r.mem.Submission(submission)
	if got := jv.Stringify(sub); !strings.Contains(got, `"status":"done"`) {
		t.Fatalf("the run did not answer: %s", got)
	}
	_ = conv
}

// beforeTool is the exception: a throw blocks the call with its message and later handlers do not run.
func TestABeforeToolThrowBlocksTheCallAndSkipsLaterHandlers(t *testing.T) {
	r := newRig(t)
	r.hookCount = map[string]int{"beforeTool": 2}
	var called []int
	r.hooks["beforeTool"] = func(i int, _ *jv.Object) (any, error) {
		called = append(called, i)
		return nil, errors.New("no way")
	}
	r.toolRound(`{}`)
	if len(r.toolRuns) != 0 || len(called) != 1 {
		t.Fatalf("tool runs %d, handlers called %v", len(r.toolRuns), called)
	}
	if last := lastEntry(r); !strings.Contains(last, `Tool call blocked: no way`) {
		t.Fatalf("result entry = %s", last)
	}
	if len(r.reports) != 0 {
		t.Fatalf("a blocking throw is not a report: %v", r.reports)
	}
}

// TestAStreamThatCannotHandOverItsFinalMessageFaultsTheRun: the host answers the request with an error instead of a message
// (its final message was not strict JSON). The task faults with that text, its input settles unanswered "faulted" and the
// committed partial becomes an aborted entry (settleSchedulerOutcome); nothing is answered or retried.
func TestAStreamThatCannotHandOverItsFinalMessageFaultsTheRun(t *testing.T) {
	r := newRig(t)
	r.start("hi")
	r.resume()
	if len(r.requests) != 1 {
		t.Fatalf("requests = %d", len(r.requests))
	}
	req := r.requests[0]
	r.requests = nil
	req.then(nil, errors.New("Cannot copy a non-JSON function"))
	r.run()
	gens := r.tasks(turn.KindGeneration)
	if len(gens) != 1 || !strings.Contains(outcomeOf(t, gens[0]), `"status":"faulted"`) || !strings.Contains(outcomeOf(t, gens[0]), "non-JSON function") {
		t.Fatalf("generation outcome = %s (reports %v)", outcomeOf(t, gens[0]), r.reports)
	}
	if len(r.requests) != 0 {
		t.Fatalf("the faulted run requested again")
	}
}
