package turn_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// rig runs the pi.generation task over a Session on the in-memory store with a scripted Host: model requests stay
// pending until the test finishes them, and timers fire when the test says.
type rig struct {
	t    *testing.T
	mem  *sessiontest.MemStore
	sess *session.Session
	loop *session.Loop
	hops []int // the hops chains the turn asked for, in order
	ctxProbe
	sched *session.Scheduler
	host  *turn.Host
	reg   *registry
	now   float64

	settings turn.Settings
	agent    turn.Agent
	hooks    map[string]func(index int, payload *jv.Object) (any, error)
	// hookCount is how many handlers a hook has (default 1 when hooks[name] is set).
	hookCount map[string]int

	toolRuns []*pendingTool
	complete []*pendingComplete
	requests []*pendingRequest
	timers   []*pendingTimer
	reports  []string
	renders  int
}

type pendingRequest struct {
	req       *turn.StreamRequest
	onPartial func(jv.Raw)
	then      func(*jv.Object, error)
	cancel    *session.Cancel
}

type pendingComplete struct {
	req  *turn.CompleteRequest
	then func(*jv.Object, error)
}

type pendingTool struct {
	run    *turn.ToolRun
	io     *turn.ToolIO
	then   func(*jv.Object, error)
	cancel *session.Cancel
}

// scanHops, when set, makes the Context host report a range scan of that many hops and a keep; ctxEvents records the
// order of KeptRange, Context, the scan's hops and keep.
type ctxProbe struct {
	scanHops  int
	ctxEvents []string
}

type pendingTimer struct {
	at   float64
	fire func()
	dead bool
}

type registry struct{ impls map[string]*session.TaskImpl }

func (r *registry) Task(name string) *session.TaskImpl { return r.impls[name] }

// mustRaw is the text of a partial message, as the host hands it to the request: a JSON object.
func mustRaw(t testing.TB, text string) jv.Raw {
	t.Helper()
	mustParse(t, text)
	return jv.Raw(text)
}

func mustParse(t testing.TB, text string) *jv.Object {
	t.Helper()
	v, err := jv.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jv.Object)
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, mem: sessiontest.New(), loop: &session.Loop{}, now: 1000, hooks: map[string]func(int, *jv.Object) (any, error){}}
	r.settings = turn.Settings{
		RetryEnabled: true, MaxRetries: 3, BaseDelayMs: 2000,
		Compaction:        history.Policy{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000, BackgroundTokens: 32768},
		PartialIntervalMs: 100, OutputIntervalMs: 100, ToolExecution: "parallel",
	}
	r.agent = turn.Agent{Model: &turn.ModelRef{Provider: "faux", ModelID: "m"}, ThinkingLevel: "off"}
	r.sess = session.New(r.mem, r.mem.NextID(), func() float64 { return r.now })
	h := &turn.Host{
		Now:      func() float64 { return r.now },
		Settings: func() turn.Settings { return r.settings },
		Agent:    func(_ session.ID, _ *session.Cancel, then func(*turn.Agent, error)) { then(&r.agent, nil) },
		ContextWindow: func(m turn.ModelRef) (float64, bool) {
			return 128000, m.Provider == "faux"
		},
		KeptRange: func(session.ID) turn.KeptRange {
			r.ctxEvents = append(r.ctxEvents, "kept-range")
			return turn.KeptRange{}
		},
		Context: func(conv, at session.ID, _ turn.KeptRange) (*history.View, int, func(bool) error, error) {
			view, err := r.context(conv, at)
			if r.scanHops == 0 {
				return view, 0, nil, err
			}
			r.ctxEvents = append(r.ctxEvents, "context")
			return view, r.scanHops, func(ended bool) error {
				r.ctxEvents = append(r.ctxEvents, fmt.Sprintf("keep ended=%v", ended))
				return nil
			}, err
		},
		Render: func(_, _ session.ID, _ *turn.Agent, shown *turn.Sections, _ *session.Cancel, then func(*turn.Sections, error)) {
			r.renders++
			then(shown, nil)
		},
		Stream: func(req *turn.StreamRequest, onPartial func(jv.Raw), cancel *session.Cancel, then func(*jv.Object, error)) {
			then = abortable(cancel, then)
			r.requests = append(r.requests, &pendingRequest{req, onPartial, then, cancel})
		},
		PrepareArguments: func(_ string, args *jv.Object, then func(any, string, error)) { then(args, "", nil) },
		ValidateArguments: func(_ string, _ *jv.Object, args any, then func(*jv.Object, string, error)) {
			then(args.(*jv.Object), "", nil)
		},
		RunTool: func(run *turn.ToolRun, io *turn.ToolIO, cancel *session.Cancel, then func(*jv.Object, error)) {
			then = abortable(cancel, then)
			r.toolRuns = append(r.toolRuns, &pendingTool{run, io, then, cancel})
		},
		MaxTokens: func(turn.ModelRef) float64 { return 0 },
		Complete: func(req *turn.CompleteRequest, _ *session.Cancel, then func(*jv.Object, error)) {
			r.complete = append(r.complete, &pendingComplete{req, then})
		},
		Hooks: func(name string, _ session.ID) int {
			if n, ok := r.hookCount[name]; ok {
				return n
			}
			if _, ok := r.hooks[name]; ok {
				return 1
			}
			return 0
		},
		Hook: func(name string, i int, _, _ session.ID, payload *jv.Object, then func(any, error)) {
			v, err := r.hooks[name](i, payload)
			then(v, err)
		},
		Hops: func(n int, then func()) {
			r.hops = append(r.hops, n)
			if n == r.scanHops && n > 0 {
				r.ctxEvents = append(r.ctxEvents, "scan")
			}
			r.loop.Defer(then)
		},
		SnapshotLoaded: func(def *session.DocDef, conv session.ID) bool {
			return r.sess.Loaded(def, session.DocArgs{Owner: conv})
		},
		Snapshot: func(def *session.DocDef, conv session.ID) (*jv.Object, error) {
			return r.sess.Snapshot(def, session.DocArgs{Owner: conv})
		},
		Entry: func(id session.ID) (*jv.Object, error) {
			e, _, err := r.mem.Entry(id)
			return e, err
		},
		AddTools:     func(*session.Tx, session.ID, []any) error { return nil },
		NewSessionID: func() string { return "session-1" },
		After: func(ms float64, fire func()) func() {
			tm := &pendingTimer{at: r.now + ms, fire: fire}
			r.timers = append(r.timers, tm)
			return func() { tm.dead = true }
		},
	}
	r.host = h
	gen := &turn.Generation{Host: h}
	reg := &registry{impls: map[string]*session.TaskImpl{}}
	for _, impl := range turn.Machines(h) {
		reg.impls[impl.Def.Name] = impl
	}
	_ = gen
	r.reg = reg
	r.boot()
	return r
}

// boot builds a Scheduler over the current Session.
func (r *rig) boot() {
	reg := r.reg
	r.sched = session.NewScheduler(r.sess, r.loop, session.Env{
		Snapshot:       func() session.Snapshot { return reg },
		Report:         func(err error, _ *jv.Object) { r.reports = append(r.reports, err.Error()) },
		Now:            func() float64 { return r.now },
		SettleOutcome:  turn.SettleSchedulerOutcome,
		WithdrawInputs: func(*session.Tx, session.ID) error { return nil },
		Timer:          r.timer,
		WakeTimer:      r.timer,
	})
}

// timer queues a pending host timer that the test fires by advancing the clock; the scheduler's retry and poll sleeps wake
// through WakeTimer, as production wires it in core/sched.go.
func (r *rig) timer(at float64, fire func()) func() {
	tm := &pendingTimer{at: at, fire: fire}
	r.timers = append(r.timers, tm)
	return func() { tm.dead = true }
}

// restart simulates a crash: volatile state (pending model requests, tool runs, timers) is lost, the store survives, and a new
// Session and Scheduler open over it.
func (r *rig) restart() {
	r.t.Helper()
	r.loop = &session.Loop{}
	r.requests, r.toolRuns, r.complete, r.timers = nil, nil, nil, nil
	r.sess = session.New(r.mem, r.mem.NextID(), func() float64 { return r.now })
	r.boot()
	r.resume()
}

// context derives the committed context of a conversation from the store's entries.
func (r *rig) context(conv, at session.ID) (*history.View, error) {
	st := history.NewStore()
	st.AddConversation(history.ConvRecord{ID: int64(conv)}, true)
	page, err := r.mem.ScanEntries(session.EntryQuery{ConversationID: conv, Ascending: true}, 1000, 0)
	if err != nil {
		return nil, err
	}
	for _, e := range page.Items {
		id, _ := e.Get("id")
		if err := st.Append(int64(conv), int64(id.(float64)), 1, []byte(jv.Stringify(e))); err != nil {
			return nil, err
		}
	}
	v, need, err := st.Context(int64(conv), int64(at))
	if need != nil {
		return nil, errors.New("context needs rows")
	}
	return v, err
}

func (r *rig) run() {
	r.t.Helper()
	if err := r.loop.Run(); err != nil {
		r.t.Fatalf("loop: %v", err)
	}
}

func (r *rig) commit(change func(tx *session.Tx) error) {
	r.t.Helper()
	r.loop.Line(func() error {
		_, err := r.sess.Commit(change, session.TxScope{})
		return err
	}, func(err error) {
		if err != nil {
			r.t.Fatalf("commit: %v", err)
		}
	})
	r.run()
}

// start creates the root conversation with one user entry and a run for a placed input submission; it returns the submission ID.
func (r *rig) start(text string) (conv, submission session.ID) {
	r.t.Helper()
	r.commit(func(tx *session.Tx) error {
		c, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		id, _ := c.Get("id")
		conv = session.ID(id.(float64))
		return nil
	})
	r.commit(func(tx *session.Tx) error {
		sub, err := tx.CreateSubmission(mustParse(r.t, `{"conversationId":1,"type":"input","status":"placed","requestId":"r1"}`))
		if err != nil {
			return err
		}
		sid, _ := sub.Get("id")
		submission = session.ID(sid.(float64))
		if _, err := tx.AppendEntry(1, mustParse(r.t, `{"kind":"pi.user","model":[{"role":"user","content":"`+text+`","timestamp":1}]}`)); err != nil {
			return err
		}
		live, err := turn.Runs{}.Live(tx, 1)
		if err != nil {
			return err
		}
		return turn.Runs{}.Start(tx, 1, live, []session.ID{submission})
	})
	return conv, submission
}

func (r *rig) resume() {
	r.sched.Open(func(err error) {
		if err != nil {
			r.t.Fatal(err)
		}
	})
	r.sched.Resume()
	r.run()
}

// finish completes the oldest pending model request with message.
func (r *rig) finish(message string) {
	r.t.Helper()
	if len(r.requests) == 0 {
		r.t.Fatal("no pending model request")
	}
	req := r.requests[0]
	r.requests = r.requests[1:]
	req.then(mustParse(r.t, message), nil)
	r.run()
}

// fireTimers fires every live timer due at or before the current time, advancing the clock to the next due one first.
func (r *rig) advance(ms float64) {
	r.now += ms
	for {
		fired := false
		for _, tm := range r.timers {
			if !tm.dead && tm.at <= r.now {
				tm.dead = true
				tm.fire()
				fired = true
				r.run()
			}
		}
		if !fired {
			return
		}
	}
}

func (r *rig) entries(conv session.ID) []*jv.Object {
	r.t.Helper()
	page, err := r.mem.ScanEntries(session.EntryQuery{ConversationID: conv, Ascending: true}, 1000, 0)
	if err != nil {
		r.t.Fatal(err)
	}
	return page.Items
}

// tasks returns the tasks of a kind in ID order.
func (r *rig) tasks(kind string) []*jv.Object {
	r.t.Helper()
	page, err := r.mem.ScanTasks(session.TaskQuery{ConversationID: 1, Kind: kind}, 100, 0)
	if err != nil {
		r.t.Fatal(err)
	}
	return page.Items
}

func (r *rig) doc(def *session.DocDef, conv session.ID) string {
	v, err := r.sess.Snapshot(def, session.DocArgs{Owner: conv})
	if err != nil {
		r.t.Fatal(err)
	}
	return jv.Stringify(v)
}

func assistantMessage(stop, text string) string {
	return `{"role":"assistant","content":[{"type":"text","text":"` + text + `"}],"provider":"faux","model":"m","stopReason":"` + stop +
		`","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":15,"cost":{"input":0.1,"output":0.2,"cacheRead":0,"cacheWrite":0,"total":0.3}},"timestamp":5}`
}

func outcomeOf(t *testing.T, task *jv.Object) string {
	t.Helper()
	state, _ := task.Get("state")
	outcome, _ := state.(*jv.Object).Get("outcome")
	return jv.Stringify(outcome)
}

// TestGenerationAnswersAnInput follows prepare -> request -> answer for one input: the request carries the pinned stream
// options, a provider session ID is created once, the assistant entry and its usage land in the answer's commit, and the run
// ends with the input settled done.
func TestGenerationAnswersAnInput(t *testing.T) {
	r := newRig(t)
	conv, submission := r.start("hi")
	r.resume()
	if len(r.requests) != 1 {
		t.Fatalf("model requests = %d", len(r.requests))
	}
	req := r.requests[0].req
	if req.Model.Provider != "faux" || req.Context.(turn.ViewContext).View.NumMessages() != 1 {
		t.Fatalf("request model %+v, %d messages", req.Model, req.Context.(turn.ViewContext).View.NumMessages())
	}
	if got, want := jv.Stringify(req.Options), `{"sessionId":"session-1"}`; got != want {
		t.Fatalf("options = %s, want %s", got, want)
	}
	// The generation is on the first attempt; the run still owns the input.
	if got := r.doc(turn.LiveDef, conv); !strings.Contains(got, `"generation":{"attempt":1}`) || !strings.Contains(got, `"run":{"taskId":`) {
		t.Fatalf("live while requesting: %s", got)
	}
	r.finish(assistantMessage("stop", "hello"))

	entries := r.entries(conv)
	if len(entries) != 2 || str(entries[1], "kind") != "pi.assistant" {
		t.Fatalf("entries: %d", len(entries))
	}
	if got := r.doc(turn.LiveDef, conv); got != `{}` {
		t.Fatalf("live after the answer: %s", got)
	}
	if got := r.doc(turn.UsageDef, conv); !strings.Contains(got, `"faux/m":{"input":10,"output":5`) {
		t.Fatalf("usage = %s", got)
	}
	sub, _ := r.mem.Submission(submission)
	if got := jv.Stringify(sub); !strings.Contains(got, `"status":"done"`) {
		t.Fatalf("submission = %s", got)
	}
	if got := outcomeOf(t, r.tasks(turn.KindGeneration)[0]); !strings.HasPrefix(got, `{"status":"completed","result":{"entryId":`) {
		t.Fatalf("task outcome = %s", got)
	}
	if len(r.reports) != 0 {
		t.Fatalf("reports: %v", r.reports)
	}
}

func str(o *jv.Object, key string) string {
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

func toolUse(name string) string {
	return `{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"` + name + `","arguments":{}}],"provider":"faux","model":"m","stopReason":"toolUse","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5}`
}

// TestGenerationRetriesATransientError: an overloaded answer is appended, the attempt backs off durably, then the next
// attempt prepares again and answers; the attempt number reaches the live document and the request.
func TestGenerationRetriesATransientError(t *testing.T) {
	r := newRig(t)
	conv, submission := r.start("hi")
	r.resume()
	r.finish(`{"role":"assistant","content":[],"provider":"faux","model":"m","stopReason":"error","errorMessage":"Overloaded: try later","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5}`)
	if len(r.requests) != 0 {
		t.Fatal("the retry started before its backoff ended")
	}
	if got, want := r.doc(turn.LiveDef, conv), `"generation":{"attempt":1,"retry":{"at":3000,"error":"Overloaded: try later"}}`; !strings.Contains(got, want) {
		t.Fatalf("live during backoff = %s", got)
	}
	r.advance(1999)
	if len(r.requests) != 0 {
		t.Fatal("woke early")
	}
	r.advance(1)
	if len(r.requests) != 1 {
		t.Fatalf("requests after the backoff = %d", len(r.requests))
	}
	if got := r.doc(turn.LiveDef, conv); !strings.Contains(got, `"generation":{"attempt":2}`) {
		t.Fatalf("live on attempt 2 = %s", got)
	}
	r.finish(assistantMessage("stop", "ok"))
	if n := len(r.entries(conv)); n != 3 {
		t.Fatalf("entries = %d, want user, errored answer, answer", n)
	}
	sub, _ := r.mem.Submission(submission)
	if got := jv.Stringify(sub); !strings.Contains(got, `"status":"done"`) {
		t.Fatalf("submission = %s", got)
	}
}

// TestGenerationFailsAFatalError: a non-retryable error ends the run, settles the input unanswered with model_error and
// fails the task.
func TestGenerationFailsAFatalError(t *testing.T) {
	r := newRig(t)
	conv, submission := r.start("hi")
	r.resume()
	r.finish(`{"role":"assistant","content":[],"provider":"faux","model":"m","stopReason":"error","errorMessage":"invalid api key","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5}`)
	sub, _ := r.mem.Submission(submission)
	if got := jv.Stringify(sub); !strings.Contains(got, `"status":"unanswered"`) || !strings.Contains(got, `"reason":"model_error"`) || !strings.Contains(got, `"detail":"invalid api key"`) {
		t.Fatalf("submission = %s", got)
	}
	if got := outcomeOf(t, r.tasks(turn.KindGeneration)[0]); got != `{"status":"failed","error":{"message":"invalid api key","detail":{"reason":"model_error"}}}` {
		t.Fatalf("outcome = %s", got)
	}
	if got := r.doc(turn.LiveDef, conv); got != `{}` {
		t.Fatalf("live = %s", got)
	}
}

// TestGenerationThrottlesPartials: partials commit at most once per interval with the newest content, an empty partial
// shows nothing, and the terminal answer clears the partial.
func TestGenerationThrottlesPartials(t *testing.T) {
	r := newRig(t)
	conv, _ := r.start("hi")
	r.resume()
	req := r.requests[0]
	req.onPartial(mustRaw(t, `{"role":"assistant","content":[],"stopReason":"stop"}`))
	if len(r.timers) != 0 && func() bool {
		for _, tm := range r.timers {
			if !tm.dead && tm.at == r.now+100 {
				return true
			}
		}
		return false
	}() {
		t.Fatal("an empty partial armed the throttle")
	}
	req.onPartial(mustRaw(t, `{"role":"assistant","content":[{"type":"text","text":"he"}],"stopReason":"stop"}`))
	req.onPartial(mustRaw(t, `{"role":"assistant","content":[{"type":"text","text":"hel"}],"stopReason":"stop"}`))
	r.advance(100)
	if got := r.doc(turn.LiveDef, conv); !strings.Contains(got, `"message":{"role":"assistant","content":[{"type":"text","text":"hel"}]`) {
		t.Fatalf("live after the throttle = %s", got)
	}
	r.finish(assistantMessage("stop", "hello"))
	if got := r.doc(turn.LiveDef, conv); got != `{}` {
		t.Fatalf("live after the answer = %s", got)
	}
}

// TestGenerationStartsAToolRoundForAnUnofferedTool: a call to a tool the request did not offer gets its tool_unavailable
// result in the commit that appends the answer, and the run hands over to the next generation.
func TestGenerationAnswersAnUnofferedTool(t *testing.T) {
	r := newRig(t)
	conv, _ := r.start("hi")
	r.resume()
	r.finish(toolUse("read"))
	entries := r.entries(conv)
	if len(entries) != 3 || str(entries[1], "kind") != "pi.assistant" || str(entries[2], "kind") != "pi.tool-result" {
		t.Fatalf("entries: %v", len(entries))
	}
	result := jv.Stringify(entries[2])
	if !strings.Contains(result, `"toolCallId":"call_1"`) || !strings.Contains(result, `[error] Tool read is not available`) || !strings.Contains(result, `"isError":true`) {
		t.Fatalf("tool result = %s", result)
	}
	// The round ends at once; the run continues with a second generation, which asks the model again.
	if len(r.requests) != 1 {
		t.Fatalf("requests = %d, want the second generation's", len(r.requests))
	}
	if gens := r.tasks(turn.KindGeneration); len(gens) != 2 {
		t.Fatalf("generations = %d", len(gens))
	}
	r.finish(assistantMessage("stop", "done"))
	if got := r.doc(turn.LiveDef, conv); got != `{}` {
		t.Fatalf("live = %s", got)
	}
}

// TestPartialsCommitAsLeafDeltas: a partial that extends the committed one writes a string append, not the whole message
// again (json.ts assignJson: only a longer string leaf is an append).
func TestPartialsCommitAsLeafDeltas(t *testing.T) {
	r := newRig(t)
	r.start("hi")
	r.resume()
	req := r.requests[0]
	partial := func(text string) {
		req.onPartial(mustRaw(t, `{"role":"assistant","content":[{"type":"text","text":"`+text+`"}],"stopReason":"stop"}`))
		r.advance(100)
	}
	partial("hel")
	before := len(r.mem.Commits)
	partial("hello world")
	if len(r.mem.Commits) != before+1 {
		t.Fatalf("commits %d -> %d", before, len(r.mem.Commits))
	}
	var ops []string
	for _, w := range r.mem.Commits[len(r.mem.Commits)-1].Writes {
		if w.Kind == session.WriteDocChange {
			for _, op := range w.Ops {
				ops = append(ops, string(rune('0'+int(op.Verb)))+":"+op.Text)
			}
		}
	}
	if len(ops) != 1 || !strings.HasSuffix(ops[0], ":lo world") {
		t.Fatalf("ops of the second partial = %v, want one append of the new text", ops)
	}
}

func echoTool() turn.Tool {
	return turn.Tool{Declaration: mustParse(&testing.T{}, `{"name":"echo","description":"echoes","parameters":{"type":"object","properties":{"text":{"type":"string"}}}}`)}
}

func toolUseWith(name, args string) string {
	return `{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"` + name + `","arguments":` + args + `}],"provider":"faux","model":"m","stopReason":"toolUse","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5}`
}

// TestToolRound runs an offered tool: the answer starts a tool task whose slot shows the running tool and its progress, the
// result entry carries the output, the slot ends done, and the run hands over to a second generation that answers.
func TestToolRound(t *testing.T) {
	r := newRig(t)
	r.agent.Tools = []turn.Tool{echoTool()}
	conv, _ := r.start("hi")
	r.resume()
	r.finish(toolUseWith("echo", `{"text":"x"}`))
	if len(r.toolRuns) != 1 {
		t.Fatalf("tool runs = %d", len(r.toolRuns))
	}
	run := r.toolRuns[0]
	if run.run.Name != "echo" || run.run.CallID != "call_1" || jv.Stringify(run.run.Arguments) != `{"text":"x"}` {
		t.Fatalf("run = %+v", run.run)
	}
	live := r.doc(turn.LiveDef, conv)
	if !strings.Contains(live, `"status":"running"`) || strings.Contains(live, `"generation"`) {
		t.Fatalf("live while the tool runs = %s", live)
	}
	if err := run.io.Output([]byte("hello "), true, nil); err != nil {
		t.Fatal(err)
	}
	r.run()
	if live := r.doc(turn.LiveDef, conv); !strings.Contains(live, `"output":"hello "`) {
		t.Fatalf("live after output = %s", live)
	}
	run.then(mustParse(t, `{"output":[{"type":"text","text":"hello world"}]}`), nil)
	r.run()
	entries := r.entries(conv)
	last := jv.Stringify(entries[len(entries)-1])
	if !strings.Contains(last, `"kind":"pi.tool-result"`) || !strings.Contains(last, `"text":"hello world"`) || !strings.Contains(last, `"durationMs":0`) {
		t.Fatalf("result entry = %s", last)
	}
	if len(r.requests) != 1 {
		t.Fatalf("requests after the round = %d", len(r.requests))
	}
	r.finish(assistantMessage("stop", "done"))
	if got := r.doc(turn.LiveDef, conv); got != `{}` {
		t.Fatalf("live = %s", got)
	}
	if got := r.doc(turn.UsageDef, conv); !strings.Contains(got, `"faux/m"`) {
		t.Fatalf("usage = %s", got)
	}
}

func (r *rig) toolRound(args string) {
	r.agent.Tools = []turn.Tool{echoTool()}
	r.start("hi")
	r.resume()
	r.finish(toolUseWith("echo", args))
}

func lastEntry(r *rig) string {
	e := r.entries(1)
	return jv.Stringify(e[len(e)-1])
}

// TestToolThrowEndsFailedWithTheErrorInTheResult: execute throwing becomes an error result with a tool_error diagnostic and
// ends the task failed (cancelling what the call owned); the generation still continues its round.
func TestToolThrowEndsFailedWithTheErrorInTheResult(t *testing.T) {
	r := newRig(t)
	r.toolRound(`{}`)
	r.toolRuns[0].then(nil, errors.New("disk on fire"))
	r.run()
	last := lastEntry(r)
	if !strings.Contains(last, `[error] disk on fire`) || !strings.Contains(last, `"isError":true`) {
		t.Fatalf("result entry = %s", last)
	}
	tools := r.tasks(turn.KindTool)
	if len(tools) != 1 || outcomeOf(t, tools[0]) != `{"status":"failed","error":{"message":"Tool echo threw"},"result":{"kind":"model","entryId":`+jv.Stringify(entryID(r, last))+`}}` {
		t.Fatalf("tool outcome = %s", outcomeOf(t, tools[0]))
	}
}

// TestToolResultTheHostCannotRecordFaultsTheCall: a result that is not strict JSON (the host's ResultFault) leaves no result
// entry and faults the tool task, where a throwing execute ends it failed with an error entry.
func TestToolResultTheHostCannotRecordFaultsTheCall(t *testing.T) {
	r := newRig(t)
	r.toolRound(`{}`)
	r.toolRuns[0].then(nil, &turn.ResultFault{Text: "Unsupported value"})
	r.run()
	tools := r.tasks(turn.KindTool)
	if len(tools) != 1 || !strings.Contains(outcomeOf(t, tools[0]), `"status":"faulted"`) {
		t.Fatalf("tool outcome = %s", outcomeOf(t, tools[0]))
	}
	if last := lastEntry(r); strings.Contains(last, `"role":"toolResult"`) {
		t.Fatalf("a result entry was recorded: %s", last)
	}
}

func entryID(r *rig, entry string) float64 {
	e := mustParse(r.t, entry)
	v, _ := e.Get("id")
	return v.(float64)
}

// TestToolBlockedAndInvalid: a beforeTool block and failing validation settle the call without running the tool.
func TestToolBlockedAndInvalid(t *testing.T) {
	r := newRig(t)
	r.hooks["beforeTool"] = func(int, *jv.Object) (any, error) { return mustParse(t, `{"block":"no echoing"}`), nil }
	r.toolRound(`{}`)
	if len(r.toolRuns) != 0 {
		t.Fatal("a blocked call ran")
	}
	if last := lastEntry(r); !strings.Contains(last, `Tool call blocked: no echoing`) {
		t.Fatalf("result entry = %s", last)
	}

	r = newRig(t)
	r.host.ValidateArguments = func(_ string, _ *jv.Object, _ any, then func(*jv.Object, string, error)) {
		then(nil, "text must be a string", nil)
	}
	r.toolRound(`{"text":1}`)
	if len(r.toolRuns) != 0 {
		t.Fatal("an invalid call ran")
	}
	if last := lastEntry(r); !strings.Contains(last, `[error] text must be a string`) {
		t.Fatalf("result entry = %s", last)
	}
}

// TestToolOutputIsBoundedAndTruncationReported: retained output beyond the limits keeps the head and reports a truncation
// diagnostic after the tool's own.
func TestToolOutputIsBoundedAndTruncationReported(t *testing.T) {
	r := newRig(t)
	n := 3
	tool := echoTool()
	tool.Limits = turn.ToolLimits{MaxLines: &n}
	r.agent.Tools = []turn.Tool{tool}
	r.start("hi")
	r.resume()
	r.finish(toolUseWith("echo", `{}`))
	_ = r.toolRuns[0].io.Output([]byte("a\nb\nc\nd\ne\n"), true, nil)
	r.toolRuns[0].then(mustParse(t, `{}`), nil)
	r.run()
	last := lastEntry(r)
	if !strings.Contains(last, `"text":"a\nb\nc\n"`) || !strings.Contains(last, `[warn] Output truncated to its beginning: 2 lines, 4 bytes dropped`) {
		t.Fatalf("result entry = %s", last)
	}
}

// abortable settles a host operation once: with its own result, or with ErrAborted when its cancel fires first (the host
// rejects the promise of an aborted operation).
func abortable(cancel *session.Cancel, then func(*jv.Object, error)) func(*jv.Object, error) {
	settled := false
	var remove func()
	settle := func(v *jv.Object, err error) {
		if settled {
			return
		}
		settled = true
		if remove != nil {
			remove()
		}
		then(v, err)
	}
	remove = cancel.OnAbort(func() { settle(nil, session.ErrAborted) })
	return settle
}

// TestSecondGenerationAfterAToolRoundPlansNoSystemEntry: the next generation replays the sections and tools the first
// system entry showed, so planning appends nothing (prompt.ts planSystemEntries over replaySections).
func TestSecondGenerationAfterAToolRoundPlansNoSystemEntry(t *testing.T) {
	r := newRig(t)
	r.agent.Tools = []turn.Tool{echoTool()}
	r.host.Render = func(_, _ session.ID, _ *turn.Agent, _ *turn.Sections, _ *session.Cancel, then func(*turn.Sections, error)) {
		s := &turn.Sections{}
		s.Set("preamble", "You are terse.")
		then(s, nil)
	}
	conv, _ := r.start("hi")
	r.resume()
	r.finish(toolUseWith("echo", `{"text":"x"}`))
	r.toolRuns[0].then(mustParse(t, `{"output":[{"type":"text","text":"ok"}]}`), nil)
	r.run()
	r.finish(assistantMessage("stop", "done"))
	n := 0
	for _, e := range r.entries(conv) {
		if str(e, "kind") == "pi.system" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("pi.system entries = %d, want 1", n)
	}
}

// runtime.agent() is `awaitWithContext(agent())`, not a Session read, and resolves three hops after the phase starts
// (scheduler.ts:1138-1139, harness.ts:215-221; dcore-trace E20/E23 phase chains): the turn asks for a three-hop chain beside
// the host's resolution, and the provider's snapshot over the loaded pi.provider document adds its own (scheduler.ts:1175).
func TestGenerationResolvesItsAgentBehindThreeHops(t *testing.T) {
	r := newRig(t)
	r.start("hi")
	r.resume()
	threes := 0
	for _, n := range r.hops {
		if n == 3 {
			threes++
		}
	}
	if threes == 0 {
		t.Fatalf("hops chains %v: no three-hop agent resolution", r.hops)
	}
}
