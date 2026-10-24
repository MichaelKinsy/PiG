package codemode

// pi: packages/durable/src/codemode/codemode.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A restart scenario is a script and the steps a host takes around it: start it, wait for what it reaches, close the
// Harness (a restart), change the stored documents, reopen, resume, and look at what is left. The same JSON runs under
// Node against Pi's real Harness, CodemodeSandbox and code mode tool (testdata/codemode_restart_oracle.mjs) and against
// PiG's, and the observations must be equal: the call's result and details, the stored run, journal and store documents,
// the entries, how often each tool ran, and the report texts.
type restartScenario struct {
	Name    string        `json:"name"`
	Code    string        `json:"code"`
	Globals []string      `json:"globals,omitempty"`
	Steps   []restartStep `json:"steps"`
}

type restartStep struct {
	Op         string            `json:"op"`
	What       string            `json:"what,omitempty"`
	Label      string            `json:"label,omitempty"`
	Entries    int               `json:"entries,omitempty"`
	Index      int               `json:"index"`
	Value      json.RawMessage   `json:"value,omitempty"`
	ID         int               `json:"id,omitempty"`
	Count      int               `json:"count,omitempty"`
	Rearm      bool              `json:"rearm,omitempty"`
	Unblock    bool              `json:"unblock,omitempty"`
	Submission bool              `json:"submission,omitempty"`
	MS         *int64            `json:"ms,omitempty"`
	Advance    *int64            `json:"advance,omitempty"`
	Doc        string            `json:"doc,omitempty"`
	Path       []json.RawMessage `json:"path,omitempty"`
	Delete     bool              `json:"delete,omitempty"`
	Length     *int              `json:"length,omitempty"`
	Message    string            `json:"message,omitempty"`
	Code       string            `json:"code,omitempty"`
	Push       bool              `json:"push,omitempty"`
	Tool       string            `json:"tool,omitempty"`
	// Light observes only what a failed Session still lets the host see.
	Light bool `json:"light,omitempty"`
}

const restartBaseClock = 1_700_000_000_000

// queuedStorage is memory storage whose commits a scenario controls (testdata/codemode_restart_oracle.mjs QueuedStorage):
// after queue, each commit waits in order until release lets the oldest go, failing when given a message; entered counts
// the commits that reached storage since.
type queuedStorage struct {
	*storage.MemoryStorage
	mu       sync.Mutex
	queueing bool
	marked   bool
	entered  int
	// pushes is, for each commit since the mark that appended journal entries, how many.
	pushes  []int
	waiting []chan error
	failure error
}

func (s *queuedStorage) queue() {
	s.mu.Lock()
	s.queueing, s.marked, s.entered, s.pushes = true, true, 0, nil
	s.mu.Unlock()
}

func (s *queuedStorage) release(failure error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.waiting) == 0 {
		panic("no commit is waiting")
	}
	next := s.waiting[0]
	s.waiting = s.waiting[1:]
	next <- failure
}

func (s *queuedStorage) unqueue() {
	s.mu.Lock()
	s.queueing = false
	waiting := s.waiting
	s.waiting = nil
	s.mu.Unlock()
	for _, next := range waiting {
		next <- nil
	}
}

func (s *queuedStorage) enteredCommits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entered
}

// journalEntriesAppended is how many entries a commit appends to a journal document.
func journalEntriesAppended(writes []durable.StorageWrite) int {
	appended := 0
	for _, write := range writes {
		change, ok := write.(durable.DocumentChangeWrite)
		if !ok {
			continue
		}
		for _, op := range change.Content.Ops {
			encoded, err := json.Marshal(op)
			if err != nil {
				panic(err)
			}
			var parts []json.RawMessage
			if err := json.Unmarshal(encoded, &parts); err != nil || len(parts) < 5 {
				continue
			}
			if string(parts[0]) == `"p"` && string(parts[1]) == `["entries"]` {
				var items []json.RawMessage
				if err := json.Unmarshal(parts[4], &items); err != nil {
					panic(err)
				}
				appended += len(items)
			}
		}
	}
	return appended
}

// mark starts counting the commits that reach storage.
func (s *queuedStorage) mark() {
	s.mu.Lock()
	s.marked, s.entered, s.pushes = true, 0, nil
	s.mu.Unlock()
}

// markedCommits is, for each commit that appended journal entries since mark, how many; nil before it, as the Harness's own commits are not what a scenario compares.
func (s *queuedStorage) markedCommits() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.marked {
		return nil
	}
	return append([]int{}, s.pushes...)
}

func (s *queuedStorage) Commit(ctx context.Context, writes []durable.StorageWrite, adopt func(durable.Seq)) (durable.Seq, error) {
	s.mu.Lock()
	s.entered++
	if s.marked {
		if appended := journalEntriesAppended(writes); appended > 0 {
			s.pushes = append(s.pushes, appended)
		}
	}
	var turn chan error
	if s.queueing {
		turn = make(chan error, 1)
		s.waiting = append(s.waiting, turn)
	}
	s.mu.Unlock()
	if turn != nil {
		select {
		case err := <-turn:
			if err != nil {
				return 0, err
			}
		case <-ctx.Done():
			return 0, context.Cause(ctx)
		}
	}
	s.mu.Lock()
	failure := s.failure
	s.failure = nil
	s.mu.Unlock()
	if failure != nil {
		return 0, failure
	}
	return s.MemoryStorage.Commit(ctx, writes, adopt)
}

// gatedSandbox is the fixture's sandbox with what the VM reports and the call's request to stop it held on request
// (the `vm` of the oracle): events queues what the VM reports, in order, and abort lets the call's request to stop the VM wait.
type gatedSandbox struct {
	inner Sandbox
	mu    sync.Mutex
	// holdEvents queues every report of the VM until releaseEvents.
	holdEvents bool
	queued     []func()
	// holdAbort keeps the VM running after the call asked to stop it, until releaseAbort.
	holdAbort      bool
	heldAborts     []func()
	abortRequested chan struct{}
	requestedOnce  sync.Once
	// ended is closed once a VM's run has its result.
	ended     chan struct{}
	endedOnce sync.Once
}

func newGatedSandbox(inner Sandbox) *gatedSandbox {
	return &gatedSandbox{inner: inner, abortRequested: make(chan struct{}), ended: make(chan struct{})}
}

func (g *gatedSandbox) Start(ctx context.Context, code string, options cm.RunOptions) (*cm.Run, error) {
	own, stop := context.WithCancelCause(context.WithoutCancel(ctx))
	go func() {
		<-ctx.Done()
		g.requestedOnce.Do(func() { close(g.abortRequested) })
		cause := context.Cause(ctx)
		g.mu.Lock()
		if g.holdAbort {
			g.heldAborts = append(g.heldAborts, func() { stop(cause) })
			g.mu.Unlock()
			return
		}
		g.mu.Unlock()
		stop(cause)
	}()
	forward := func(report func()) {
		g.mu.Lock()
		if g.holdEvents {
			g.queued = append(g.queued, report)
			g.mu.Unlock()
			return
		}
		g.mu.Unlock()
		report()
	}
	started, call, idle := options.OnStarted, options.OnCall, options.OnIdle
	options.OnStarted = func(fingerprint string) { forward(func() { started(fingerprint) }) }
	options.OnCall = func(made cm.HostCall) { forward(func() { call(made) }) }
	options.OnIdle = func(seq, issued int) { forward(func() { idle(seq, issued) }) }
	run, err := g.inner.Start(own, code, options)
	if err == nil {
		go func() {
			run.Result()
			g.endedOnce.Do(func() { close(g.ended) })
		}()
	}
	return run, err
}

func (g *gatedSandbox) releaseEvents() {
	g.mu.Lock()
	g.holdEvents = false
	queued := g.queued
	g.queued = nil
	g.mu.Unlock()
	for _, report := range queued {
		report()
	}
}

func (g *gatedSandbox) releaseAbort() {
	g.mu.Lock()
	g.holdAbort = false
	held := g.heldAborts
	g.heldAborts = nil
	g.mu.Unlock()
	for _, stop := range held {
		stop()
	}
}

// restartRun is one scenario under PiG.
type restartRun struct {
	t       *testing.T
	f       *codemodeFixture
	storage *queuedStorage
	vm      *gatedSandbox
	clock   *manualClock
	counter atomic.Int64
	// finishedGlobals is the gslow calls that returned.
	finishedGlobals atomic.Int64
	opened          harness.Harness
	root            harness.Conversation
	id              durable.SubmissionId
	sub             durable.Submission
	status          any
	mu              sync.Mutex
	reports         []string
	seen            []map[string]any
	closing         chan error
	editing         []chan struct{}
	known           durable.TaskId
	closed          bool
	// scenarioCode is the script the scenario's first step submits.
	scenarioCode string
}

func newRestartRun(t *testing.T, scenario restartScenario) *restartRun {
	t.Helper()
	r := &restartRun{t: t, storage: &queuedStorage{MemoryStorage: storage.NewMemoryStorage()}, clock: newManualClock()}
	r.clock.ms.Store(restartBaseClock)
	implementations := map[string]func(context.Context, json.RawMessage, durable.ToolExecutionApi) (json.RawMessage, error){
		"next": func(context.Context, json.RawMessage, durable.ToolExecutionApi) (json.RawMessage, error) {
			return json.RawMessage(fmt.Sprint(r.counter.Add(1))), nil
		},
		"gslow": func(ctx context.Context, args json.RawMessage, _ durable.ToolExecutionApi) (json.RawMessage, error) {
			var id int
			if err := json.Unmarshal(args, &id); err != nil {
				return nil, err
			}
			if err := r.f.slowGate(id).wait(ctx); err != nil {
				return nil, err
			}
			r.finishedGlobals.Add(1)
			return json.RawMessage(fmt.Sprint(id)), nil
		},
		"constant": constantGlobal(`"c"`),
		"boom": func(context.Context, json.RawMessage, durable.ToolExecutionApi) (json.RawMessage, error) {
			return nil, fmt.Errorf("boom")
		},
		"undef": func(context.Context, json.RawMessage, durable.ToolExecutionApi) (json.RawMessage, error) {
			return nil, nil
		},
	}
	var globals []Global
	for _, name := range scenario.Globals {
		globals = append(globals, Global{Name: name, Execute: implementations[name]})
	}
	r.f = codemodeChat(t, globals...)
	r.vm = newGatedSandbox(r.f.vm)
	r.f.setup.registry.Uninstall(r.f.ext)
	r.f.ext = &durable.Extension{Name: "tool:codemode", Tools: []*durable.ToolRegistration{CreateTool(ToolOptions{Sandbox: r.vm, Globals: globals})}}
	if err := r.f.setup.registry.Install(r.f.ext); err != nil {
		t.Fatal(err)
	}
	r.f.setup.now = r.clock.now
	failed := true
	addTool(t, r.f.setup.registry, tool("fails", func(context.Context, toolArgs, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Output: textResult("permission denied").Output, IsError: &failed}, nil
	}, durable.ReplayUnsafe))
	addTool(t, r.f.setup.registry, tool("next", func(context.Context, toolArgs, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return textResult("tool next"), nil
	}, durable.ReplayUnsafe))
	return r
}

func (r *restartRun) open(rearm, unblock bool) {
	r.t.Helper()
	r.storage.Reopen()
	r.closed = false
	if rearm {
		r.f.holds.reached = newGate()
	}
	if unblock {
		r.f.holds.block.Store(false)
	}
	opened, err := harness.OpenHarness(testContext, r.storage, harness.HarnessOptions{
		Models: r.f.setup.models, Registry: r.f.setup.registry, Now: r.clock.now,
		OnReport: func(err error, _ durable.ReportInfo) {
			r.mu.Lock()
			r.reports = append(r.reports, err.Error())
			r.mu.Unlock()
		},
	})
	if err != nil {
		r.t.Fatal(err)
	}
	root, err := opened.Root(testContext, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		r.t.Fatal(err)
	}
	r.opened, r.root = opened, root
}

func pathKeys(raw []json.RawMessage) []any {
	keys := make([]any, len(raw))
	for i, element := range raw {
		var key any
		if err := json.Unmarshal(element, &key); err != nil {
			panic(err)
		}
		if number, ok := key.(float64); ok {
			key = int(number)
		}
		keys[i] = key
	}
	return keys
}

// editDraft changes a document the way the step names, through the draft handles the Harness gives a transaction.
func editDraft(t *testing.T, draft *delta.Object, step restartStep) {
	t.Helper()
	path := pathKeys(step.Path)
	walk := path
	if step.Length == nil && !step.Push {
		walk = path[:len(path)-1]
	}
	var node any = draft
	for _, key := range walk {
		switch parent := node.(type) {
		case *delta.Object:
			name := key.(string)
			if next := parent.Object(name); next != nil {
				node = next
			} else {
				node = parent.Array(name)
			}
		case *delta.Array:
			index := key.(int)
			if next := parent.Object(index); next != nil {
				node = next
			} else {
				node = parent.Array(index)
			}
		}
		if node == nil || reflect.ValueOf(node).IsNil() {
			t.Fatalf("edit %v: nothing at %v", path, key)
		}
	}
	if step.Length != nil {
		node.(*delta.Array).SetLen(*step.Length)
		return
	}
	if step.Push {
		var value any
		if err := json.Unmarshal(step.Value, &value); err != nil {
			t.Fatal(err)
		}
		if _, err := node.(*delta.Array).Push(value); err != nil {
			t.Fatal(err)
		}
		return
	}
	last := path[len(path)-1]
	if step.Delete {
		node.(*delta.Object).Delete(last.(string))
		return
	}
	var value any
	if err := json.Unmarshal(step.Value, &value); err != nil {
		t.Fatal(err)
	}
	switch parent := node.(type) {
	case *delta.Object:
		mustSet(t, parent.Set(last.(string), value))
	case *delta.Array:
		mustSet(t, parent.Set(last.(int), value))
	}
}

func (r *restartRun) edit(step restartStep) {
	r.t.Helper()
	task := r.scriptTask()
	_, err := durable.Commit(testContext, r.opened, func(tx durable.Tx) (struct{}, error) {
		var draft *delta.Object
		var err error
		switch step.Doc {
		case "run":
			draft, err = durable.TxDoc[runState](tx, RunDoc, task)
		case "journal":
			draft, err = durable.TxDoc[journalState](tx, JournalDoc, task)
		default:
			draft, err = durable.TxDoc[StoreState](tx, StoreDoc, r.root.Id())
		}
		if err == nil {
			editDraft(r.t, draft, step)
		}
		return struct{}{}, err
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

// document is the stored document as JSON, without what differs between two runs of a scenario.
func (r *restartRun) document(token durable.AnyDocToken, owner any) any {
	r.t.Helper()
	value, err := r.opened.SnapshotErased(testContext, token, owner)
	if err != nil {
		r.t.Fatal(err)
	}
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		r.t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		r.t.Fatal(err)
	}
	if start, ok := decoded["start"].(map[string]any); ok {
		delete(start, "seed")
		delete(start, "fingerprint")
	}
	return decoded
}

// scriptTaskOrZero is the code mode call's task. A read goes through the Session's line, behind a commit held in storage, so
// the task is looked up once.
func (r *restartRun) scriptTaskOrZero() (durable.TaskId, bool) {
	if r.known != 0 {
		return r.known, true
	}
	for _, task := range toolTasks(r.t, r.opened) {
		if toolInput(task).Kind == harness.ToolTaskModel {
			r.known = task.Id
			return task.Id, true
		}
	}
	return 0, false
}

func (r *restartRun) scriptTask() durable.TaskId {
	task, ok := r.scriptTaskOrZero()
	if !ok {
		r.t.Fatal("no model tool task")
	}
	return task
}

func (r *restartRun) journal() *journalState {
	task, ok := r.scriptTaskOrZero()
	if !ok {
		return nil
	}
	return journalOf(r.t, r.opened, task)
}

func (r *restartRun) observe(label string, light bool) {
	r.t.Helper()
	if light {
		echo := map[string]any{}
		for text, count := range r.f.echoCounts() {
			echo[text] = count
		}
		r.mu.Lock()
		reports := append([]string{}, r.reports...)
		r.mu.Unlock()
		r.seen = append(r.seen, map[string]any{
			"label":   label,
			"counts":  map[string]any{"commits": r.storage.markedCommits(), "starts": r.f.vm.starts.Load(), "echo": echo, "holds": r.f.holds.runs.Load(), "next": r.counter.Load()},
			"reports": reports,
			"status":  r.status,
		})
		return
	}
	observation := map[string]any{"label": label, "result": nil, "run": nil, "journal": nil, "status": r.status}
	var lines []string
	for _, entry := range allEntries(r.t, r.root) {
		if len(entry.Model) == 0 {
			lines = append(lines, "-")
			continue
		}
		encoded, err := json.Marshal(entry.Model[0])
		if err != nil {
			r.t.Fatal(err)
		}
		var message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			IsError bool            `json:"isError"`
			Details any             `json:"details"`
		}
		if err := json.Unmarshal(encoded, &message); err != nil {
			r.t.Fatal(err)
		}
		var text string
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(message.Content, &text) != nil {
			if err := json.Unmarshal(message.Content, &blocks); err != nil {
				r.t.Fatal(err)
			}
		}
		var joined strings.Builder
		for _, block := range blocks {
			if block.Type == "text" {
				joined.WriteString(block.Text)
			} else {
				joined.WriteString(block.Type)
			}
		}
		if len(blocks) > 0 || text == "" {
			text = joined.String()
		}
		lines = append(lines, message.Role+":"+text)
		if durable.ToolResultEntry.Is(&entry) {
			var plain strings.Builder
			for _, block := range blocks {
				if block.Type == "text" {
					plain.WriteString(block.Text)
				}
			}
			details := message.Details
			if object, ok := details.(map[string]any); ok {
				calls, _ := object["calls"].([]any)
				for _, call := range calls {
					// The durations are real time.
					delete(call.(map[string]any), "durationMs")
				}
			}
			observation["result"] = map[string]any{"text": plain.String(), "isError": message.IsError, "details": details}
		}
	}
	observation["entries"] = lines
	if task, ok := r.scriptTaskOrZero(); ok {
		observation["run"] = r.document(RunDoc, task)
		observation["journal"] = r.document(JournalDoc, task)
	}
	observation["store"] = r.document(StoreDoc, r.root.Id())
	echo := map[string]any{}
	for text, count := range r.f.echoCounts() {
		echo[text] = count
	}
	observation["counts"] = map[string]any{"commits": r.storage.markedCommits(), "starts": r.f.vm.starts.Load(), "echo": echo, "holds": r.f.holds.runs.Load(), "nested": nestedCount(r.t, r.opened), "next": r.counter.Load()}
	r.mu.Lock()
	reports := append([]string{}, r.reports...)
	r.mu.Unlock()
	observation["reports"] = reports
	r.seen = append(r.seen, observation)
}

func (r *restartRun) step(step restartStep) {
	r.t.Helper()
	switch step.Op {
	case "start":
		code := r.scenarioCode
		if step.Code != "" {
			code = step.Code
		}
		r.f.setup.faux.SetResponses([]ai.FauxResponseStep{script(code), done()})
		if r.opened == nil {
			r.open(false, false)
		}
		submission := submitGo(r.t, r.root)
		r.known = 0
		r.id, r.sub = submission.Id(), submission
		r.opened.Resume()
	case "await":
		switch step.What {
		case "hold":
			mustWait(r.t, r.f.holds.reached)
		case "journal":
			eventually(r.t, func() bool { j := r.journal(); return j != nil && len(j.Entries) >= step.Entries })
		case "issuedAfter":
			var want int
			if err := json.Unmarshal(step.Value, &want); err != nil {
				r.t.Fatal(err)
			}
			eventually(r.t, func() bool {
				j := r.journal()
				return j != nil && step.Index < len(j.Entries) && j.Entries[step.Index].IssuedAfter != nil && *j.Entries[step.Index].IssuedAfter == want
			})
		case "firstIssued":
			var want int
			if err := json.Unmarshal(step.Value, &want); err != nil {
				r.t.Fatal(err)
			}
			eventually(r.t, func() bool { j := r.journal(); return j != nil && j.FirstIssued != nil && *j.FirstIssued == want })
		case "commits":
			eventually(r.t, func() bool { return r.storage.enteredCommits() >= step.Count })
		case "globals-finished":
			eventually(r.t, func() bool { return r.finishedGlobals.Load() >= int64(step.Count) })
		case "held-events":
			eventually(r.t, func() bool { r.vm.mu.Lock(); defer r.vm.mu.Unlock(); return len(r.vm.queued) >= step.Count })
		case "vm-ended":
			<-r.vm.ended
		case "abort-requested":
			<-r.vm.abortRequested
		case "nested":
			eventually(r.t, func() bool { return nestedCount(r.t, r.opened) >= step.Count })
		case "done":
			waiting, stop := context.WithTimeout(testContext, time.Minute)
			settled, err := r.sub.Wait(waiting)
			stop()
			if err != nil {
				r.status = "error: " + err.Error()
			} else {
				r.status = string(settled.Status)
			}
		default:
			r.t.Fatalf("unknown await %s", step.What)
		}
	case "close":
		mustClose(r.t, r.opened)
		r.closed = true
	case "close-begin":
		r.closing = make(chan error, 1)
		go func() { r.closing <- r.opened.Close(testContext) }()
	case "close-end":
		if err := <-r.closing; err != nil {
			r.t.Fatal(err)
		}
		r.closed = true
	case "open":
		r.open(step.Rearm, step.Unblock)
	case "resume":
		r.opened.Resume()
		r.sub = must(r.opened.Submission(testContext, r.id))
	case "unblock":
		r.f.holds.block.Store(false)
	case "release":
		r.f.slowGate(step.ID).resolve()
	case "clock":
		if step.MS != nil {
			r.clock.ms.Store(*step.MS)
		} else {
			r.clock.ms.Add(*step.Advance)
		}
	case "edit":
		r.edit(step)
	case "edit-begin":
		editing := make(chan struct{})
		r.editing = append(r.editing, editing)
		go func() {
			defer close(editing)
			r.edit(step)
		}()
	case "edit-end":
		for _, editing := range r.editing {
			<-editing
		}
		r.editing = nil
	case "mark-commits":
		r.storage.mark()
	case "queue-commits":
		r.storage.queue()
	case "release-commit":
		var failure error
		if step.Message != "" {
			failure = fmt.Errorf("%s", step.Message)
		}
		r.storage.release(failure)
	case "unqueue-commits":
		r.storage.unqueue()
	case "fail-next-commit":
		r.storage.mu.Lock()
		r.storage.failure = fmt.Errorf("%s", step.Message)
		r.storage.mu.Unlock()
	case "uninstall-tool":
		r.f.setup.registry.Uninstall(&durable.Extension{Name: "tool:" + step.Tool})
	case "hold-events":
		r.vm.mu.Lock()
		r.vm.holdEvents = true
		r.vm.mu.Unlock()
	case "release-events":
		r.vm.releaseEvents()
	case "hold-abort":
		r.vm.mu.Lock()
		r.vm.holdAbort = true
		r.vm.mu.Unlock()
	case "release-abort":
		r.vm.releaseAbort()
	case "observe":
		r.observe(step.Label, step.Light)
	default:
		r.t.Fatalf("unknown op %s", step.Op)
	}
}

// runRestartScenarios runs each scenario under Pi and under PiG and compares what they observed.
func runRestartScenarios(t *testing.T, scenarios []restartScenario) {
	t.Helper()
	root := testenv.ModuleRoot(t)
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/codemode_restart_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi's restart oracle: %v\n%s", err, stderr.String())
	}
	var pi []struct {
		OK  []map[string]any `json:"ok"`
		Err string           `json:"err"`
	}
	if err := json.Unmarshal(output, &pi); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if len(pi) != len(scenarios) {
		t.Fatalf("Pi answered %d scenarios, want %d", len(pi), len(scenarios))
	}
	for i, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			if pi[i].Err != "" {
				t.Fatalf("Pi's oracle failed: %s", pi[i].Err)
			}
			r := newRestartRun(t, scenario)
			r.scenarioCode = scenario.Code
			for _, step := range scenario.Steps {
				r.step(step)
			}
			if !r.closed {
				mustClose(t, r.opened)
			}
			got, err := json.Marshal(r.seen)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(pi[i].OK)
			if err != nil {
				t.Fatal(err)
			}
			var gotValue, wantValue any
			if err := json.Unmarshal(got, &gotValue); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(want, &wantValue); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotValue, wantValue) {
				t.Fatalf("PiG observed what Pi did not, first at %s", firstDifference("", gotValue, wantValue))
			}
		})
	}
}

// Step builders: the scenarios read as the sequence of what a host does.
func restartOp(op string) restartStep { return restartStep{Op: op} }

func awaitStep(what string) restartStep { return restartStep{Op: "await", What: what} }

func observeAt(label string) restartStep { return restartStep{Op: "observe", Label: label} }

// reopen is what follows a restart: the Harness opens over the stored documents with hold no longer blocking.
func reopen() restartStep { return restartStep{Op: "open", Unblock: true} }

// docPath is a path through a document: strings are keys, ints are indexes.
func docPath(path []any) []json.RawMessage {
	keys := make([]json.RawMessage, len(path))
	for i, key := range path {
		encoded, err := json.Marshal(key)
		if err != nil {
			panic(err)
		}
		keys[i] = encoded
	}
	return keys
}

func setDoc(doc string, value string, path ...any) restartStep {
	return restartStep{Op: "edit", Doc: doc, Path: docPath(path), Value: json.RawMessage(value)}
}

func deleteDoc(doc string, path ...any) restartStep {
	return restartStep{Op: "edit", Doc: doc, Path: docPath(path), Delete: true}
}

// truncateDoc sets the length of the array at path.
func truncateDoc(doc string, length int, path ...any) restartStep {
	return restartStep{Op: "edit", Doc: doc, Path: docPath(path), Length: &length}
}

// firstDifference names the first place two decoded JSON values differ, with both sides' values there.
func firstDifference(path string, got, want any) string {
	switch got := got.(type) {
	case map[string]any:
		if want, ok := want.(map[string]any); ok {
			for _, key := range sortedKeys(got) {
				if _, found := want[key]; !found {
					return fmt.Sprintf("%s.%s: PiG has %s, Pi has none", path, key, compactJSON(got[key]))
				}
				if difference := firstDifference(path+"."+key, got[key], want[key]); difference != "" {
					return difference
				}
			}
			for _, key := range sortedKeys(want) {
				if _, found := got[key]; !found {
					return fmt.Sprintf("%s.%s: PiG has none, Pi has %s", path, key, compactJSON(want[key]))
				}
			}
			return ""
		}
	case []any:
		if want, ok := want.([]any); ok {
			for i := 0; i < len(got) && i < len(want); i++ {
				if difference := firstDifference(fmt.Sprintf("%s[%d]", path, i), got[i], want[i]); difference != "" {
					return difference
				}
			}
			if len(got) != len(want) {
				return fmt.Sprintf("%s: PiG has %d items %s, Pi has %d items %s", path, len(got), compactJSON(got), len(want), compactJSON(want))
			}
			return ""
		}
	default:
		if reflect.DeepEqual(got, want) {
			return ""
		}
	}
	return fmt.Sprintf("%s: PiG has %s, Pi has %s", path, compactJSON(got), compactJSON(want))
}

func compactJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}
