package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/durable/core/contracttest"
	"github.com/MichaelKinsy/PiG/durable/core/contracttest/storehost"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/rec"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

const (
	usageKey    = "faux/scripted-1"
	interrupted = "Tool lookup was interrupted and may have partially run"
	attemptOps  = `[["s",["generation"],{"attempt":1}]]`
	toolRunning = `[["s",["tools",0,"status"],"running"]]`
)

// obj builds a JSON object from alternating keys and values, in order.
func obj(kv ...any) *jv.Object {
	o := jv.NewObjectCap(len(kv) / 2)
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func num(n int64) float64 { return float64(n) }

// turnState is the turn in flight.
type turnState struct {
	active                     bool
	reqID                      string
	user, sub, gen, asst, tool int64
	call                       int
}

// engine runs the durable-bench turn (a user input, generations and one lookup per round) over the production store: every
// write is a store.Tx, every record a rec constructor or a history entry record, and the transcript lives in history.Store.
type engine struct {
	h  *storehost.Host
	st *store.Store
	hs *history.Store

	conv, liveDoc, usageDoc int64
	liveVer, usageVer       int
	api                     string
	now                     float64
	payloadKB               int

	live, usage *jv.Object
	idx         index
	hasPrev     bool
	prevChars   int
	lastEntry   int64

	tasks map[int64]*rec.Task
	sub   *jv.Object
	t     turnState
	calls int
	// awaitingTool: the next request is the tool's, not the model's.
	awaitingTool bool
}

func ceil4(n int) int { return (n + 3) / 4 }

// openEngine runs cold open and loads the root conversation's context.
func openEngine(h *storehost.Host, now float64, payloadKB int) (*engine, *store.Live, error) {
	st := store.New(store.Options{})
	var live *store.Live
	if err := h.Run(st, func() (err error) { live, err = st.Open(h.B); return }); err != nil {
		return nil, nil, fmt.Errorf("open: %w", err)
	}
	e := &engine{h: h, st: st, hs: history.NewStore(), now: now, payloadKB: payloadKB, tasks: map[int64]*rec.Task{}}
	live.LoadInto(e.hs)
	if len(live.Conversations) == 0 {
		return nil, nil, errors.New("the store has no conversation")
	}
	e.conv = live.Conversations[0].ID()
	for _, c := range live.Conversations {
		if c.ID() < e.conv {
			e.conv = c.ID()
		}
	}
	for _, d := range live.Documents {
		switch d.Record.Kind() {
		case "pi.live":
			e.liveDoc, e.liveVer, e.live = d.Record.ID(), d.Version, jv.Clone(d.Value).(*jv.Object)
		case "pi.usage":
			e.usageDoc, e.usageVer, e.usage = d.Record.ID(), d.Version, jv.Clone(d.Value).(*jv.Object)
		}
	}
	if e.live == nil || e.usage == nil {
		return nil, nil, errors.New("the store lacks the pi.live or pi.usage document")
	}
	var view *history.View
	if err := h.Run(st, func() (err error) {
		var need *history.Need
		view, need, err = e.hs.Context(e.conv, 0)
		if err == nil && need != nil {
			err = st.Load(h.B, e.hs, need)
		}
		return err
	}); err != nil {
		return nil, nil, fmt.Errorf("load: %w", err)
	}
	// The scripted model reads the stored messages of every entry, as the reference's provider adapter does when the context
	// is already whole; the context's synthetic results for a dangling tool call are not part of the transcript.
	for i := range view.NumEntries() {
		e.lastEntry = max(e.lastEntry, view.EntryID(i))
		v, err := jv.Parse(view.EntryRecord(i))
		if err != nil {
			return nil, nil, err
		}
		model, _ := objOf(v).Get("model")
		msgs, _ := model.([]any)
		for _, raw := range msgs {
			m, err := info(jv.Append(nil, raw))
			if err != nil {
				return nil, nil, err
			}
			e.idx.add(m)
		}
	}
	e.api = lastAPI(view)
	return e, live, nil
}

// lastAPI is the faux provider's api name: the stored assistant messages carry it (the provider is created once per process
// and named from the virtual clock and the seeded random source, so every run of the scenario has the same name).
func lastAPI(view *history.View) string {
	for i := view.NumMessages() - 1; i >= 0; i-- {
		v, err := jv.Parse(view.Message(i))
		if err != nil {
			continue
		}
		if o, ok := v.(*jv.Object); ok && str(o, "role") == "assistant" {
			return str(o, "api")
		}
	}
	return ""
}

// commit builds one Pi commit with build, applies it, and returns the sequence it consumed.
func (e *engine) commit(build func(tx *store.Tx) error) (int64, error) {
	var seq int64
	err := e.h.Run(e.st, func() error {
		tx := e.st.Begin()
		if err := build(tx); err != nil {
			return err
		}
		s, err := e.st.Commit(e.h.B, tx)
		seq = s
		return err
	})
	return seq, err
}

func (e *engine) task(id int64, kind string, input *jv.Object, owner int64) *rec.Task {
	if t, ok := e.tasks[id]; ok {
		return t
	}
	version := 1
	if kind == "pi.tool" {
		version = 2 // tool.ts ToolTask version 2: tagged input and result
	}
	return rec.NewTask(id, e.conv, kind, version, input, owner, false, nil)
}

func (e *engine) gen(id int64) *rec.Task { return e.task(id, "pi.generation", obj(), 0) }

func (e *engine) tool() *rec.Task {
	t := e.t
	return e.task(t.tool, "pi.tool", obj("kind", "model", "assistant", num(t.asst), "callId", "call-"+strconv.Itoa(t.call)), t.gen)
}

// putTask replaces a task's state, stamps its times as Pi's Session does, and stages the write.
func (e *engine) putTask(tx *store.Tx, t *rec.Task, state *jv.Object) {
	n := t.WithState(state).StampTimes(nil, e.now)
	tx.Task(n, n.Append(nil))
	if n.Terminal() {
		delete(e.tasks, n.ID())
	} else {
		e.tasks[n.ID()] = n
	}
}

func (e *engine) putSubmission(tx *store.Tx) {
	sub := &rec.Submission{V: e.sub}
	tx.Submission(sub, sub.Append(nil))
}

func (e *engine) putLive(tx *store.Tx)  { tx.DocumentBase(e.liveDoc, e.liveVer, jv.Clone(e.live)) }
func (e *engine) putUsage(tx *store.Tx) { tx.DocumentBase(e.usageDoc, e.usageVer, jv.Clone(e.usage)) }

// putEntry stages an entry written from a draft and returns what follows the commit: the transcript append.
func (e *engine) putEntry(tx *store.Tx, id int64, draft string, task int64, hasTask bool) (func(seq int64) error, error) {
	record, err := history.AppendEntryRecord(nil, []byte(draft), id, e.conv, task, hasTask)
	if err != nil {
		return nil, err
	}
	tx.Entry(id, e.conv, 0, record)
	e.lastEntry = id
	return func(seq int64) error { return e.hs.Append(e.conv, id, seq, record) }, nil
}

func state(status string, checkpoint any, more ...any) *jv.Object {
	s := obj("status", status, "checkpoint", checkpoint)
	for i := 0; i+1 < len(more); i += 2 {
		s.Set(more[i].(string), more[i+1])
	}
	return s
}

func (e *engine) requestCheckpoint() *jv.Object {
	return obj("phase", "request", "attempt", num(1), "model", obj("provider", "faux", "modelId", "scripted-1"),
		"thinkingLevel", "off", "streamOptions", obj(), "cutoff", num(e.lastEntry))
}

func prepareCheckpoint() *jv.Object { return obj("phase", "prepare", "attempt", num(1)) }

func terminal(result any) *jv.Object {
	return obj("status", "terminal", "outcome", result)
}

func completed(entry int64) *jv.Object {
	return obj("status", "completed", "result", obj("entryId", num(entry)))
}

// runGeneration is the three commits that take a pending generation to a model request.
func (e *engine) runGeneration() error {
	g := e.gen(e.t.gen)
	if _, err := e.commit(func(tx *store.Tx) error { e.putTask(tx, g, state("running", prepareCheckpoint())); return nil }); err != nil {
		return err
	}
	g = e.gen(e.t.gen)
	if _, err := e.commit(func(tx *store.Tx) error { e.putTask(tx, g, state("running", e.requestCheckpoint())); return nil }); err != nil {
		return err
	}
	_, err := e.commit(func(tx *store.Tx) error {
		e.live.Set("generation", obj("attempt", num(1)))
		tx.DocumentDelta(e.liveDoc, e.liveVer, []byte(attemptOps))
		return nil
	})
	return err
}

func quote(s string) string { return string(jv.AppendString(nil, s)) }

// submit is the input event: four commits, then the model request.
func (e *engine) submit(reqID, text string) error {
	e.t = turnState{active: true, reqID: reqID, user: e.st.MintID(), sub: e.st.MintID(), gen: e.st.MintID()}
	t := e.t
	userDraft := `{"model":[{"role":"user","content":` + quote(text) + `,"timestamp":` + num2(e.now) + `}],"kind":"pi.user"}`
	m, err := info([]byte(`{"role":"user","content":` + quote(text) + `}`))
	if err != nil {
		return err
	}
	e.idx.add(m)
	e.sub = obj("conversationId", num(e.conv), "requestId", reqID, "type", "input", "status", "placed", "entry", num(t.user), "id", num(t.sub))
	var after func(int64) error
	seq, err := e.commit(func(tx *store.Tx) (err error) {
		if after, err = e.putEntry(tx, t.user, userDraft, 0, false); err != nil {
			return err
		}
		e.putSubmission(tx)
		e.putTask(tx, e.gen(t.gen), state("pending", prepareCheckpoint()))
		e.live.Set("run", obj("taskId", num(t.gen), "inputs", []any{num(t.sub)}))
		e.putLive(tx)
		return nil
	})
	if err != nil {
		return err
	}
	if err := after(seq); err != nil {
		return err
	}
	return e.runGeneration()
}

func num2(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// fauxUsage is pi-ai's faux provider usage estimate for a response of outputChars characters; its prompt cache lives in the
// process, so it is absent after a cold start.
func (e *engine) fauxUsage(outputChars int) (in, out, cacheRead, cacheWrite, total int) {
	l := e.idx.promptChars()
	prompt := ceil4(l)
	out = ceil4(outputChars)
	if e.hasPrev {
		cacheRead = ceil4(e.prevChars)
		cacheWrite = ceil4(l - e.prevChars)
		in = max(0, prompt-cacheRead)
	} else {
		in, cacheWrite = prompt, prompt
	}
	e.prevChars, e.hasPrev = l, true
	total = in + out + cacheRead + cacheWrite
	return
}

// recordUsage is Pi's recordUsage: a new bucket is a copy of the usage, an existing one has it added.
func (e *engine) recordUsage(in, out, cacheRead, cacheWrite, total int) {
	models, _ := e.usage.Get("models")
	mo := models.(*jv.Object)
	cost := func() *jv.Object {
		return obj("input", num(0), "output", num(0), "cacheRead", num(0), "cacheWrite", num(0), "total", num(0))
	}
	add := obj("input", num(int64(in)), "output", num(int64(out)), "cacheRead", num(int64(cacheRead)), "cacheWrite", num(int64(cacheWrite)), "totalTokens", num(int64(total)), "cost", cost())
	cur, ok := mo.Get(usageKey)
	if !ok {
		mo.Set(usageKey, add)
		return
	}
	co := cur.(*jv.Object)
	for _, k := range []string{"input", "output", "cacheRead", "cacheWrite", "totalTokens"} {
		a, _ := co.Get(k)
		b, _ := add.Get(k)
		co.Set(k, a.(float64)+b.(float64))
	}
}

func usageJSON(in, out, cacheRead, cacheWrite, total int) string {
	return fmt.Sprintf(`{"input":%d,"output":%d,"cacheRead":%d,"cacheWrite":%d,"totalTokens":%d,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`, in, out, cacheRead, cacheWrite, total)
}

// model answers the model request with the scripted model and the faux provider's usage, then runs the commits that follow.
func (e *engine) model(trace *contracttest.Writer) error {
	e.calls++
	if err := trace.Model(e.calls, e.idx.fingerprint()); err != nil {
		return err
	}
	call, done := e.idx.next()
	t := &e.t
	var content, stop string
	var outChars int
	if call >= 0 {
		args := `{"n":` + strconv.Itoa(call) + `}`
		content = `[{"type":"toolCall","id":"call-` + strconv.Itoa(call) + `","name":"lookup","arguments":` + args + `}]`
		stop, outChars = "toolUse", len("lookup:")+len(args)
	} else {
		text := "done after " + strconv.Itoa(done) + " lookups"
		content = `[{"type":"text","text":` + quote(text) + `}]`
		stop, outChars = "stop", len(text)
	}
	in, out, cr, cw, total := e.fauxUsage(outChars)
	e.recordUsage(in, out, cr, cw, total)
	t.asst = e.st.MintID()
	draft := `{"model":[{"role":"assistant","content":` + content + `,"api":` + quote(e.api) + `,"provider":"faux","model":"scripted-1","usage":` +
		usageJSON(in, out, cr, cw, total) + `,"stopReason":"` + stop + `","timestamp":` + num2(e.now) + `,"durationMs":0}],"kind":"pi.assistant"}`
	m, err := info([]byte(`{"role":"assistant","content":` + content + `}`))
	if err != nil {
		return err
	}
	e.idx.add(m)

	var after func(int64) error
	if call < 0 {
		seq, err := e.commit(func(tx *store.Tx) (err error) {
			if after, err = e.putEntry(tx, t.asst, draft, t.gen, true); err != nil {
				return err
			}
			e.sub.Set("status", "done")
			e.sub.Set("answer", num(t.asst))
			e.putSubmission(tx)
			e.putTask(tx, e.gen(t.gen), terminal(completed(t.asst)))
			e.live.Delete("run")
			e.live.Delete("generation")
			e.putLive(tx)
			e.putUsage(tx)
			return nil
		})
		if err != nil {
			return err
		}
		t.active = false
		return after(seq)
	}

	t.call = call
	t.tool = e.st.MintID()
	seq, err := e.commit(func(tx *store.Tx) (err error) {
		if after, err = e.putEntry(tx, t.asst, draft, t.gen, true); err != nil {
			return err
		}
		e.putTask(tx, e.gen(t.gen), obj("status", "waiting",
			"checkpoint", obj("phase", "tools", "assistant", num(t.asst), "tools", []any{num(t.tool)}, "pending", []any{}),
			"on", []any{num(t.tool)}, "policy", "allSettled"))
		e.putTask(tx, e.tool(), state("pending", obj("phase", "call")))
		e.live.Delete("generation")
		e.live.Set("tools", []any{obj("callId", "call-"+strconv.Itoa(call), "name", "lookup", "taskId", num(t.tool), "status", "pending")})
		e.putLive(tx)
		e.putUsage(tx)
		return nil
	})
	if err != nil {
		return err
	}
	if err := after(seq); err != nil {
		return err
	}
	if _, err := e.commit(func(tx *store.Tx) error { e.putTask(tx, e.tool(), state("running", obj("phase", "call"))); return nil }); err != nil {
		return err
	}
	return e.executeTool()
}

// executeTool takes the tool task to its execute phase; the live document records it running (a delta).
func (e *engine) executeTool() error {
	_, err := e.commit(func(tx *store.Tx) error {
		e.putTask(tx, e.tool(), state("running", obj("phase", "execute", "arguments", obj("n", num(int64(e.t.call))), "replay", "unsafe")))
		tools, _ := e.live.Get("tools")
		tools.([]any)[0].(*jv.Object).Set("status", "running")
		tx.DocumentDelta(e.liveDoc, e.liveVer, []byte(toolRunning))
		return nil
	})
	return err
}

// payload is the lookup tool's result text.
func (e *engine) payload(n int) string {
	head := "record " + strconv.Itoa(n) + ": "
	size := 256
	switch {
	case e.payloadKB > 0:
		size = e.payloadKB * 1024
	case n%97 == 0:
		size = 8192
	}
	return head + strings.Repeat("x", size-len(head))
}

// toolResult settles the tool with the lookup's result and runs the commits up to the next model request.
func (e *engine) toolResult() error {
	if err := e.settleTool(e.payload(e.t.call), "completed", "", false); err != nil {
		return err
	}
	return e.afterTool(false)
}

// settleTool writes the tool's result entry, its terminal task and the live document. A failed settlement is the
// interrupted-tool result Pi writes when it recovers a tool that may have run.
func (e *engine) settleTool(text, outcome, errMessage string, isError bool) error {
	t := e.t
	res := e.st.MintID()
	diag := ""
	if isError {
		diag = `{"severity":"error","code":"interrupted","message":` + quote(errMessage) + `}`
	}
	// A result the recovery writes for a tool that may have run has no duration: nothing measured it.
	duration := `"durationMs":0,`
	if isError {
		duration = ""
	}
	draft := `{"model":[{"role":"toolResult","toolCallId":"call-` + strconv.Itoa(t.call) + `","toolName":"lookup","content":[{"type":"text","text":` + quote(text) +
		`}],"isError":` + strconv.FormatBool(isError) + `,` + duration + `"timestamp":` + num2(e.now) + `}],"data":{"diagnostics":[` + diag + `]},"kind":"pi.tool-result"}`
	m, err := info([]byte(`{"role":"toolResult","toolName":"lookup","content":[{"type":"text","text":` + quote(text) + `}]}`))
	if err != nil {
		return err
	}
	e.idx.add(m)
	var ended *jv.Object
	if outcome == "failed" {
		ended = obj("status", "failed", "error", obj("message", errMessage), "result", obj("kind", "model", "entryId", num(res)))
	} else {
		ended = obj("status", "completed", "result", obj("kind", "model", "entryId", num(res)))
	}
	var after func(int64) error
	seq, err := e.commit(func(tx *store.Tx) (err error) {
		if after, err = e.putEntry(tx, res, draft, t.tool, true); err != nil {
			return err
		}
		e.putTask(tx, e.tool(), terminal(ended))
		tools, _ := e.live.Get("tools")
		tl := tools.([]any)[0].(*jv.Object)
		tl.Set("status", "done")
		tl.Set("entry", num(res))
		e.putLive(tx)
		return nil
	})
	if err != nil {
		return err
	}
	return after(seq)
}

// afterTool takes a generation whose tool has settled to its next model request: it wakes, finishes, and the next one runs
// prepare, request and the attempt delta. A generation persisted as running restarts first.
func (e *engine) afterTool(resumed bool) error {
	t := &e.t
	g := e.gen(t.gen)
	running := func(status string) *jv.Object {
		return state(status, obj("phase", "tools", "assistant", num(t.asst), "tools", []any{num(t.tool)}, "pending", []any{}))
	}
	if resumed {
		if _, err := e.commit(func(tx *store.Tx) error { e.putTask(tx, g, running("pending")); return nil }); err != nil {
			return err
		}
	}
	g = e.gen(t.gen)
	if _, err := e.commit(func(tx *store.Tx) error { e.putTask(tx, g, running("running")); return nil }); err != nil {
		return err
	}
	g = e.gen(t.gen)
	t.gen = e.st.MintID()
	if _, err := e.commit(func(tx *store.Tx) error {
		e.putTask(tx, e.gen(t.gen), state("pending", prepareCheckpoint()))
		e.putTask(tx, g, terminal(completed(t.asst)))
		e.live.Delete("tools")
		run, _ := e.live.Get("run")
		run.(*jv.Object).Set("taskId", num(t.gen))
		e.putLive(tx)
		return nil
	}); err != nil {
		return err
	}
	return e.runGeneration()
}

// turn runs one input to completion: submit, then answer each request until the generation is done.
func (e *engine) turn(trace *contracttest.Writer, id string, tools int) error {
	if err := e.submit(id, "turn "+id+" tools="+strconv.Itoa(tools)); err != nil {
		return err
	}
	return e.drive(trace)
}

// drive answers requests until the turn is done. The next request is the model's unless a tool call is outstanding.
func (e *engine) drive(trace *contracttest.Writer) error {
	for e.t.active {
		if e.awaitingTool {
			e.awaitingTool = false
			if err := e.toolResult(); err != nil {
				return err
			}
			continue
		}
		if err := e.model(trace); err != nil {
			return err
		}
		if e.t.active {
			e.awaitingTool = true
		}
	}
	return nil
}

func objOf(v any) *jv.Object { o, _ := v.(*jv.Object); return o }

func intOf(o *jv.Object, key string) int64 {
	v, _ := o.Get(key)
	f, _ := v.(float64)
	return int64(f)
}

// setStatus rewrites the task with only its status changed, as scheduler recovery does.
func (e *engine) setStatus(id int64, status string) error {
	t := e.tasks[id]
	s := jv.Clone(t.State()).(*jv.Object)
	s.Set("status", status)
	_, err := e.commit(func(tx *store.Tx) error { e.putTask(tx, t, s); return nil })
	return err
}

// recover resumes a turn from the persisted task states the way pi-durable's recovery does: a running task is re-queued as
// pending, then each task continues from its checkpoint phase. A tool that reached its execute phase may have run, so it
// settles as interrupted instead of running again.
func (e *engine) recover(live *store.Live) error {
	var gen, tool *rec.Task
	for _, t := range live.Tasks {
		switch t.Kind() {
		case "pi.generation":
			gen = t
		case "pi.tool":
			tool = t
		default:
			return fmt.Errorf("recovery: task kind %s is outside this host's workload", t.Kind())
		}
		e.tasks[t.ID()] = t
	}
	if gen == nil {
		return nil
	}
	if len(live.Submissions) != 1 {
		return errors.New("recovery: expected exactly one placed submission")
	}
	sub := live.Submissions[0]
	e.sub = jv.Clone(sub.V).(*jv.Object)
	reqID, _ := sub.RequestID()
	user, _ := sub.Entry()
	e.t = turnState{active: true, reqID: reqID, user: user, sub: sub.ID(), gen: gen.ID()}
	t := &e.t
	st := gen.State()
	cp := objOf(mustGet(st, "checkpoint"))
	status := str(st, "status")
	phase := ""
	if cp != nil {
		phase = str(cp, "phase")
		t.asst = intOf(cp, "assistant")
	}
	tools := func(o *jv.Object) (int64, error) {
		arr, _ := mustGet(o, "tools").([]any)
		if len(arr) != 1 {
			return 0, errors.New("recovery: generation waits on an unexpected tool set")
		}
		return int64(arr[0].(float64)), nil
	}
	if tool != nil {
		t.tool = tool.ID()
		in := objOf(tool.Input())
		t.asst = intOf(in, "assistant")
		call, err := strconv.Atoi(strings.TrimPrefix(str(in, "callId"), "call-"))
		if err != nil {
			return fmt.Errorf("recovery: tool call id %q", str(in, "callId"))
		}
		t.call = call
		ts := tool.State()
		if str(ts, "status") == "running" {
			if err := e.setStatus(tool.ID(), "pending"); err != nil {
				return err
			}
		}
		if str(objOf(mustGet(ts, "checkpoint")), "phase") == "call" {
			if err := e.setStatus(tool.ID(), "running"); err != nil {
				return err
			}
			if err := e.executeTool(); err != nil {
				return err
			}
			e.awaitingTool = true
			return nil
		}
		if err := e.setStatus(tool.ID(), "running"); err != nil {
			return err
		}
		if err := e.settleTool("<harness>\n[error] "+interrupted+"\n</harness>", "failed", interrupted, true); err != nil {
			return err
		}
		return e.afterTool(false)
	}
	switch {
	case status == "waiting":
		id, err := tools(cp)
		if err != nil {
			return err
		}
		t.tool = id
		return e.afterTool(false)
	case phase == "tools":
		id, err := tools(cp)
		if err != nil {
			return err
		}
		t.tool = id
		return e.afterTool(status == "running")
	case phase == "prepare":
		if status == "running" {
			if err := e.setStatus(gen.ID(), "pending"); err != nil {
				return err
			}
		}
		return e.runGeneration()
	case phase == "request":
		if status == "running" {
			if err := e.setStatus(gen.ID(), "pending"); err != nil {
				return err
			}
		}
		if err := e.setStatus(gen.ID(), "running"); err != nil {
			return err
		}
		if _, ok := e.live.Get("generation"); !ok {
			_, err := e.commit(func(tx *store.Tx) error {
				e.live.Set("generation", obj("attempt", num(1)))
				tx.DocumentDelta(e.liveDoc, e.liveVer, []byte(attemptOps))
				return nil
			})
			return err
		}
		return nil
	}
	return fmt.Errorf("recovery: generation phase %q is outside this host's workload", phase)
}

func mustGet(o *jv.Object, key string) any { v, _ := o.Get(key); return v }
