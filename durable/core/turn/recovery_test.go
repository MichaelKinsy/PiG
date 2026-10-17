package turn_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// Recovery (CONTRACT 3.8): a restart loses every volatile effect; the committed state decides what resumes.

// TestRecoverResendsAnUnansweredRequest: a model request that never answered is sent again after a restart, and the
// answer commits once.
func TestRecoverResendsAnUnansweredRequest(t *testing.T) {
	r := newRig(t)
	conv, _ := r.start("hi")
	r.resume()
	if len(r.requests) != 1 {
		t.Fatalf("requests = %d", len(r.requests))
	}
	first := viewText(r.requests[0].req.Context.(turn.ViewContext).View)
	r.restart()
	if len(r.requests) != 1 {
		t.Fatalf("requests after the restart = %d (reports %v)", len(r.requests), r.reports)
	}
	if got := viewText(r.requests[0].req.Context.(turn.ViewContext).View); got != first {
		t.Fatalf("resent context differs:\n%s\n%s", first, got)
	}
	r.finish(assistantMessage("stop", "hello"))
	n := 0
	for _, e := range r.entries(conv) {
		if str(e, "kind") == "pi.assistant" {
			n++
		}
	}
	if n != 1 || len(r.requests) != 0 {
		t.Fatalf("assistant entries = %d, requests = %d", n, len(r.requests))
	}
}

// TestRecoverRetryBackoffResumes: a restart during the retry backoff keeps the wake time; the request is sent when it
// passes, not before.
func TestRecoverRetryBackoffResumes(t *testing.T) {
	r := newRig(t)
	r.start("hi")
	r.resume()
	r.finish(`{"role":"assistant","content":[],"provider":"faux","model":"m","stopReason":"error","errorMessage":"Overloaded","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5}`)
	r.restart()
	if len(r.requests) != 0 {
		t.Fatal("the request was sent during the backoff")
	}
	r.advance(1999)
	if len(r.requests) != 0 {
		t.Fatal("the request was sent before the wake time")
	}
	r.advance(1)
	if len(r.requests) != 1 {
		t.Fatalf("requests after the wake time = %d (reports %v)", len(r.requests), r.reports)
	}
}

// TestRecoverRunningToolIsInterrupted: an unsafe tool that had started is not run again; its call settles as interrupted
// and the generation continues. A safe tool runs again.
func TestRecoverRunningTool(t *testing.T) {
	for _, replay := range []string{"", "safe"} {
		t.Run("replay="+replay, func(t *testing.T) {
			r := newRig(t)
			tool := echoTool()
			tool.Replay = replay
			r.agent.Tools = []turn.Tool{tool}
			r.start("hi")
			r.resume()
			r.finish(toolUseWith("echo", `{"text":"x"}`))
			if len(r.toolRuns) != 1 {
				t.Fatalf("tool runs = %d", len(r.toolRuns))
			}
			r.restart()
			if replay == "safe" {
				if len(r.toolRuns) != 1 {
					t.Fatalf("safe tool runs after the restart = %d (reports %v)", len(r.toolRuns), r.reports)
				}
				r.toolRuns[0].then(mustParse(t, `{"output":[{"type":"text","text":"again"}]}`), nil)
				r.run()
			} else if len(r.toolRuns) != 0 {
				t.Fatalf("an unsafe tool ran again")
			}
			got := lastEntryOf(r, "pi.tool-result")
			if replay == "safe" && !strings.Contains(got, `"text":"again"`) {
				t.Fatalf("safe result = %s", got)
			}
			if replay != "safe" && !strings.Contains(got, "interrupted") {
				t.Fatalf("unsafe result = %s", got)
			}
			if len(r.requests) != 1 {
				t.Fatalf("the generation did not continue: requests = %d (reports %v)", len(r.requests), r.reports)
			}
		})
	}
}

func lastEntryOf(r *rig, kind string) string {
	var out string
	for _, e := range r.entries(1) {
		if str(e, "kind") == kind {
			out = jv.Stringify(e)
		}
	}
	return out
}

// TestRecoverPartialBecomesAnAbortedEntry: a restart with a streamed partial in pi.live keeps the text as an aborted
// assistant entry (convertPartial) and sends the request again.
func TestRecoverPartialBecomesAnAbortedEntry(t *testing.T) {
	r := newRig(t)
	conv, _ := r.start("hi")
	r.resume()
	r.requests[0].onPartial(mustRaw(t, `{"role":"assistant","content":[{"type":"text","text":"half an ans"}],"provider":"faux","model":"m","stopReason":"stop","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5}`))
	r.run()
	r.advance(100)
	if live := r.doc(turn.LiveDef, conv); !strings.Contains(live, "half an ans") {
		t.Fatalf("partial not committed: %s", live)
	}
	r.restart()
	got := lastEntryOf(r, "pi.assistant")
	if !strings.Contains(got, "half an ans") || !strings.Contains(got, `"stopReason":"aborted"`) {
		t.Fatalf("partial entry = %s (reports %v)", got, r.reports)
	}
	if len(r.requests) != 1 {
		t.Fatalf("requests = %d", len(r.requests))
	}
}

func viewText(v *history.View) string {
	var b strings.Builder
	for i := 0; i < v.NumMessages(); i++ {
		b.Write(v.Message(i))
		b.WriteByte('\n')
	}
	return b.String()
}

// TestRecoverRerunsTheToolsPhaseInterruptedInAfterTools: a restart while an afterTools handler runs leaves the tools phase
// uncommitted, so the reopened phase calls afterTools again before it hands over to the next generation (harness-tools-recovery
// "reruns the generation tools phase interrupted before its commit").
func TestRecoverRerunsTheToolsPhaseInterruptedInAfterTools(t *testing.T) {
	r := newRig(t)
	r.agent.Tools = []turn.Tool{echoTool()}
	calls := 0
	var held func(any, error)
	// turnhost counts handlers from the agent it resolved in this process (h.agents), so a count is only right after Agent.
	resolved := false
	agent := r.host.Agent
	r.host.Agent = func(conv session.ID, cancel *session.Cancel, then func(*turn.Agent, error)) {
		resolved = true
		agent(conv, cancel, then)
	}
	r.host.Hooks = func(name string, _ session.ID) int {
		if name == "afterTools" && resolved {
			return 1
		}
		return 0
	}
	r.host.Hook = func(_ string, _ int, _, _ session.ID, _ *jv.Object, then func(any, error)) {
		calls++
		if calls == 1 {
			held = then
			return
		}
		then(nil, nil)
	}
	r.start("hi")
	r.resume()
	r.finish(toolUseWith("echo", `{"text":"x"}`))
	r.toolRuns[0].then(mustParse(t, `{"output":[{"type":"text","text":"ok"}]}`), nil)
	r.run()
	if calls != 1 || held == nil {
		t.Fatalf("afterTools calls before the restart = %d", calls)
	}
	resolved = false
	r.restart()
	if calls != 2 {
		t.Fatalf("afterTools calls after the restart = %d (reports %v)", calls, r.reports)
	}
	if len(r.requests) != 1 {
		t.Fatalf("the generation did not continue after the rerun: requests = %d (reports %v)", len(r.requests), r.reports)
	}
}

// TestDeferredResponsePollsDurablyAndSurvivesARestart: a deferred message commits the poll phase with its wake time and shows
// it in pi.live; nothing is fetched before that time, also across a restart; a still-deferred result polls strictly later and a
// final one answers the run (generation.ts classify/poll).
func TestDeferredResponsePollsDurablyAndSurvivesARestart(t *testing.T) {
	r := newRig(t)
	fetches := 0
	r.host.FetchDeferred = func(_ turn.ModelRef, handle *jv.Object, _ *session.Cancel, then func(*jv.Object, error)) {
		fetches++
		if fetches == 1 {
			then(mustParse(t, `{"role":"assistant","content":[],"provider":"faux","model":"m","stopReason":"deferred","deferred":{"id":"h1","pollAfterMs":500},"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":6}`), nil)
			return
		}
		then(mustParse(t, assistantMessage("stop", "polled")), nil)
	}
	r.start("hi")
	r.resume()
	r.finish(`{"role":"assistant","content":[],"provider":"faux","model":"m","stopReason":"deferred","deferred":{"id":"h1","pollAfterMs":1000},"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5}`)
	if got := r.doc(turn.LiveDef, 1); !strings.Contains(got, `"deferred":{"pollAt":2000}`) {
		t.Fatalf("live = %s", got)
	}
	r.restart()
	r.advance(999)
	if fetches != 0 {
		t.Fatalf("fetched before the poll time: %d", fetches)
	}
	r.advance(1)
	if fetches != 1 {
		t.Fatalf("fetches at the poll time = %d (reports %v)", fetches, r.reports)
	}
	if got := r.doc(turn.LiveDef, 1); !strings.Contains(got, `"deferred":{"pollAt":2500}`) {
		t.Fatalf("a still-deferred result polls pollAfterMs later: live = %s", got)
	}
	r.advance(499)
	if fetches != 1 {
		t.Fatalf("fetched again early: %d", fetches)
	}
	r.advance(1)
	if fetches != 2 {
		t.Fatalf("second fetch = %d (reports %v)", fetches, r.reports)
	}
	if got := lastEntryOf(r, "pi.assistant"); !strings.Contains(got, `"text":"polled"`) {
		t.Fatalf("answer = %s", got)
	}
}
