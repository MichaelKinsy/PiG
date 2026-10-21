// Ports the helpers packages/durable/test/codemode.test.ts takes from chat-support.ts, harness-support.ts and
// task-support.ts.

package codemode

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
	"github.com/MichaelKinsy/PiG/telemetry"
)

var testContext = context.Background()

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

// gate is task-support.ts deferred(): resolved once.
type gate struct {
	once sync.Once
	done chan struct{}
}

func newGate() *gate { return &gate{done: make(chan struct{})} }

func (g *gate) resolve() { g.once.Do(func() { close(g.done) }) }

func (g *gate) wait(ctx context.Context) error {
	select {
	case <-g.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// abortedBy is task-support.ts aborted(signal): it fails with the signal's cause once the signal ends.
func abortedBy(signal context.Context) error {
	<-signal.Done()
	return context.Cause(signal)
}

// eventually flushes scheduling turns until check holds (task-support.ts eventually).
func eventually(t *testing.T, check func() bool) {
	t.Helper()
	for range 2000 {
		if check() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Condition was not reached")
}

func openSqlite(t *testing.T, path string) durable.Storage {
	t.Helper()
	storage, err := openSqliteStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	return storage
}

func openSqliteStorage(path string) (durable.Storage, error) {
	return sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
}

// chat is chat-support.ts ChatSetup: the models and registry that survive a close and reopen, like a host process's own
// objects.
type chat struct {
	faux     *ai.FauxProviderHandle
	models   *ai.Models
	registry harness.Registry
	// telemetry is the adapter the Harness opens with; nil opens it without telemetry.
	telemetry telemetry.TelemetryContext
}

func chatSetup() *chat {
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	return &chat{faux: faux, models: models, registry: harness.CreateRegistry()}
}

func (c *chat) open(t *testing.T, storage durable.Storage) (harness.Harness, harness.Conversation) {
	t.Helper()
	return c.openWith(t, storage, nil)
}

// openWith opens the Harness with an execution environment, when given.
func (c *chat) openWith(t *testing.T, storage durable.Storage, environment env.ExecutionEnv) (harness.Harness, harness.Conversation) {
	t.Helper()
	options := harness.HarnessOptions{Models: c.models, Registry: c.registry, Telemetry: c.telemetry}
	if environment != nil {
		options.Env = func(context.Context, harness.EnvTarget) (env.ExecutionEnv, error) { return environment, nil }
	}
	opened, err := harness.OpenHarness(testContext, storage, options)
	if err != nil {
		t.Fatal(err)
	}
	root, err := opened.Root(testContext, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	return opened, root
}

func allEntries(t *testing.T, conversation harness.Conversation) []durable.EntryRecord {
	t.Helper()
	page, err := conversation.Entries(testContext, durable.EntryQuery{}, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]durable.EntryRecord, 0, len(page.Items))
	for _, item := range slices.Backward(page.Items) {
		entries = append(entries, item)
	}
	return entries
}

func addTool(t *testing.T, registry harness.Registry, tool *durable.ToolRegistration) {
	t.Helper()
	if err := registry.Install(&durable.Extension{Name: "tool:" + tool.Name, Tools: []*durable.ToolRegistration{tool}}); err != nil {
		t.Fatal(err)
	}
}

const parametersSchema = `{"type":"object","properties":{"text":{"type":"string"},"id":{"type":"number"}}}`

// tool is the test tools' shape: Parameters {text?, id?}.
func tool(name string, execute func(ctx context.Context, args toolArgs, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error), replay durable.ToolReplay) *durable.ToolRegistration {
	var parameters map[string]any
	if err := json.Unmarshal([]byte(parametersSchema), &parameters); err != nil {
		panic(err)
	}
	return harness.DefineTool(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: name, Description: name, Parameters: parameters},
		Replay:     replay,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			decoded, err := durable.FromJsonValue[toolArgs](args)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return execute(ctx, decoded, api)
		},
	})
}

type toolArgs struct {
	Text *string  `json:"text"`
	ID   *float64 `json:"id"`
}

func (a toolArgs) text() string {
	if a.Text == nil {
		return ""
	}
	return *a.Text
}

func (a toolArgs) id() string {
	if a.ID == nil {
		return ""
	}
	return jsNumber(*a.ID)
}

func jsNumber(n float64) string {
	encoded, _ := json.Marshal(n)
	return string(encoded)
}

func textResult(text string) durable.ToolExecutionResult {
	return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}
}

func script(code string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(ToolName, map[string]any{"code": code}, &ai.FauxToolCallOptions{ID: "s1"})}, StopReason: "toolUse"})
}

func done() ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}})
}

// vmCount wraps a sandbox to count the VMs started and to let a test see each run.
type vmCount struct {
	sandbox *cm.Sandbox
	starts  atomic.Int32
	mu      sync.Mutex
	onStart func(*cm.Run)
}

func (v *vmCount) Start(ctx context.Context, code string, options cm.RunOptions) (*cm.Run, error) {
	v.starts.Add(1)
	run, err := v.sandbox.Start(ctx, code, options)
	v.mu.Lock()
	hook := v.onStart
	v.mu.Unlock()
	if err == nil && hook != nil {
		hook(run)
	}
	return run, err
}

// codemodeChat is a chat with the code mode tool and test tools:
//   - echo returns `echo <text>` and counts its runs by text;
//   - hold (replay-safe) waits for the holds gate while holds.block is set, then returns `held <id>`;
//   - pause (replay-safe) waits while pauses.block is set, then returns `paused`;
//   - slow returns `slow <id>` once slows.release(id) was called.
type codemodeFixture struct {
	setup   *chat
	ext     *durable.Extension
	sandbox *cm.Sandbox
	globals []Global
	vm      *vmCount
	mu      sync.Mutex
	echo    map[string]int
	holds   struct {
		block   atomic.Bool
		reached *gate
		runs    atomic.Int32
	}
	pauses struct {
		block   atomic.Bool
		reached *gate
	}
	slows struct {
		mu    sync.Mutex
		gates map[int]*gate
	}
	// onFirstOpen, when set, sees restart's first Harness before the script starts.
	onFirstOpen func(harness.Harness)
}

func (f *codemodeFixture) echoCounts() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts := map[string]int{}
	maps.Copy(counts, f.echo)
	return counts
}

func (f *codemodeFixture) slowGate(id int) *gate {
	f.slows.mu.Lock()
	defer f.slows.mu.Unlock()
	g, ok := f.slows.gates[id]
	if !ok {
		g = newGate()
		f.slows.gates[id] = g
	}
	return g
}

func codemodeChat(t *testing.T, globals ...Global) *codemodeFixture {
	t.Helper()
	return codemodeChatWith(t, ToolOptions{Globals: globals})
}

// codemodeChatWith is codemodeChat with the tool's options (its sandbox is the fixture's).
func codemodeChatWith(t *testing.T, options ToolOptions) *codemodeFixture {
	t.Helper()
	sandbox, err := cm.NewSandbox(cm.SandboxOptions{MemoryLimitBytes: 64 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sandbox.Close(); err != nil {
			t.Error(err)
		}
	})
	f := &codemodeFixture{setup: chatSetup(), sandbox: sandbox, vm: &vmCount{sandbox: sandbox}, echo: map[string]int{}}
	f.holds.block.Store(true)
	f.holds.reached = newGate()
	f.pauses.block.Store(true)
	f.pauses.reached = newGate()
	f.slows.gates = map[int]*gate{}
	f.globals = options.Globals
	f.ext = &durable.Extension{Name: "tool:codemode", Tools: []*durable.ToolRegistration{CreateTool(withSandbox(options, f.vm))}}
	if err := f.setup.registry.Install(f.ext); err != nil {
		t.Fatal(err)
	}
	addTool(t, f.setup.registry, tool("echo", func(_ context.Context, args toolArgs, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		f.mu.Lock()
		f.echo[args.text()]++
		f.mu.Unlock()
		return textResult("echo " + args.text()), nil
	}, durable.ReplayUnsafe))
	addTool(t, f.setup.registry, tool("hold", func(ctx context.Context, args toolArgs, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		f.holds.runs.Add(1)
		if f.holds.block.Load() {
			f.holds.reached.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}
		return textResult("held " + args.id()), nil
	}, durable.ReplaySafe))
	addTool(t, f.setup.registry, tool("pause", func(ctx context.Context, _ toolArgs, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		if f.pauses.block.Load() {
			f.pauses.reached.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}
		return textResult("paused"), nil
	}, durable.ReplaySafe))
	addTool(t, f.setup.registry, tool("slow", func(ctx context.Context, args toolArgs, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		id := 0
		if args.ID != nil {
			id = int(*args.ID)
		}
		if err := f.slowGate(id).wait(ctx); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return textResult("slow " + args.id()), nil
	}, durable.ReplayUnsafe))
	return f
}

func withSandbox(options ToolOptions, sandbox Sandbox) ToolOptions {
	options.Sandbox = sandbox
	return options
}

// scriptTask is the task of the code mode call (the model-issued tool task).
func scriptTask(t *testing.T, opened harness.Harness) durable.TaskId {
	t.Helper()
	for _, task := range toolTasks(t, opened) {
		if input := toolInput(task); input.Kind == harness.ToolTaskModel {
			return task.Id
		}
	}
	t.Fatal("no model tool task")
	return 0
}

var toolTaskKind = harness.ToolTask.AnyDefinition().Name

func toolTasks(t *testing.T, opened harness.Harness) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
	t.Helper()
	page, err := durable.Commit(testContext, opened, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
		return tx.ScanTasks(durable.TaskQuery{Kind: &toolTaskKind}, 100, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

func toolInput(task durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) harness.ToolTaskInput {
	return must(durable.FromJsonValue[harness.ToolTaskInput](task.Input))
}

func nestedCount(t *testing.T, opened harness.Harness) int {
	t.Helper()
	count := 0
	for _, task := range toolTasks(t, opened) {
		if toolInput(task).Kind == harness.ToolTaskNested {
			count++
		}
	}
	return count
}

// resultText is the code mode call's result text.
func resultText(t *testing.T, root harness.Conversation) (text string, isError bool) {
	t.Helper()
	for _, entry := range allEntries(t, root) {
		if durable.ToolResultEntry.Is(&entry) {
			message := entry.Model[0].(ai.ToolResultMessage)
			parts := make([]string, 0, len(message.Content))
			for _, item := range message.Content {
				if text, ok := item.(ai.TextContent); ok {
					parts = append(parts, text.Text)
				} else {
					parts = append(parts, "")
				}
			}
			// Without the "Script completed" header and its wall time, which tests of it check themselves.
			return headerPattern.ReplaceAllString(strings.Join(parts, ""), ""), message.IsError
		}
	}
	t.Fatal("no tool result entry")
	return "", false
}

func submitGo(t *testing.T, root harness.Conversation) durable.Submission {
	t.Helper()
	return must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")}))
}

// restart runs code until holds.reached, closes the Harness (a restart), lets between change the stored state, then
// reopens with hold no longer blocking and waits for the answer.
func (f *codemodeFixture) restart(t *testing.T, code string, hooks ...func(opened harness.Harness, task durable.TaskId)) (harness.Harness, harness.Conversation) {
	t.Helper()
	var between, beforeClose func(harness.Harness, durable.TaskId)
	if len(hooks) > 0 {
		between = hooks[0]
	}
	if len(hooks) > 1 {
		beforeClose = hooks[1]
	}
	path := filepath.Join(t.TempDir(), "session.sqlite")
	f.setup.faux.SetResponses([]ai.FauxResponseStep{script(code), done()})
	opened, root := f.setup.open(t, openSqlite(t, path))
	if f.onFirstOpen != nil {
		f.onFirstOpen(opened)
	}
	id := submitGo(t, root).Id()
	opened.Resume()
	if err := f.holds.reached.wait(testContext); err != nil {
		t.Fatal(err)
	}
	if beforeClose != nil {
		beforeClose(opened, scriptTask(t, opened))
	}
	if err := opened.Close(testContext); err != nil {
		t.Fatal(err)
	}
	f.holds.block.Store(false)
	opened, root = f.setup.open(t, openSqlite(t, path))
	if between != nil {
		between(opened, scriptTask(t, opened))
	}
	opened.Resume()
	submission := must(opened.Submission(testContext, id))
	if settled := must(submission.Wait(testContext)); settled.Status != durable.SubmissionDone {
		t.Fatalf("submission %s, want done", settled.Status)
	}
	return opened, root
}

// editJournal changes the stored journal of the code mode task in one commit (`opened.commit(tx => tx.doc(CodemodeJournalDoc, task))`).
func editJournal(t *testing.T, opened harness.Harness, task durable.TaskId, change func(journal durable.Draft[journalState])) {
	t.Helper()
	_, err := durable.Commit(testContext, opened, func(tx durable.Tx) (struct{}, error) {
		journal, err := durable.TxDoc[journalState](tx, JournalDoc, task)
		if err == nil {
			change(journal)
		}
		return struct{}{}, err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// editRun changes the stored run document of the code mode task in one commit.
func editRun(t *testing.T, opened harness.Harness, task durable.TaskId, change func(run durable.Draft[runState])) {
	t.Helper()
	_, err := durable.Commit(testContext, opened, func(tx durable.Tx) (struct{}, error) {
		run, err := durable.TxDoc[runState](tx, RunDoc, task)
		if err == nil {
			change(run)
		}
		return struct{}{}, err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// journalOf is the committed journal of the code mode task; nil when absent.
func journalOf(t *testing.T, opened harness.Harness, task durable.TaskId) *journalState {
	t.Helper()
	journal, err := durable.Snapshot[journalState](testContext, opened, JournalDoc, task)
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

// issuedAfterIs reports whether the journal's first entry records n calls.
func issuedAfterIs(t *testing.T, opened harness.Harness, task durable.TaskId, n int) bool {
	journal := journalOf(t, opened, task)
	return journal != nil && len(journal.Entries) > 0 && journal.Entries[0].IssuedAfter != nil && *journal.Entries[0].IssuedAfter == n
}

func mustSet(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustClose(t *testing.T, opened harness.Harness) {
	t.Helper()
	if err := opened.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

func mustWait(t *testing.T, g *gate) {
	t.Helper()
	if err := g.wait(testContext); err != nil {
		t.Fatal(err)
	}
}

// replaceSandbox installs a code mode tool over a new sandbox, as the next process of a host has.
func (f *codemodeFixture) replaceSandbox(t *testing.T) {
	t.Helper()
	sandbox, err := cm.NewSandbox(cm.SandboxOptions{MemoryLimitBytes: 64 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sandbox.Close(); err != nil {
			t.Error(err)
		}
	})
	f.setup.registry.Uninstall(f.ext)
	f.ext = &durable.Extension{Name: "tool:codemode", Tools: []*durable.ToolRegistration{CreateTool(ToolOptions{Sandbox: sandbox})}}
	if err := f.setup.registry.Install(f.ext); err != nil {
		t.Fatal(err)
	}
}

// heldStorage is memory storage whose commits can be held (session-support.ts ControlledStorage.holdCommits).
type heldStorage struct {
	*storage.MemoryStorage
	mu   sync.Mutex
	held *gate
	fail error
}

// failNextCommit makes the next commit fail with err.
func (s *heldStorage) failNextCommit(err error) {
	s.mu.Lock()
	s.fail = err
	s.mu.Unlock()
}

func newHeldStorage() *heldStorage { return &heldStorage{MemoryStorage: storage.NewMemoryStorage()} }

// holdCommits makes every commit wait until the returned gate is resolved.
func (s *heldStorage) holdCommits() *gate {
	g := newGate()
	s.mu.Lock()
	s.held = g
	s.mu.Unlock()
	return g
}

func (s *heldStorage) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	s.mu.Lock()
	held, fail := s.held, s.fail
	s.fail = nil
	s.mu.Unlock()
	if fail != nil {
		return 0, fail
	}
	if held != nil {
		if err := held.wait(ctx); err != nil {
			return 0, err
		}
	}
	return s.MemoryStorage.Commit(ctx, writes)
}

// detailsOf is the details of the code mode call's result entry.
func detailsOf(t *testing.T, root harness.Conversation) ToolDetails {
	t.Helper()
	for _, entry := range allEntries(t, root) {
		if durable.ToolResultEntry.Is(&entry) {
			encoded, err := json.Marshal(entry.Model[0].(ai.ToolResultMessage).Details)
			if err != nil {
				t.Fatal(err)
			}
			var details ToolDetails
			if err := json.Unmarshal(encoded, &details); err != nil {
				t.Fatal(err)
			}
			return details
		}
	}
	t.Fatal("no tool result entry")
	return ToolDetails{}
}

func constantGlobal(value string) func(context.Context, json.RawMessage, durable.ToolExecutionApi) (json.RawMessage, error) {
	return func(context.Context, json.RawMessage, durable.ToolExecutionApi) (json.RawMessage, error) {
		return json.RawMessage(value), nil
	}
}

func mustSubmitDone(t *testing.T, root harness.Conversation) {
	t.Helper()
	if _, err := submitGo(t, root).Wait(testContext); err != nil {
		t.Fatal(err)
	}
}

func resultEntries(t *testing.T, conversation harness.Conversation) []durable.EntryRecord {
	t.Helper()
	var results []durable.EntryRecord
	for _, entry := range allEntries(t, conversation) {
		if durable.ToolResultEntry.Is(&entry) {
			results = append(results, entry)
		}
	}
	return results
}

// resultMessageText is the first text of a tool result entry's message.
// resultMessageText is the text of the result's second content item: the first is the header.
func resultMessageText(entry durable.EntryRecord) string {
	content := entry.Model[0].(ai.ToolResultMessage).Content
	if len(content) < 2 {
		return ""
	}
	text, _ := content[1].(ai.TextContent)
	return text.Text
}

var headerPattern = regexp.MustCompile(`^Script (completed|failed)\nWall time [\d.]+ seconds\nOutput:\n`)
