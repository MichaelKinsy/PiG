package turn_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// compactionRig has a root conversation with four exchanges and a manual compaction task waiting to run.
func compactionRig(t *testing.T) *rig {
	r := newRig(t)
	r.settings.Compaction.KeepRecentTokens = 1
	r.commit(func(tx *session.Tx) error {
		if _, err := tx.CreateRootConversation(); err != nil {
			return err
		}
		for i := range 4 {
			if _, err := tx.AppendEntry(1, mustParse(t, `{"kind":"pi.user","model":[{"role":"user","content":"question number `+string(rune('a'+i))+`","timestamp":1}]}`)); err != nil {
				return err
			}
			if _, err := tx.AppendEntry(1, mustParse(t, `{"kind":"pi.assistant","model":[`+assistantMessage("stop", "answer "+string(rune('a'+i)))+`]}`)); err != nil {
				return err
			}
		}
		live, err := turn.Runs{}.Live(tx, 1)
		if err != nil {
			return err
		}
		id, err := tx.CreateTask(turn.CompactionDef, mustParse(t, `{"reason":"manual"}`), session.TaskOptions{ConversationID: 1})
		if err != nil {
			return err
		}
		return turn.AddCompactionStatus(live, mustParse(t, `{"taskId":`+jv.Stringify(float64(id))+`,"trigger":"manual","blocking":false,"attempt":1}`))
	})
	return r
}

// TestCompactionSummarizesAndPlacesThroughAdmission: select pins the range, summarize sends the transcript with the
// summarization prompt, and a conversation-owned compaction places its pi.compaction entry (head = first kept) through a
// write submission; the status leaves pi.live and the summarizer's usage is recorded.
func TestCompactionSummarizesAndPlacesThroughAdmission(t *testing.T) {
	r := compactionRig(t)
	r.resume()
	if len(r.complete) != 1 {
		t.Fatalf("summarization requests = %d (reports %v)", len(r.complete), r.reports)
	}
	req := r.complete[0].req
	text := jv.Stringify(req.Context)
	if !strings.Contains(text, `[User]: question number a`) || !strings.Contains(text, "You are a context summarization assistant") {
		t.Fatalf("context = %s", text)
	}
	if got := jv.Stringify(req.Options); !strings.Contains(got, `"cacheRetention":"none"`) || !strings.Contains(got, `"sessionId":"session-1"`) {
		t.Fatalf("options = %s", got)
	}
	r.complete[0].then(mustParse(t, assistantMessage("stop", "  the summary  ")), nil)
	r.run()
	entries := r.entries(1)
	last := jv.Stringify(entries[len(entries)-1])
	if !strings.Contains(last, `"kind":"pi.compaction"`) || !strings.Contains(last, "<summary>\\nthe summary\\n</summary>") || !strings.Contains(last, `"data":{"reason":"manual"}`) {
		t.Fatalf("summary entry = %s", last)
	}
	if got := r.doc(turn.LiveDef, 1); got != `{}` {
		t.Fatalf("live = %s", got)
	}
	if got := outcomeOf(t, r.tasks(turn.KindCompaction)[0]); !strings.HasPrefix(got, `{"status":"completed","result":{"submissionId":`) {
		t.Fatalf("outcome = %s", got)
	}
	if got := r.doc(turn.UsageDef, 1); !strings.Contains(got, `"faux/m"`) {
		t.Fatalf("usage = %s", got)
	}
}

// TestCompactionRetriesATransientSummaryError: an overloaded answer backs off durably (the status shows the retry), then
// the next attempt summarizes again.
// TestContextReadKeepsItsRangeAfterTheScan: runtime.context receives the kept range when it is called, and the scheduler
// keeps the range the read returned only once readContextFrom returns, after the off-line scan (scheduler.ts:1231-1252): a
// second read whose bounds job runs during the first's scan must not see it.
func TestContextReadKeepsItsRangeAfterTheScan(t *testing.T) {
	r := compactionRig(t)
	r.scanHops = 7
	r.resume()
	want := []string{"kept-range", "context", "scan", "keep ended=false"}
	if len(r.ctxEvents) < len(want) || strings.Join(r.ctxEvents[:len(want)], ",") != strings.Join(want, ",") {
		t.Fatalf("context read order = %v, want it to start with %v", r.ctxEvents, want)
	}
}

func TestCompactionRetriesATransientSummaryError(t *testing.T) {
	r := compactionRig(t)
	r.resume()
	r.complete[0].then(mustParse(t, `{"role":"assistant","content":[],"provider":"faux","model":"m","stopReason":"error","errorMessage":"Overloaded","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5}`), nil)
	r.run()
	if got := r.doc(turn.LiveDef, 1); !strings.Contains(got, `"retry":{"at":3000,"error":"Overloaded"}`) {
		t.Fatalf("live during backoff = %s (reports %v)", got, r.reports)
	}
	r.advance(2000)
	if len(r.complete) != 2 {
		t.Fatalf("requests after the backoff = %d", len(r.complete))
	}
	if got := r.doc(turn.LiveDef, 1); !strings.Contains(got, `"attempt":2`) || strings.Contains(got, `"retry"`) {
		t.Fatalf("live on attempt 2 = %s", got)
	}
	if got := jv.Stringify(r.tasks(turn.KindCompaction)[0]); !strings.Contains(got, `"checkpoint":{"phase":"summarize","attempt":2,`) {
		t.Fatalf("task after the backoff = %s", got)
	}
	r.complete[1].then(mustParse(t, assistantMessage("stop", "ok")), nil)
	r.run()
	if got := outcomeOf(t, r.tasks(turn.KindCompaction)[0]); !strings.HasPrefix(got, `{"status":"completed"`) {
		t.Fatalf("outcome = %s", got)
	}
}

// TestCompactionDeclinedByAHookAndNothingToCompact.
func TestCompactionDeclinedByAHook(t *testing.T) {
	r := compactionRig(t)
	r.hooks["beforeCompact"] = func(_ int, payload *jv.Object) (any, error) {
		if got := jv.Stringify(payload); !strings.Contains(got, `"reason":"manual"`) || !strings.Contains(got, `"firstKept":`) {
			t.Errorf("payload = %s", got)
		}
		return mustParse(t, `{"decline":true}`), nil
	}
	r.resume()
	if len(r.complete) != 0 {
		t.Fatal("a declined compaction summarized")
	}
	if got := outcomeOf(t, r.tasks(turn.KindCompaction)[0]); got != `{"status":"completed","result":{}}` {
		t.Fatalf("outcome = %s", got)
	}
}

// thresholdRig is a conversation with a run about to prepare its request over four earlier exchanges, whose estimated size
// is above the blocking threshold (the rig's window is 128000, so reserve 128000 puts blocking at zero) or only above the
// background one (reserve 0, background 128000).
func thresholdRig(t *testing.T, reserve, background float64) *rig {
	r := newRig(t)
	r.settings.Compaction.KeepRecentTokens = 1
	r.settings.Compaction.ReserveTokens = reserve
	r.settings.Compaction.BackgroundTokens = background
	r.start("hi")
	r.commit(func(tx *session.Tx) error {
		for i := range 4 {
			if _, err := tx.AppendEntry(1, mustParse(t, `{"kind":"pi.user","model":[{"role":"user","content":"question number `+string(rune('a'+i))+`","timestamp":1}]}`)); err != nil {
				return err
			}
			if _, err := tx.AppendEntry(1, mustParse(t, `{"kind":"pi.assistant","model":[`+assistantMessage("stop", "answer "+string(rune('a'+i)))+`]}`)); err != nil {
				return err
			}
		}
		// The context ends in a message the model answers (a context that does not ends the run with nothing_to_answer).
		if _, err := tx.AppendEntry(1, mustParse(t, `{"kind":"pi.user","model":[{"role":"user","content":"and one more question","timestamp":1}]}`)); err != nil {
			return err
		}
		return nil
	})
	return r
}

// TestBlockingThresholdCompactsBeforeTheRequest: above contextWindow - reserveTokens the prepare commit creates a threshold
// compaction owned by the generation and waits for it; no model request is made until the compaction settled, and then the
// preparation runs again and sends the request (generation.ts thresholdCompaction, blocking).
func TestBlockingThresholdCompactsBeforeTheRequest(t *testing.T) {
	r := thresholdRig(t, 128000, 0)
	r.resume()
	if len(r.requests) != 0 {
		t.Fatalf("a request was sent before the blocking compaction ran")
	}
	tasks := r.tasks(turn.KindCompaction)
	if len(tasks) != 1 || !strings.Contains(jv.Stringify(tasks[0]), `"reason":"threshold"`) || !strings.Contains(jv.Stringify(tasks[0]), `"owner":`) {
		t.Fatalf("compaction tasks = %v (reports %v)", tasks, r.reports)
	}
	gen := r.tasks(turn.KindGeneration)[0]
	if got := jv.Stringify(gen); !strings.Contains(got, `"status":"waiting"`) || !strings.Contains(got, `"compacted":`) {
		t.Fatalf("generation = %s", got)
	}
	if len(r.complete) != 1 {
		t.Fatalf("summarization requests = %d", len(r.complete))
	}
	r.complete[0].then(mustParse(t, assistantMessage("stop", "the summary")), nil)
	r.run()
	if len(r.requests) != 1 {
		t.Fatalf("requests after the compaction = %d (reports %v)", len(r.requests), r.reports)
	}
	if n := len(r.tasks(turn.KindCompaction)); n != 1 {
		t.Fatalf("the prepare ran the threshold again: %d compactions", n)
	}
}

// TestBackgroundThresholdCompactsBesideTheRequest: between the background and blocking thresholds the request goes out at
// once and a conversation-owned threshold compaction runs beside it, shown in pi.live.compactions.
func TestBackgroundThresholdCompactsBesideTheRequest(t *testing.T) {
	r := thresholdRig(t, 0, 128000)
	r.resume()
	if len(r.requests) != 1 {
		t.Fatalf("requests = %d (reports %v)", len(r.requests), r.reports)
	}
	tasks := r.tasks(turn.KindCompaction)
	if len(tasks) != 1 || strings.Contains(jv.Stringify(tasks[0]), `"owner":`) {
		t.Fatalf("a background compaction is owned by its conversation: %v", tasks)
	}
	if got := r.doc(turn.LiveDef, 1); !strings.Contains(got, `"compactions":[{"taskId":`) || !strings.Contains(got, `"trigger":"threshold"`) || !strings.Contains(got, `"blocking":false`) {
		t.Fatalf("live = %s", got)
	}
	if len(r.complete) != 1 {
		t.Fatalf("summarization requests = %d", len(r.complete))
	}
}

// TestCompactionPolicyLayers is the layering of per-conversation compaction settings (harness-compaction.test.ts "per-conversation
// compaction settings"): a model-specific value beats a general one and the conversation beats the Harness at the same level, and the
// conversation's own enabled: false beats the Harness's model override unless its own model override says otherwise.
func TestCompactionPolicyLayers(t *testing.T) {
	num := func(v float64) *float64 { return &v }
	flag := func(v bool) *bool { return &v }
	model := turn.ModelRef{Provider: "faux", ModelID: "faux-1"}
	reserve := func(v float64) history.PolicyOverride { return history.PolicyOverride{ReserveTokens: num(v)} }
	cases := []struct {
		name            string
		harness         map[string]history.PolicyOverride
		conversation    *turn.AgentCompaction
		reserve         float64
		enabled, hasOff bool
	}{
		{name: "none", reserve: 1000},
		{name: "conversation general", conversation: &turn.AgentCompaction{Own: reserve(500)}, reserve: 500},
		{
			name: "harness model beats conversation general", harness: map[string]history.PolicyOverride{"faux/faux-1": reserve(250)},
			conversation: &turn.AgentCompaction{Own: reserve(500)}, reserve: 250,
		},
		{
			name: "conversation model beats harness model", harness: map[string]history.PolicyOverride{"faux/faux-1": reserve(250)},
			conversation: &turn.AgentCompaction{Own: reserve(500), Overrides: map[string]history.PolicyOverride{"faux/faux-1": reserve(125)}}, reserve: 125,
		},
		{
			name:         "conversation override of another model is ignored",
			conversation: &turn.AgentCompaction{Own: reserve(500), Overrides: map[string]history.PolicyOverride{"faux/other": reserve(125)}}, reserve: 500,
		},
		{
			name: "conversation enabled false beats harness model override", harness: map[string]history.PolicyOverride{"faux/faux-1": {Enabled: flag(true)}},
			conversation: &turn.AgentCompaction{Own: history.PolicyOverride{Enabled: flag(false)}}, reserve: 1000, hasOff: true,
		},
		{
			name: "conversation model override beats its own enabled false", harness: map[string]history.PolicyOverride{"faux/faux-1": {Enabled: flag(true)}},
			conversation: &turn.AgentCompaction{
				Own:       history.PolicyOverride{Enabled: flag(false)},
				Overrides: map[string]history.PolicyOverride{"faux/faux-1": {Enabled: flag(true)}},
			}, reserve: 1000, enabled: true,
		},
	}
	for _, c := range cases {
		settings := turn.Settings{Compaction: history.Policy{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 400, BackgroundTokens: 800}, CompactionOverrides: c.harness}
		got := settings.CompactionFor(model, c.conversation)
		wantEnabled := !c.hasOff || c.enabled
		if got.ReserveTokens != c.reserve || got.Enabled != wantEnabled {
			t.Errorf("%s: policy = %+v, want reserve %v enabled %v", c.name, got, c.reserve, wantEnabled)
		}
	}
}
