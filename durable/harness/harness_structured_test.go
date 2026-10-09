// Ports packages/durable/test/harness-structured.test.ts.

package harness

// pi: packages/durable/src/harness/events.ts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// ─── A scriptable task ──────────────────────────────────────────────────────

type nodeInput struct {
	Name string `json:"name"`
}

// nodeCheckpoint is {phase: "run"} or {phase: "resume", round}.
type nodeCheckpoint struct {
	Phase string `json:"phase"`
	Round int    `json:"round,omitempty"`
}

// MarshalJSON writes round for the resume phase only, as upstream's union does.
func (checkpoint nodeCheckpoint) MarshalJSON() ([]byte, error) {
	if checkpoint.Phase != "resume" {
		return json.Marshal(map[string]any{"phase": checkpoint.Phase})
	}
	return json.Marshal(map[string]any{"phase": checkpoint.Phase, "round": checkpoint.Round})
}

type nodeRuntime = durable.TaskRuntime[nodeInput, nodeCheckpoint, string, any]
type nodeRecord = durable.RunningTask[nodeInput, nodeCheckpoint, string]
type nodeNext = durable.NextTaskState[nodeCheckpoint, string]

// nodeBehavior is the per-name behavior of nodeTask; unscripted parts use the defaults.
type nodeBehavior struct {
	run    func(ctx context.Context, runtime nodeRuntime) error
	resume func(ctx context.Context, runtime nodeRuntime, round int) error
	abort  func(ctx context.Context, runtime nodeRuntime) error
}

// endingGate opens with how a default run ends: completed, failed, or throw.
type endingGate struct {
	once   sync.Once
	done   chan struct{}
	ending string
}

func (gate *endingGate) open(ending string) {
	gate.once.Do(func() {
		gate.ending = ending
		close(gate.done)
	})
}

func (gate *endingGate) wait(ctx context.Context) (string, error) {
	select {
	case <-gate.done:
		return gate.ending, nil
	case <-ctx.Done():
		return "", context.Cause(ctx)
	}
}

// nodes holds the behaviors, gates, and handler log of the scriptable task; reset per test.
var nodes = struct {
	mu        sync.Mutex
	behaviors map[string]nodeBehavior
	gates     map[string]*endingGate
	log       []string
}{behaviors: map[string]nodeBehavior{}, gates: map[string]*endingGate{}}

// resetNodes clears the shared state after the test, as upstream's afterEach does; behaviors scripted before
// openNodes stay.
func resetNodes(t *testing.T) {
	t.Helper()
	clear := func() {
		nodes.mu.Lock()
		nodes.behaviors = map[string]nodeBehavior{}
		nodes.gates = map[string]*endingGate{}
		nodes.log = nil
		nodes.mu.Unlock()
	}
	t.Cleanup(clear)
}

func nodeGate(name string) *endingGate {
	nodes.mu.Lock()
	defer nodes.mu.Unlock()
	found := nodes.gates[name]
	if found == nil {
		found = &endingGate{done: make(chan struct{})}
		nodes.gates[name] = found
	}
	return found
}

func openGate(name string, ending ...string) {
	how := "completed"
	if len(ending) > 0 {
		how = ending[0]
	}
	nodeGate(name).open(how)
}

func scriptNode(name string, behavior nodeBehavior) {
	nodes.mu.Lock()
	nodes.behaviors[name] = behavior
	nodes.mu.Unlock()
}

func logLine(line string) {
	nodes.mu.Lock()
	nodes.log = append(nodes.log, line)
	nodes.mu.Unlock()
}

func nodeLog() []string {
	nodes.mu.Lock()
	defer nodes.mu.Unlock()
	return slices.Clone(nodes.log)
}

func logged(line string) bool { return slices.Contains(nodeLog(), line) }

func logWithPrefix(prefix string) []string {
	lines := []string{}
	for _, line := range nodeLog() {
		if strings.HasPrefix(line, prefix) {
			lines = append(lines, line)
		}
	}
	return lines
}

func behaviorOf(name string) nodeBehavior {
	nodes.mu.Lock()
	defer nodes.mu.Unlock()
	return nodes.behaviors[name]
}

// defaultRun waits for the gate or the abort signal, then ends as the gate says.
func defaultRun(ctx context.Context, runtime nodeRuntime, name string) error {
	ending, err := nodeGate(name).wait(runtime.Signal())
	if err != nil {
		return err
	}
	if ending == "throw" {
		return fmt.Errorf("%s threw", name)
	}
	return runtime.Commit(ctx, func(durable.Tx, nodeRecord) (*nodeNext, error) { return endNode(ending, name), nil })
}

func endNode(ending, name string) *nodeNext {
	if ending == "completed" {
		return &nodeNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeCompleted, Result: &name}}
	}
	return &nodeNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: name + " failed"}}}
}

func commitNext(ctx context.Context, runtime nodeRuntime, next func(tx durable.Tx) (*nodeNext, error)) error {
	return runtime.Commit(ctx, func(tx durable.Tx, _ nodeRecord) (*nodeNext, error) { return next(tx) })
}

// nodeTask runs its name's behavior.
var nodeTask = durable.DefineTask(durable.TaskDefinition[nodeInput, nodeCheckpoint, string, any]{
	Name:    "test.node",
	Version: 1,
	Initial: func(nodeInput) nodeCheckpoint { return nodeCheckpoint{Phase: "run"} },
	Phases: map[string]durable.PhaseHandler[nodeInput, nodeCheckpoint, string, any]{
		"run": func(ctx context.Context, task nodeRecord, runtime nodeRuntime) error {
			name := task.Input.Name
			logLine("run:" + name)
			if behavior := behaviorOf(name).run; behavior != nil {
				return behavior(ctx, runtime)
			}
			return defaultRun(ctx, runtime, name)
		},
		"resume": func(ctx context.Context, task nodeRecord, runtime nodeRuntime) error {
			name := task.Input.Name
			logLine("resume:" + name)
			if behavior := behaviorOf(name).resume; behavior != nil {
				return behavior(ctx, runtime, task.State.Checkpoint.Round)
			}
			return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) { return endNode("completed", name), nil })
		},
	},
	Abort: func(ctx context.Context, task nodeRecord, runtime nodeRuntime) error {
		name := task.Input.Name
		logLine("abort:" + name)
		if behavior := behaviorOf(name).abort; behavior != nil {
			return behavior(ctx, runtime)
		}
		return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) {
			return &nodeNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted, Result: &name}}, nil
		})
	},
})

// unregisteredTask is never registered: aborting it can only orphan it.
var unregisteredTask = durable.DefineTask(durable.TaskDefinition[nodeInput, nodeCheckpoint, string, any]{
	Name:    "test.unregistered",
	Version: 1,
	Initial: func(nodeInput) nodeCheckpoint { return nodeCheckpoint{Phase: "run"} },
	Phases: map[string]durable.PhaseHandler[nodeInput, nodeCheckpoint, string, any]{
		"run": func(context.Context, nodeRecord, nodeRuntime) error { return nil },
	},
	Abort: func(context.Context, nodeRecord, nodeRuntime) error { return nil },
})

type taskNotesState struct {
	Text string `json:"text"`
}

var taskNotesDoc = durable.DefineDoc(durable.DocDefinition[taskNotesState]{
	CommonDocDefinition: durable.CommonDocDefinition[taskNotesState]{Kind: "test.task-notes", Version: 1, Initial: func() taskNotesState { return taskNotesState{} }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeTask},
})

var conversationOwnedTask = durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}}

func ownedBy(owner durable.TaskId) durable.TaskOptions {
	return durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: owner}}
}

// spawn creates a child task named name, owned by owner.
func spawn(tx durable.Tx, owner durable.TaskId, name string) (durable.TaskId, error) {
	return durable.CreateTask(tx, nodeTask, nodeInput{Name: name}, ownedBy(owner))
}

// waitOn waits on on, resuming in round.
func waitOn(on []durable.TaskId, policy durable.JoinPolicy, round ...int) *nodeNext {
	resumeRound := 1
	if len(round) > 0 {
		resumeRound = round[0]
	}
	return &nodeNext{Status: durable.TaskWaiting, Checkpoint: &nodeCheckpoint{Phase: "resume", Round: resumeRound}, On: slices.Clone(on), Policy: policy}
}

func resumeNext(round int) *nodeNext {
	return &nodeNext{Status: durable.TaskRunning, Checkpoint: &nodeCheckpoint{Phase: "resume", Round: round}}
}

func startNode(t *testing.T, conversation Conversation, name string, options ...durable.TaskOptions) durable.TaskId {
	t.Helper()
	option := conversationOwnedTask
	if len(options) > 0 {
		option = options[0]
	}
	return commitValue(t, conversation, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, nodeTask, nodeInput{Name: name}, option)
	})
}

type openedNodes struct {
	harness  Harness
	root     Conversation
	registry Registry
	reports  *reportLog
}

func openNodes(t *testing.T, stores ...durable.Storage) openedNodes {
	t.Helper()
	resetNodes(t)
	var store durable.Storage = storage.NewMemoryStorage()
	if len(stores) > 0 {
		store = stores[0]
	}
	harness, registry, reports := openTasks(t, store, []durable.AnyTask{nodeTask})
	root := must(harness.Root(testContext, nil))
	harness.Resume()
	return openedNodes{harness: harness, root: root, registry: registry, reports: reports}
}

func taskState(t *testing.T, harness Harness, id durable.TaskId) durable.TaskState[durable.JsonValue, durable.JsonValue] {
	t.Helper()
	return must(harness.GetTask(testContext, id)).State
}

func outcomeOf(t *testing.T, harness Harness, id durable.TaskId) string {
	t.Helper()
	return string(must(harness.WaitForTask(testContext, id)).State.Outcome.Status)
}

func expectTaskOutcomeKind(t *testing.T, harness Harness, id durable.TaskId, want string) {
	t.Helper()
	if got := outcomeOf(t, harness, id); got != want {
		t.Fatalf("outcome of %d = %s, want %s", id, got, want)
	}
}

func statusIs(t *testing.T, harness Harness, id durable.TaskId, status durable.TaskStatus) func() bool {
	return func() bool { return taskState(t, harness, id).Status == status }
}

// nodeIds is a concurrency-safe list of task IDs a behavior records.
type nodeIds struct {
	mu  sync.Mutex
	ids []durable.TaskId
}

func (ids *nodeIds) add(id durable.TaskId) {
	ids.mu.Lock()
	ids.ids = append(ids.ids, id)
	ids.mu.Unlock()
}

func (ids *nodeIds) all() []durable.TaskId {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	return slices.Clone(ids.ids)
}

func (ids *nodeIds) len() int { return len(ids.all()) }

func expectCommitError(t *testing.T, conversation Conversation, change func(tx durable.Tx) (any, error), want string) {
	t.Helper()
	_, err := conversation.Commit(testContext, change)
	if want == "" {
		if err == nil {
			t.Fatal("commit succeeded")
		}
		return
	}
	expectError(t, err, want)
}

// ─── Ownership ──────────────────────────────────────────────────────────────

func TestTaskOwnership(t *testing.T) {
	t.Run("creates a child in its owner's conversation with its owner recorded", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		children := &nodeIds{}
		scriptNode("parent", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				child, err := spawn(tx, runtime.TaskId(), "child")
				if err != nil {
					return nil, err
				}
				children.add(child)
				return waitOn([]durable.TaskId{child}, durable.JoinAllSettled), nil
			})
		}})
		parent := startNode(t, root, "parent")
		waitFor(t, func() bool { return children.len() > 0 })
		expectMatch(t, must(harness.GetTask(testContext, children.all()[0])), jsonText(t, map[string]any{"owner": parent, "conversationId": root.Id(), "background": false}))
		if owner := must(harness.GetTask(testContext, parent)).Owner; owner != nil {
			t.Fatalf("parent owner = %d, want undefined", *owner)
		}
		openGate("child")
		expectTaskOutcomeKind(t, harness, parent, "completed")
		closeHarness(t, harness)
	})

	t.Run("rejects no ownership, a missing owner, a child in another conversation, and a background child", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		parent := startNode(t, root, "parent")
		other := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless}))
		create := func(options durable.TaskOptions) func(tx durable.Tx) (any, error) {
			return func(tx durable.Tx) (any, error) {
				return durable.CreateTask(tx, nodeTask, nodeInput{Name: "x"}, options)
			}
		}
		expectCommitError(t, root, create(durable.TaskOptions{}), "")
		expectCommitError(t, root, create(ownedBy(999_999)), "does not exist")
		otherId := other.Id()
		inOther := ownedBy(parent)
		inOther.ConversationId = &otherId
		expectCommitError(t, root, create(inOther), "owner's conversation")
		background := ownedBy(parent)
		background.Background = true
		expectCommitError(t, root, create(background), "cannot be background")
		// The owner's conversation is the default, even from a commit bound to another conversation.
		child := commitValue(t, other, func(tx durable.Tx) (durable.TaskId, error) { return spawn(tx, parent, "child") })
		if conversation := must(harness.GetTask(testContext, child)).ConversationId; conversation != root.Id() {
			t.Fatalf("child conversation = %d, want %d", conversation, root.Id())
		}
		openGate("parent")
		openGate("child")
		must(harness.WaitForTask(testContext, parent))
		closeHarness(t, harness)
	})

	t.Run("rejects new owned work below an owner that is completing, terminal, or abort-marked", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		scriptNode("parent", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				if _, err := spawn(tx, runtime.TaskId(), "child"); err != nil {
					return nil, err
				}
				return resumeNext(1), nil
			})
		}})
		parent := startNode(t, root, "parent")
		waitFor(t, statusIs(t, harness, parent, durable.TaskCompleting))
		createChild := func(tx durable.Tx) (any, error) { return spawn(tx, parent, "late") }
		createConversation := func(tx durable.Tx) (any, error) {
			return tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: parent}})
		}
		expectCommitError(t, root, createChild, "is completing")
		expectCommitError(t, root, createConversation, "is completing")
		openGate("child")
		must(harness.WaitForTask(testContext, parent))
		expectCommitError(t, root, createChild, "is terminal")
		expectCommitError(t, root, createConversation, "is terminal")

		scriptNode("slow", nodeBehavior{abort: func(ctx context.Context, runtime nodeRuntime) error {
			if _, err := nodeGate("abort.slow").wait(ctx); err != nil {
				return err
			}
			return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) {
				return &nodeNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted}}, nil
			})
		}})
		slow := startNode(t, root, "slow")
		waitFor(t, func() bool { return logged("run:slow") })
		must(harness.AbortTask(testContext, slow))
		expectCommitError(t, root, func(tx durable.Tx) (any, error) { return spawn(tx, slow, "late") }, "is abort-marked")
		openGate("abort.slow")
		expectTaskOutcomeKind(t, harness, slow, "aborted")
		closeHarness(t, harness)
	})

	t.Run("cannot create a child in its finishing commit, but work it starts in an owned conversation holds it", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		scriptNode("eager", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				if _, err := spawn(tx, runtime.TaskId(), "never"); err != nil {
					return nil, err
				}
				return endNode("completed", "eager"), nil
			})
		}})
		eager := startNode(t, root, "eager")
		outcome := must(harness.WaitForTask(testContext, eager)).State.Outcome
		if outcome.Status != durable.OutcomeFaulted || outcome.Error == nil || !strings.Contains(outcome.Error.Message, "is completing") {
			t.Fatalf("outcome = %s", jsonText(t, outcome))
		}
		// The rejected commit wrote nothing.
		kind := "test.node"
		page := must(durable.Commit(testContext, harness, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
			return tx.ScanTasks(durable.TaskQuery{Kind: &kind}, 20, nil)
		}))
		names := []string{}
		for _, task := range page.Items {
			names = append(names, plainObject(task.Input)["name"].(string))
		}
		expectEqualJSON(t, names, `["eager"]`)

		var mu sync.Mutex
		var child durable.ConversationId
		scriptNode("host", nodeBehavior{
			run: func(ctx context.Context, runtime nodeRuntime) error {
				return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
					record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: runtime.TaskId()}})
					if err != nil {
						return nil, err
					}
					mu.Lock()
					child = record.Id
					mu.Unlock()
					return resumeNext(1), nil
				})
			},
			resume: func(ctx context.Context, runtime nodeRuntime, _ int) error {
				return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
					mu.Lock()
					conversation := child
					mu.Unlock()
					options := conversationOwnedTask
					options.ConversationId = &conversation
					if _, err := durable.CreateTask(tx, nodeTask, nodeInput{Name: "inner"}, options); err != nil {
						return nil, err
					}
					return endNode("completed", "host"), nil
				})
			},
		})
		host := startNode(t, root, "host")
		waitFor(t, statusIs(t, harness, host, durable.TaskCompleting))
		waited := make(chan struct{})
		go func() {
			defer close(waited)
			_, _ = harness.WaitForTask(testContext, host)
		}()
		if settled(waited) {
			t.Fatal("the host settled while its conversation's work runs")
		}
		openGate("inner")
		expectTaskOutcomeKind(t, harness, host, "completed")
		closeHarness(t, harness)
	})
}

// ─── Waiting ────────────────────────────────────────────────────────────────

// joinParent records the children a parent spawned and the outcomes it read.
type joinParent struct {
	ids      nodeIds
	mu       sync.Mutex
	outcomes []string
}

func (found *joinParent) setOutcomes(outcomes []durable.TaskOutcome[durable.JsonValue]) {
	statuses := []string{}
	for _, outcome := range outcomes {
		statuses = append(statuses, string(outcome.Status))
	}
	found.mu.Lock()
	found.outcomes = statuses
	found.mu.Unlock()
}

func (found *joinParent) read() []string {
	found.mu.Lock()
	defer found.mu.Unlock()
	return slices.Clone(found.outcomes)
}

// resumeWithOutcomes reads the outcomes of ids and completes parent.
func resumeWithOutcomes(found *joinParent, parent string) func(ctx context.Context, runtime nodeRuntime, round int) error {
	return func(ctx context.Context, runtime nodeRuntime, _ int) error {
		outcomes, err := runtime.Outcomes(ctx, found.ids.all())
		if err != nil {
			return err
		}
		found.setOutcomes(outcomes)
		return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) { return endNode("completed", parent), nil })
	}
}

// joinParentOf scripts parent to spawn children in one commit and wait on them with policy; it records their outcomes.
func joinParentOf(parent string, children []string, policy durable.JoinPolicy) *joinParent {
	found := &joinParent{}
	scriptNode(parent, nodeBehavior{
		run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				for _, name := range children {
					id, err := spawn(tx, runtime.TaskId(), name)
					if err != nil {
						return nil, err
					}
					found.ids.add(id)
				}
				return waitOn(found.ids.all(), policy), nil
			})
		},
		resume: resumeWithOutcomes(found, parent),
	})
	return found
}

func abortRequested(t *testing.T, harness Harness, id durable.TaskId) bool {
	t.Helper()
	return must(harness.GetTask(testContext, id)).AbortRequested
}

func TestWaiting(t *testing.T) {
	t.Run("resumes once every awaited task is terminal and reads their outcomes in order (allSettled)", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		found := &joinParent{}
		scriptNode("parent", nodeBehavior{
			run: func(ctx context.Context, runtime nodeRuntime) error {
				return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
					for _, name := range []string{"ok", "fails", "throws", "aborted"} {
						id, err := spawn(tx, runtime.TaskId(), name)
						if err != nil {
							return nil, err
						}
						found.ids.add(id)
					}
					orphan, err := durable.CreateTask(tx, unregisteredTask, nodeInput{Name: "orphan"}, ownedBy(runtime.TaskId()))
					if err != nil {
						return nil, err
					}
					found.ids.add(orphan)
					return waitOn(found.ids.all(), durable.JoinAllSettled), nil
				})
			},
			resume: resumeWithOutcomes(found, "parent"),
		})
		parent := startNode(t, root, "parent")
		waitFor(t, func() bool { return found.ids.len() == 5 })
		waitFor(t, statusIs(t, harness, parent, durable.TaskWaiting))
		ids := found.ids.all()
		openGate("ok")
		openGate("fails", "failed")
		openGate("throws", "throw")
		must(harness.AbortTask(testContext, ids[3]))
		if result := must(harness.AbortTask(testContext, ids[4])); result != "marked" {
			t.Fatalf("abort = %s, want marked", result)
		}
		expectTaskOutcomeKind(t, harness, parent, "completed")
		expectEqualJSON(t, found.read(), `["completed","failed","faulted","aborted","orphaned"]`)
		// allSettled never marks siblings.
		expectEqualJSON(t, logWithPrefix("abort:"), `["abort:aborted"]`)
		closeHarness(t, harness)
	})

	for _, ending := range []string{"failed", "throw"} {
		name := "failed"
		if ending == "throw" {
			name = "faulted"
		}
		t.Run("fails fast when a child ends "+name+": its live siblings are aborted, the parent is not", func(t *testing.T) {
			opened := openNodes(t)
			harness, root := opened.harness, opened.root
			found := joinParentOf("checkout", []string{"p1", "p2", "p3", "p4"}, durable.JoinFailFast)
			parent := startNode(t, root, "checkout")
			waitFor(t, statusIs(t, harness, parent, durable.TaskWaiting))
			openGate("p2", ending)
			expectTaskOutcomeKind(t, harness, parent, "completed")
			expectEqualJSON(t, found.read(), jsonText(t, []string{"aborted", name, "aborted", "aborted"}))
			if abortRequested(t, harness, parent) {
				t.Fatal("the parent was marked")
			}
			aborts := logWithPrefix("abort:")
			slices.Sort(aborts)
			expectEqualJSON(t, aborts, `["abort:p1","abort:p3","abort:p4"]`)
			closeHarness(t, harness)
		})
	}

	t.Run("fails fast on a held failure before the failing child drains", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		found := &nodeIds{}
		scriptNode("p1", nodeBehavior{
			run: func(ctx context.Context, runtime nodeRuntime) error {
				return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
					if _, err := spawn(tx, runtime.TaskId(), "grandchild"); err != nil {
						return nil, err
					}
					return resumeNext(1), nil
				})
			},
			resume: func(ctx context.Context, runtime nodeRuntime, _ int) error {
				return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) { return endNode("failed", "p1"), nil })
			},
		})
		scriptNode("grandchild", nodeBehavior{abort: func(ctx context.Context, runtime nodeRuntime) error {
			if _, err := nodeGate("abort.grandchild").wait(ctx); err != nil {
				return err
			}
			return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) {
				return &nodeNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted}}, nil
			})
		}})
		outside := &nodeIds{}
		scriptNode("parent", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				for _, name := range []string{"p1", "p2"} {
					id, err := spawn(tx, runtime.TaskId(), name)
					if err != nil {
						return nil, err
					}
					found.add(id)
				}
				id, err := spawn(tx, runtime.TaskId(), "outside")
				if err != nil {
					return nil, err
				}
				outside.add(id)
				return waitOn(found.all(), durable.JoinFailFast), nil
			})
		}})
		parent := startNode(t, root, "parent")
		waitFor(t, func() bool { return found.len() == 2 })
		ids := found.all()
		// p1 holds failed while its grandchild's abort handler runs; p2 is already aborted.
		expectTaskOutcomeKind(t, harness, ids[1], "aborted")
		expectMatch(t, taskState(t, harness, ids[0]), `{"status":"completing","outcome":{"status":"failed"}}`)
		if status := taskState(t, harness, parent).Status; status != durable.TaskWaiting {
			t.Fatalf("parent = %s, want waiting", status)
		}
		openGate("abort.grandchild")
		waitFor(t, func() bool { return logged("resume:parent") })
		// Only the other tasks in on are marked: not the failed one, not the parent, not a child outside on.
		expectTaskOutcomeKind(t, harness, ids[0], "failed")
		outsideId := outside.all()[0]
		marked := []bool{abortRequested(t, harness, ids[0]), abortRequested(t, harness, ids[1]), abortRequested(t, harness, outsideId)}
		expectEqualJSON(t, marked, `[false,true,false]`)
		if abortRequested(t, harness, parent) || taskState(t, harness, outsideId).Status != durable.TaskRunning {
			t.Fatal("the parent was marked or the outside child stopped")
		}
		// Finished, the parent holds for the child outside on.
		waitFor(t, statusIs(t, harness, parent, durable.TaskCompleting))
		openGate("outside")
		expectTaskOutcomeKind(t, harness, parent, "completed")
		closeHarness(t, harness)
	})

	t.Run("waits with allSettled on tasks it does not own, including already terminal ones", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		done := startNode(t, root, "done")
		openGate("done")
		must(harness.WaitForTask(testContext, done))
		live := startNode(t, root, "live")
		found := &joinParent{}
		found.ids.add(done)
		found.ids.add(live)
		scriptNode("parent", nodeBehavior{
			run: func(ctx context.Context, runtime nodeRuntime) error {
				return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) {
					return waitOn([]durable.TaskId{done, live}, durable.JoinAllSettled), nil
				})
			},
			resume: resumeWithOutcomes(found, "parent"),
		})
		parent := startNode(t, root, "parent")
		waitFor(t, statusIs(t, harness, parent, durable.TaskWaiting))
		// A task it does not own is not its work: the parent may finish while it lives, but here it waits for it.
		openGate("live", "failed")
		expectTaskOutcomeKind(t, harness, parent, "completed")
		expectEqualJSON(t, found.read(), `["completed","failed"]`)
		closeHarness(t, harness)
	})

	t.Run("rejects waits on itself, its owner, a missing task, and failFast on a task it does not own", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		other := startNode(t, root, "other")
		for _, variant := range []struct {
			name    string
			on      func(runtime nodeRuntime) []durable.TaskId
			policy  durable.JoinPolicy
			message string
		}{
			{"self", func(runtime nodeRuntime) []durable.TaskId { return []durable.TaskId{runtime.TaskId()} }, durable.JoinAllSettled, "cannot wait on itself or its owner"},
			{"missing", func(nodeRuntime) []durable.TaskId { return []durable.TaskId{999_999} }, durable.JoinAllSettled, "does not exist"},
			{"foreign", func(nodeRuntime) []durable.TaskId { return []durable.TaskId{other} }, durable.JoinFailFast, "only on tasks it owns"},
		} {
			scriptNode(variant.name, nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
				return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) { return waitOn(variant.on(runtime), variant.policy), nil })
			}})
			id := startNode(t, root, variant.name)
			outcome := must(harness.WaitForTask(testContext, id)).State.Outcome
			if outcome.Status != durable.OutcomeFaulted || !strings.Contains(outcome.Error.Message, variant.message) {
				t.Fatalf("%s: outcome = %s", variant.name, jsonText(t, outcome))
			}
		}
		// A child waiting on its owner could never resume.
		children := &nodeIds{}
		parents := &nodeIds{}
		scriptNode("child", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) { return waitOn(parents.all(), durable.JoinAllSettled), nil })
		}})
		scriptNode("parent", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			parents.add(runtime.TaskId())
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				child, err := spawn(tx, runtime.TaskId(), "child")
				if err != nil {
					return nil, err
				}
				children.add(child)
				return waitOn([]durable.TaskId{child}, durable.JoinAllSettled), nil
			})
		}})
		parent := startNode(t, root, "parent")
		expectTaskOutcomeKind(t, harness, parent, "completed")
		outcome := must(harness.WaitForTask(testContext, children.all()[0])).State.Outcome
		if outcome.Status != durable.OutcomeFaulted || !strings.Contains(outcome.Error.Message, "cannot wait on itself or its owner") {
			t.Fatalf("child outcome = %s", jsonText(t, outcome))
		}
		openGate("other")
		closeHarness(t, harness)
	})

	t.Run("resumes at the next pass when it waits on nothing", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		scriptNode("parent", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) { return waitOn(nil, durable.JoinFailFast), nil })
		}})
		expectTaskOutcomeKind(t, harness, startNode(t, root, "parent"), "completed")
		expectEqualJSON(t, nodeLog(), `["run:parent","resume:parent"]`)
		closeHarness(t, harness)
	})

	t.Run("keeps running phases after spawning and waits on subsets in sequence", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		ids := &nodeIds{}
		scriptNode("parent", nodeBehavior{
			run: func(ctx context.Context, runtime nodeRuntime) error {
				return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
					for _, name := range []string{"a", "b", "c"} {
						id, err := spawn(tx, runtime.TaskId(), name)
						if err != nil {
							return nil, err
						}
						ids.add(id)
					}
					return resumeNext(0), nil
				})
			},
			resume: func(ctx context.Context, runtime nodeRuntime, round int) error {
				logLine("round:" + itoa(round))
				all := ids.all()
				return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) {
					switch round {
					case 0:
						return waitOn(all[:1], durable.JoinAllSettled, 1), nil
					case 1:
						return waitOn(all[1:], durable.JoinFailFast, 2), nil
					}
					return endNode("completed", "parent"), nil
				})
			},
		})
		parent := startNode(t, root, "parent")
		waitFor(t, func() bool { return logged("round:0") })
		waitFor(t, statusIs(t, harness, parent, durable.TaskWaiting))
		all := ids.all()
		openGate("b")
		must(harness.WaitForTask(testContext, all[1]))
		if status := taskState(t, harness, parent).Status; status != durable.TaskWaiting {
			t.Fatalf("parent = %s, want waiting", status)
		}
		openGate("a")
		waitFor(t, func() bool { return logged("round:1") })
		openGate("c")
		expectTaskOutcomeKind(t, harness, parent, "completed")
		expectEqualJSON(t, logWithPrefix("round:"), `["round:0","round:1","round:2"]`)
		closeHarness(t, harness)
	})
}

// ─── Completing ─────────────────────────────────────────────────────────────

// spawnAndResume scripts name to spawn child and continue in a resume phase; ids records the child.
func spawnAndResume(name, child string, ids *nodeIds, resume func(ctx context.Context, runtime nodeRuntime, round int) error) {
	scriptNode(name, nodeBehavior{
		run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				id, err := spawn(tx, runtime.TaskId(), child)
				if err != nil {
					return nil, err
				}
				ids.add(id)
				return resumeNext(1), nil
			})
		},
		resume: resume,
	})
}

func TestCompleting(t *testing.T) {
	t.Run("holds a finished task until its owned work drains; waiters and task documents wait for the final commit", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		children := &nodeIds{}
		scriptNode("parent", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				notes, err := durable.TxDoc[taskNotesState](tx, taskNotesDoc, runtime.TaskId())
				if err != nil {
					return nil, err
				}
				if err := notes.Set("text", "notes"); err != nil {
					return nil, err
				}
				child, err := spawn(tx, runtime.TaskId(), "child")
				if err != nil {
					return nil, err
				}
				children.add(child)
				return resumeNext(1), nil
			})
		}})
		parent := startNode(t, root, "parent")
		waitFor(t, statusIs(t, harness, parent, durable.TaskCompleting))
		expectEqualJSON(t, taskState(t, harness, parent), `{"status":"completing","outcome":{"status":"completed","result":"parent"}}`)
		waiter := make(chan durable.SettledTask[durable.JsonValue], 1)
		go func() { waiter <- must(harness.WaitForTask(testContext, parent)) }()
		flush()
		if len(waiter) != 0 {
			t.Fatal("the waiter settled during the hold")
		}
		expectEqualJSON(t, must(durable.Snapshot(testContext, harness, taskNotesDoc, parent)), `{"text":"notes"}`)
		inspection := must(harness.Inspect(testContext))
		index := slices.IndexFunc(inspection.Tasks, func(task TaskInspection) bool { return task.Record.Id == parent })
		if state := inspection.Tasks[index].State; state.Kind != TaskInspectionCompleting {
			t.Fatalf("inspection = %+v", state)
		}
		idle := make(chan struct{})
		go func() {
			defer close(idle)
			_ = root.WaitForIdle(testContext)
		}()
		if settled(idle) {
			t.Fatal("idle during the hold")
		}
		openGate("child")
		expectEqualJSON(t, (<-waiter).State.Outcome, `{"status":"completed","result":"parent"}`)
		if notes := must(durable.Snapshot(testContext, harness, taskNotesDoc, parent)); notes != nil {
			t.Fatalf("notes = %v, want undefined", notes)
		}
		<-idle
		if children.len() == 0 {
			t.Fatal("no child")
		}
		closeHarness(t, harness)
	})

	t.Run("keeps holding for ordinary work created during the hold", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		var mu sync.Mutex
		var conversation durable.ConversationId
		scriptNode("parent", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: runtime.TaskId()}})
				if err != nil {
					return nil, err
				}
				mu.Lock()
				conversation = record.Id
				mu.Unlock()
				options := conversationOwnedTask
				options.ConversationId = &record.Id
				if _, err := durable.CreateTask(tx, nodeTask, nodeInput{Name: "first"}, options); err != nil {
					return nil, err
				}
				return resumeNext(1), nil
			})
		}})
		parent := startNode(t, root, "parent")
		waitFor(t, statusIs(t, harness, parent, durable.TaskCompleting))
		mu.Lock()
		id := conversation
		mu.Unlock()
		child := must(harness.Conversation(testContext, id))
		startNode(t, child, "second")
		openGate("first")
		waitFor(t, func() bool { return logged("run:second") })
		if status := taskState(t, harness, parent).Status; status != durable.TaskCompleting {
			t.Fatalf("parent = %s, want completing", status)
		}
		openGate("second")
		expectTaskOutcomeKind(t, harness, parent, "completed")
		closeHarness(t, harness)
	})

	for _, variant := range []struct{ label, ending string }{{"a held failure", "failed"}, {"a held scheduler fault", "throw"}} {
		t.Run("aborts the work below "+variant.label+", then finishes with the held outcome", func(t *testing.T) {
			opened := openNodes(t)
			harness, root := opened.harness, opened.root
			children := &nodeIds{}
			spawnAndResume("parent", "child", children, func(ctx context.Context, runtime nodeRuntime, _ int) error {
				if variant.ending == "throw" {
					return errors.New("parent threw")
				}
				return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) { return endNode("failed", "parent"), nil })
			})
			parent := startNode(t, root, "parent")
			waitFor(t, func() bool { return children.len() > 0 })
			expectTaskOutcomeKind(t, harness, children.all()[0], "aborted")
			want := "failed"
			if variant.ending == "throw" {
				want = "faulted"
			}
			expectTaskOutcomeKind(t, harness, parent, want)
			closeHarness(t, harness)
		})
	}

	t.Run("only marks a completing task when aborted: the work below is aborted, the held outcome stays", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		children := &nodeIds{}
		spawnAndResume("parent", "child", children, nil)
		parent := startNode(t, root, "parent")
		waitFor(t, statusIs(t, harness, parent, durable.TaskCompleting))
		if result := must(harness.AbortTask(testContext, parent)); result != "marked" {
			t.Fatalf("abort = %s, want marked", result)
		}
		expectTaskOutcomeKind(t, harness, children.all()[0], "aborted")
		settledParent := must(harness.WaitForTask(testContext, parent))
		expectEqualJSON(t, settledParent.State.Outcome, `{"status":"completed","result":"parent"}`)
		if !settledParent.AbortRequested || logged("abort:parent") {
			t.Fatal("the completing parent was not only marked")
		}
		if result := must(harness.AbortTask(testContext, parent)); result != "terminal" {
			t.Fatalf("abort = %s, want terminal", result)
		}
		closeHarness(t, harness)
	})
}

// ─── Abort order ────────────────────────────────────────────────────────────

func abortedNext() *nodeNext {
	return &nodeNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted}}
}

// gatedAbort is an abort handler that waits for gate name, then ends aborted.
func gatedAbort(name string) func(ctx context.Context, runtime nodeRuntime) error {
	return func(ctx context.Context, runtime nodeRuntime) error {
		if _, err := nodeGate(name).wait(ctx); err != nil {
			return err
		}
		return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) { return abortedNext(), nil })
	}
}

// waitUntilAborted blocks until the invocation is signalled.
func waitUntilAborted(_ context.Context, runtime nodeRuntime, _ int) error {
	return abortedBy(runtime.Signal())
}

// chain scripts name to spawn child and wait on it; its abort handler records the child's status first.
func chain(harness func() Harness, name, child string) *nodeIds {
	found := &nodeIds{}
	scriptNode(name, nodeBehavior{
		run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				id, err := spawn(tx, runtime.TaskId(), child)
				if err != nil {
					return nil, err
				}
				found.add(id)
				return waitOn([]durable.TaskId{id}, durable.JoinAllSettled), nil
			})
		},
		abort: func(ctx context.Context, runtime nodeRuntime) error {
			record, err := harness().GetTask(testContext, found.all()[0])
			if err != nil {
				return err
			}
			logLine(name + " saw " + string(record.State.Status))
			return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) { return abortedNext(), nil })
		},
	})
	return found
}

func TestAbortOrder(t *testing.T) {
	t.Run("runs abort handlers bottom-up across three levels, each after the level below is terminal", func(t *testing.T) {
		var opened openedNodes
		b := chain(func() Harness { return opened.harness }, "a", "b")
		c := chain(func() Harness { return opened.harness }, "b", "c")
		opened = openNodes(t)
		harness, root := opened.harness, opened.root
		a := startNode(t, root, "a")
		waitFor(t, func() bool { return logged("run:c") })
		waitFor(t, statusIs(t, harness, c.all()[0], durable.TaskRunning))
		if result := must(harness.AbortTask(testContext, a)); result != "marked" {
			t.Fatalf("abort = %s, want marked", result)
		}
		expectTaskOutcomeKind(t, harness, a, "aborted")
		lines := []string{}
		for _, line := range nodeLog() {
			if strings.HasPrefix(line, "abort:") || strings.Contains(line, " saw ") {
				lines = append(lines, line)
			}
		}
		expectEqualJSON(t, lines, `["abort:c","abort:b","b saw terminal","abort:a","a saw terminal"]`)
		if b.len() == 0 {
			t.Fatal("b was never spawned")
		}
		closeHarness(t, harness)
	})

	t.Run("does not wait for a task it waits on but does not own", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		other := startNode(t, root, "other")
		scriptNode("parent", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) {
				return waitOn([]durable.TaskId{other}, durable.JoinAllSettled), nil
			})
		}})
		parent := startNode(t, root, "parent")
		waitFor(t, statusIs(t, harness, parent, durable.TaskWaiting))
		must(harness.AbortTask(testContext, parent))
		expectTaskOutcomeKind(t, harness, parent, "aborted")
		if status := taskState(t, harness, other).Status; status != durable.TaskRunning {
			t.Fatalf("other = %s, want running", status)
		}
		openGate("other")
		closeHarness(t, harness)
	})

	t.Run("reports an abort-marked task as waiting for its live owned work", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		children := &nodeIds{}
		scriptNode("child", nodeBehavior{abort: gatedAbort("abort.child")})
		scriptNode("parent", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				child, err := spawn(tx, runtime.TaskId(), "child")
				if err != nil {
					return nil, err
				}
				children.add(child)
				return waitOn([]durable.TaskId{child}, durable.JoinAllSettled), nil
			})
		}})
		parent := startNode(t, root, "parent")
		waitFor(t, statusIs(t, harness, parent, durable.TaskWaiting))
		must(harness.AbortTask(testContext, parent))
		waitFor(t, func() bool { return logged("abort:child") })
		inspection := must(harness.Inspect(testContext))
		index := slices.IndexFunc(inspection.Tasks, func(task TaskInspection) bool { return task.Record.Id == parent })
		if state := inspection.Tasks[index].State; state.Kind != TaskInspectionWaiting || !slices.Equal(state.On, children.all()) {
			t.Fatalf("inspection = %+v", state)
		}
		openGate("abort.child")
		expectTaskOutcomeKind(t, harness, parent, "aborted")
		closeHarness(t, harness)
	})

	t.Run("faults an abort handler that tries to wait", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		scriptNode("parent", nodeBehavior{abort: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) { return waitOn(nil, durable.JoinAllSettled), nil })
		}})
		parent := startNode(t, root, "parent")
		waitFor(t, func() bool { return logged("run:parent") })
		must(harness.AbortTask(testContext, parent))
		outcome := must(harness.WaitForTask(testContext, parent)).State.Outcome
		if outcome.Status != durable.OutcomeFaulted || !strings.Contains(outcome.Error.Message, "cannot wait") {
			t.Fatalf("outcome = %s", jsonText(t, outcome))
		}
		closeHarness(t, harness)
	})

	t.Run("orphans a blocked task only after its owned work drained", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		ids := commitValue(t, root, func(tx durable.Tx) ([]durable.TaskId, error) {
			owner, err := durable.CreateTask(tx, unregisteredTask, nodeInput{Name: "owner"}, conversationOwnedTask)
			if err != nil {
				return nil, err
			}
			child, err := spawn(tx, owner, "child")
			return []durable.TaskId{owner, child}, err
		})
		owner, child := ids[0], ids[1]
		waitFor(t, func() bool { return logged("run:child") })
		if result := must(harness.AbortTask(testContext, owner)); result != "marked" {
			t.Fatalf("abort = %s, want marked", result)
		}
		expectTaskOutcomeKind(t, harness, child, "aborted")
		expectEqualJSON(t, must(harness.WaitForTask(testContext, owner)).State.Outcome, `{"status":"orphaned","reason":"missing_task"}`)
		closeHarness(t, harness)
	})

	t.Run("cascades through task and conversation edges, bottom-up", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		xs := &nodeIds{}
		scriptNode("a", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				b, err := spawn(tx, runtime.TaskId(), "b")
				if err != nil {
					return nil, err
				}
				return waitOn([]durable.TaskId{b}, durable.JoinAllSettled), nil
			})
		}})
		scriptNode("b", nodeBehavior{
			run: func(ctx context.Context, runtime nodeRuntime) error {
				return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
					conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: runtime.TaskId()}})
					if err != nil {
						return nil, err
					}
					options := conversationOwnedTask
					options.ConversationId = &conversation.Id
					x, err := durable.CreateTask(tx, nodeTask, nodeInput{Name: "x"}, options)
					if err != nil {
						return nil, err
					}
					xs.add(x)
					return resumeNext(1), nil
				})
			},
			resume: waitUntilAborted,
		})
		a := startNode(t, root, "a")
		waitFor(t, func() bool { return logged("run:x") })
		must(harness.AbortTask(testContext, a))
		expectTaskOutcomeKind(t, harness, a, "aborted")
		expectTaskOutcomeKind(t, harness, xs.all()[0], "aborted")
		expectEqualJSON(t, logWithPrefix("abort:"), `["abort:x","abort:b","abort:a"]`)
		closeHarness(t, harness)
	})
}

// ─── Background and terminal owners ─────────────────────────────────────────

func TestBoundaries(t *testing.T) {
	t.Run("keeps background work through Conversation.abort(); { background: true } aborts it and waits", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		foreground := startNode(t, root, "foreground")
		below := &nodeIds{}
		scriptNode("background", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				child, err := spawn(tx, runtime.TaskId(), "child")
				if err != nil {
					return nil, err
				}
				below.add(child)
				conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: runtime.TaskId()}})
				if err != nil {
					return nil, err
				}
				options := conversationOwnedTask
				options.ConversationId = &conversation.Id
				id, err := durable.CreateTask(tx, nodeTask, nodeInput{Name: "below"}, options)
				if err != nil {
					return nil, err
				}
				below.add(id)
				return waitOn([]durable.TaskId{child}, durable.JoinAllSettled), nil
			})
		}})
		backgroundOptions := conversationOwnedTask
		backgroundOptions.Background = true
		background := startNode(t, root, "background", backgroundOptions)
		waitFor(t, func() bool { return logged("run:below") && logged("run:child") })
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		expectTaskOutcomeKind(t, harness, foreground, "aborted")
		ids := append([]durable.TaskId{background}, below.all()...)
		for _, id := range ids {
			if taskState(t, harness, id).Status == durable.TaskTerminal {
				t.Fatalf("background work %d ended", id)
			}
		}
		if err := root.Abort(testContext, &durable.ConversationAbortOptions{Background: true}); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if status := taskState(t, harness, id).Status; status != durable.TaskTerminal {
				t.Fatalf("task %d = %s, want terminal", id, status)
			}
		}
		closeHarness(t, harness)
	})

	t.Run("never cascades from a terminal owner: an aborted subagent's conversation runs new work normally", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		var mu sync.Mutex
		var conversation durable.ConversationId
		scriptNode("agent", nodeBehavior{
			run: func(ctx context.Context, runtime nodeRuntime) error {
				return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
					record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: runtime.TaskId()}})
					if err != nil {
						return nil, err
					}
					mu.Lock()
					conversation = record.Id
					mu.Unlock()
					return resumeNext(1), nil
				})
			},
			resume: waitUntilAborted,
		})
		agent := startNode(t, root, "agent")
		waitFor(t, func() bool { return logged("resume:agent") })
		must(harness.AbortTask(testContext, agent))
		expectTaskOutcomeKind(t, harness, agent, "aborted")
		mu.Lock()
		id := conversation
		mu.Unlock()
		child := must(harness.Conversation(testContext, id))
		question := startNode(t, child, "question")
		openGate("question")
		expectTaskOutcomeKind(t, harness, question, "completed")
		closeHarness(t, harness)
	})
}

// ─── Recovery ───────────────────────────────────────────────────────────────

type seeded struct {
	parent   durable.TaskId
	children []durable.TaskId
}

type anyRecord = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]

// seed writes records a crash could leave, without a Harness: parent with children named children, then edit replaces
// records through the internal task write.
func seed(t *testing.T, path string, children []string, edit func(set func(id durable.TaskId, patch func(record *anyRecord)), seeded seeded)) seeded {
	t.Helper()
	line := session.CreateSession(openSqlite(t, path))
	result := must(commitOnLine(testContext, line, session.TransactionScope{}, func(tx *session.Transaction) (seeded, error) {
		root, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		if err != nil {
			return seeded{}, err
		}
		options := conversationOwnedTask
		options.ConversationId = &root.Id
		parent, err := durable.CreateTask(tx, nodeTask, nodeInput{Name: "parent"}, options)
		if err != nil {
			return seeded{}, err
		}
		ids := []durable.TaskId{}
		for _, name := range children {
			id, err := spawn(tx, parent, name)
			if err != nil {
				return seeded{}, err
			}
			ids = append(ids, id)
		}
		return seeded{parent: parent, children: ids}, nil
	}))
	edit(func(id durable.TaskId, patch func(record *anyRecord)) {
		must(commitOnLine(testContext, line, session.TransactionScope{}, func(tx *session.Transaction) (struct{}, error) {
			record, err := tx.Task(id)
			if err != nil {
				return struct{}{}, err
			}
			updated := *record
			patch(&updated)
			return struct{}{}, tx.SetTask(updated)
		}))
	}, result)
	if err := line.Close(testContext); err != nil {
		t.Fatal(err)
	}
	return result
}

func stateFrom(t *testing.T, value string) durable.TaskState[durable.JsonValue, durable.JsonValue] {
	t.Helper()
	return must(durable.FromJsonValue[durable.TaskState[durable.JsonValue, durable.JsonValue]](must(jsonValue(value))))
}

const seedCompleted = `{"status":"terminal","outcome":{"status":"completed","result":"done"}}`
const seedFailed = `{"status":"terminal","outcome":{"status":"failed","error":{"message":"declined"}}}`

func seedWaiting(t *testing.T, on []durable.TaskId, policy string) durable.TaskState[durable.JsonValue, durable.JsonValue] {
	t.Helper()
	return stateFrom(t, jsonText(t, map[string]any{"status": "waiting", "checkpoint": map[string]any{"phase": "resume", "round": 1}, "on": on, "policy": policy}))
}

func TestStructuredRecovery(t *testing.T) {
	t.Run("resumes a parent whose awaited children finished before the crash", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		found := seed(t, path, []string{"c1"}, func(set func(durable.TaskId, func(*anyRecord)), found seeded) {
			set(found.children[0], func(record *anyRecord) { record.State = stateFrom(t, seedCompleted) })
			set(found.parent, func(record *anyRecord) { record.State = seedWaiting(t, found.children, "allSettled") })
		})
		opened := openNodes(t, openSqlite(t, path))
		expectTaskOutcomeKind(t, opened.harness, found.parent, "completed")
		expectEqualJSON(t, nodeLog(), `["resume:parent"]`)
		closeHarness(t, opened.harness)
	})

	t.Run("marks failFast siblings a crash left unmarked", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		found := seed(t, path, []string{"c1", "c2"}, func(set func(durable.TaskId, func(*anyRecord)), found seeded) {
			set(found.children[0], func(record *anyRecord) { record.State = stateFrom(t, seedFailed) })
			set(found.parent, func(record *anyRecord) { record.State = seedWaiting(t, found.children, "failFast") })
		})
		opened := openNodes(t, openSqlite(t, path))
		expectTaskOutcomeKind(t, opened.harness, found.children[1], "aborted")
		expectTaskOutcomeKind(t, opened.harness, found.parent, "completed")
		if logged("run:c2") {
			t.Fatal("c2 ran")
		}
		closeHarness(t, opened.harness)
	})

	t.Run("finalizes a held outcome whose work drained before the crash, and keeps one whose work lives", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		const held = `{"status":"completing","outcome":{"status":"completed","result":"parent"}}`
		found := seed(t, path, []string{"c1"}, func(set func(durable.TaskId, func(*anyRecord)), found seeded) {
			set(found.parent, func(record *anyRecord) { record.State = stateFrom(t, held) })
		})
		opened := openNodes(t, openSqlite(t, path))
		waitFor(t, func() bool { return logged("run:c1") })
		expectEqualJSON(t, taskState(t, opened.harness, found.parent), held)
		closeHarness(t, opened.harness)

		opened = openNodes(t, openSqlite(t, path))
		openGate("c1")
		expectTaskOutcomeKind(t, opened.harness, found.children[0], "completed")
		expectTaskOutcomeKind(t, opened.harness, found.parent, "completed")
		closeHarness(t, opened.harness)

		drained := filepath.Join(t.TempDir(), "drained.sqlite")
		second := seed(t, drained, []string{"c1"}, func(set func(durable.TaskId, func(*anyRecord)), found seeded) {
			set(found.children[0], func(record *anyRecord) { record.State = stateFrom(t, seedCompleted) })
			set(found.parent, func(record *anyRecord) { record.State = stateFrom(t, held) })
		})
		opened = openNodes(t, openSqlite(t, drained))
		expectTaskOutcomeKind(t, opened.harness, second.parent, "completed")
		closeHarness(t, opened.harness)
	})

	t.Run("resumes a bottom-up abort a crash interrupted before the cascade", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		found := seed(t, path, []string{"c1"}, func(set func(durable.TaskId, func(*anyRecord)), found seeded) {
			set(found.parent, func(record *anyRecord) {
				record.AbortRequested = true
				record.State = seedWaiting(t, found.children, "allSettled")
			})
		})
		opened := openNodes(t, openSqlite(t, path))
		expectTaskOutcomeKind(t, opened.harness, found.parent, "aborted")
		expectTaskOutcomeKind(t, opened.harness, found.children[0], "aborted")
		expectEqualJSON(t, logWithPrefix("abort:"), `["abort:c1","abort:parent"]`)
		closeHarness(t, opened.harness)
	})

	t.Run("reopens a checkout waiting on live payments and finishes it", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		found := joinParentOf("checkout", []string{"p1", "p2"}, durable.JoinFailFast)
		opened := openNodes(t, openSqlite(t, path))
		parent := startNode(t, opened.root, "checkout")
		waitFor(t, statusIs(t, opened.harness, parent, durable.TaskWaiting))
		waitFor(t, func() bool { return logged("run:p1") && logged("run:p2") })
		closeHarness(t, opened.harness)

		opened = openNodes(t, openSqlite(t, path))
		found.ids.mu.Lock()
		found.ids.ids = slices.Clone(taskState(t, opened.harness, parent).On)
		found.ids.mu.Unlock()
		openGate("p1")
		openGate("p2")
		expectTaskOutcomeKind(t, opened.harness, parent, "completed")
		expectEqualJSON(t, found.read(), `["completed","completed"]`)
		closeHarness(t, opened.harness)
	})
}

// ─── Built-in tool rounds ───────────────────────────────────────────────────

type blocking struct {
	started      *deferredGate
	registration durable.ToolRegistration
}

func structuredBlockingTool(name string) blocking {
	started := deferred()
	return blocking{started: started, registration: durable.ToolRegistration{
		ToolSchema:    ai.ToolSchema{Name: name, Description: "The " + name + " tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		ExecutionMode: durable.ToolExecutionSequential,
		Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			api.Output("partial")
			started.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		},
	}}
}

func toolCalls(calls ...[2]string) ai.FauxResponseStep {
	blocks := []ai.FauxContentBlock{}
	for _, call := range calls {
		blocks = append(blocks, ai.FauxToolCall(call[0], map[string]any{}, &ai.FauxToolCallOptions{ID: call[1]}))
	}
	return toolCallStep(blocks...)
}

func toolResults(t *testing.T, conversation Conversation) []ai.ToolResultMessage {
	t.Helper()
	results := []ai.ToolResultMessage{}
	for _, entry := range allEntries(t, conversation) {
		if durable.ToolResultEntry.Is(&entry) {
			results = append(results, entry.Model[0].(ai.ToolResultMessage))
		}
	}
	return results
}

func scanTasks(t *testing.T, harness Harness, query durable.TaskQuery, limit int) []anyRecord {
	t.Helper()
	return must(durable.Commit(testContext, harness, func(tx durable.Tx) (durable.Page[anyRecord, durable.Cursor], error) {
		return tx.ScanTasks(query, limit, nil)
	})).Items
}

// Pi source: packages/durable/src/harness/types.ts
// mutation-checked: dropping the reads and writes of GenerationHooks.AfterTools fails it
func TestToolRounds(t *testing.T) {
	t.Run("owns its tool tasks, waits for them, and hands the run to a conversation-owned generation", func(t *testing.T) {
		resetNodes(t)
		setup := chatSetup(t)
		addTool(t, setup.Registry, noopTool("noop"))
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCalls([2]string{"noop", "c1"}, [2]string{"noop", "c2"}), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		expectSettled(t, must(submitInput(t, root, "go").Wait(testContext)), durable.SubmissionDone, "")
		rootId := root.Id()
		tasks := scanTasks(t, harness, durable.TaskQuery{ConversationId: &rootId}, 20)
		var generations []anyRecord
		owners := []any{}
		for _, task := range tasks {
			switch task.Kind {
			case "pi.generation":
				generations = append(generations, task)
			case "pi.tool":
				owners = append(owners, task.Owner)
			}
		}
		first, second := generations[0], generations[1]
		expectEqualJSON(t, owners, jsonText(t, []any{first.Id, first.Id}))
		if first.Owner != nil || second.Owner != nil {
			t.Fatal("a generation has an owner")
		}
		assistant := assistants(t, root)[0]
		expectEqualJSON(t, first.State, jsonText(t, map[string]any{"status": "terminal", "outcome": map[string]any{"status": "completed", "result": map[string]any{"entryId": assistant.Id}}}))
		closeHarness(t, harness)
	})

	t.Run("aborts a parallel round with its generation: tools first, then the generation, with every result written", func(t *testing.T) {
		resetNodes(t)
		setup := chatSetup(t)
		one, two := structuredBlockingTool("one"), structuredBlockingTool("two")
		one.registration.ExecutionMode = durable.ToolExecutionParallel
		two.registration.ExecutionMode = durable.ToolExecutionParallel
		addTool(t, setup.Registry, new(one.registration))
		addTool(t, setup.Registry, new(two.registration))
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCalls([2]string{"one", "c1"}, [2]string{"two", "c2"})})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := submitInput(t, root, "go")
		awaitGate(t, one.started)
		awaitGate(t, two.started)
		generation := liveOf(t, harness, root.Id()).Run.TaskId
		must(harness.AbortTask(testContext, generation))
		expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		calls := []string{}
		for _, result := range toolResults(t, root) {
			calls = append(calls, result.ToolCallID)
		}
		expectEqualJSON(t, calls, `["c1","c2"]`)
		expectEqualJSON(t, liveOf(t, harness, root.Id()), `{}`)
		expectTaskOutcomeKind(t, harness, generation, "aborted")
		closeHarness(t, harness)
	})

	t.Run("answers the unstarted calls of an aborted sequential round with aborted results, in call order", func(t *testing.T) {
		resetNodes(t)
		setup := chatSetup(t)
		one := structuredBlockingTool("one")
		addTool(t, setup.Registry, new(one.registration))
		addTool(t, setup.Registry, new(structuredBlockingTool("two").registration))
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCalls([2]string{"one", "c1"}, [2]string{"two", "c2"}, [2]string{"two", "c3"})})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := submitInput(t, root, "go")
		awaitGate(t, one.started)
		started := []bool{}
		for _, slot := range liveOf(t, harness, root.Id()).Tools {
			started = append(started, slot.TaskId != nil)
		}
		expectEqualJSON(t, started, `[true,false,false]`)
		generation := liveOf(t, harness, root.Id()).Run.TaskId
		must(harness.AbortTask(testContext, generation))
		expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		results := toolResults(t, root)
		calls, errs := []string{}, []bool{}
		for _, result := range results {
			calls = append(calls, result.ToolCallID)
			errs = append(errs, result.IsError)
		}
		expectEqualJSON(t, calls, `["c1","c2","c3"]`)
		expectEqualJSON(t, errs, `[true,true,true]`)
		expectEqualJSON(t, results[1].Content, `[{"type":"text","text":"<harness>\n[error] Tool two was aborted\n</harness>"}]`)
		rootId, kind := root.Id(), "pi.tool"
		if tools := scanTasks(t, harness, durable.TaskQuery{ConversationId: &rootId, Kind: &kind}, 20); len(tools) != 1 {
			t.Fatalf("tool tasks = %d, want 1", len(tools))
		}
		closeHarness(t, harness)
	})

	t.Run("ends a turn at the generation's hold, before its successor's turn starts", func(t *testing.T) {
		resetNodes(t)
		setup := chatSetup(t)
		addTask(t, setup.Registry, nodeTask)
		addTool(t, setup.Registry, noopTool("noop"))
		// An extension's hook starts work owned by the generation, which holds it while the next turn runs.
		var opened Harness
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{AfterTools: func(ctx context.Context, _ durable.EntryId, _ []durable.EntryId, api HookApi) error {
			_, err := durable.Commit(ctx, opened, func(tx durable.Tx) (durable.TaskId, error) {
				return durable.CreateTask(tx, nodeTask, nodeInput{Name: "hooked"}, ownedBy(api.TaskId()))
			})
			return err
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCalls([2]string{"noop", "c1"}), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		opened = harness
		stream := must(WatchEvents(testContext, harness, root.Id()))
		var mu sync.Mutex
		turns := []string{}
		stream.Start(func(_ context.Context, batch []AgentEvent) error {
			mu.Lock()
			for _, event := range batch {
				if strings.HasPrefix(event.EventType(), "turn_") {
					turns = append(turns, event.EventType())
				}
			}
			mu.Unlock()
			return nil
		})
		turnTypes := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(turns)
		}
		must(submitInput(t, root, "go").Wait(testContext))
		kind := "pi.generation"
		first := scanTasks(t, harness, durable.TaskQuery{Kind: &kind}, 1)[0]
		if status := taskState(t, harness, first.Id).Status; status != durable.TaskCompleting {
			t.Fatalf("first generation = %s, want completing", status)
		}
		waitFor(t, func() bool { return len(turnTypes()) == 4 })
		expectEqualJSON(t, turnTypes(), `["turn_start","turn_end","turn_start","turn_end"]`)
		openGate("hooked")
		expectTaskOutcomeKind(t, harness, first.Id, "completed")
		waitFor(t, func() bool { return len(turnTypes()) == 4 })
		must(stream.Stop())
		closeHarness(t, harness)
	})

	t.Run("keeps run control with a faulted generation until its owned work drains, and retries a rejected final commit", func(t *testing.T) {
		resetNodes(t)
		setup := chatSetup(t)
		withProvider(setup, func(provider *ai.ModelsProvider) {
			provider.StreamSimple = func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				// The final message is not strict JSON, so the classification commit fails and the task faults. A Go
				// message cannot hold a function, so a non-finite number stands for upstream's function.
				final := fauxMessage("final", ai.StopReasonStop)
				final.Usage.Cost.Total = math.NaN()
				return streamOf(fauxMessage("partial", ai.StopReasonPending), 300*time.Millisecond, final), nil
			}
		})
		addTask(t, setup.Registry, nodeTask)
		var opened Harness
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{BeforeRequest: func(ctx context.Context, _ GenerationRequest, api HookApi) (*GenerationRequest, error) {
			_, err := durable.Commit(ctx, opened, func(tx durable.Tx) (durable.TaskId, error) {
				return durable.CreateTask(tx, nodeTask, nodeInput{Name: "hooked"}, ownedBy(api.TaskId()))
			})
			return nil, err
		}})
		scriptNode("hooked", nodeBehavior{abort: gatedAbort("abort.hooked")})
		store := &rejectingStorage{MemoryStorage: storage.NewMemoryStorage()}
		harness, root := openChat(t, store, setup)
		opened = harness
		submission := submitInput(t, root, "hi")
		waitFor(t, func() bool {
			run := liveOf(t, harness, root.Id()).Run
			return run != nil && taskState(t, harness, run.TaskId).Status == durable.TaskCompleting
		})
		generation := liveOf(t, harness, root.Id()).Run.TaskId
		expectMatch(t, taskState(t, harness, generation), `{"status":"completing","outcome":{"status":"faulted"}}`)
		waited := make(chan durable.SubmissionRecord, 1)
		go func() { waited <- must(submission.Wait(testContext)) }()
		flush()
		if len(waited) != 0 || liveOf(t, harness, root.Id()).Run.TaskId != generation {
			t.Fatal("the run left the faulted generation before its work drained")
		}
		// The faulted outcome is cancellation intent: the hooked work is aborted, then the run settles.
		waitFor(t, func() bool { return logged("abort:hooked") })
		store.reject(generation)
		openGate("abort.hooked")
		// The final commit, with the run's cleanup, is rejected once: nothing of the cleanup lands.
		waitFor(t, func() bool {
			return slices.ContainsFunc(setup.Reports.all(), func(err error) bool {
				var rejected *durable.StorageRejected
				return errors.As(err, &rejected)
			})
		})
		if liveOf(t, harness, root.Id()).Run.TaskId != generation {
			t.Fatal("the run moved on after a rejected final commit")
		}
		expectKinds(t, allEntries(t, root), "pi.user")
		if len(waited) != 0 {
			t.Fatal("the submission settled after a rejected final commit")
		}
		// The next commit retries it; the partial becomes one aborted entry.
		appendNote(t, root, "note")
		expectSettled(t, <-waited, durable.SubmissionUnanswered, "faulted")
		expectEqualJSON(t, liveOf(t, harness, root.Id()), `{}`)
		expectKinds(t, allEntries(t, root), "pi.user", "note", "pi.assistant")
		closeHarness(t, harness)
	})
}

// rejectingStorage rejects, once, the commit that finalizes the task it is told to reject.
type rejectingStorage struct {
	*storage.MemoryStorage
	mu     sync.Mutex
	target *durable.TaskId
}

func (store *rejectingStorage) reject(id durable.TaskId) {
	store.mu.Lock()
	store.target = &id
	store.mu.Unlock()
}

func (store *rejectingStorage) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	store.mu.Lock()
	target := store.target
	finalizes := target != nil && slices.ContainsFunc(writes, func(write durable.StorageWrite) bool {
		task, ok := write.(durable.TaskWrite)
		return ok && task.Value.Id == *target && task.Value.State.Status == durable.TaskTerminal
	})
	if finalizes {
		store.target = nil
	}
	store.mu.Unlock()
	if finalizes {
		return 0, durable.NewStorageRejected("rejected once", nil)
	}
	return store.MemoryStorage.Commit(ctx, writes)
}

// ─── Review follow-ups ──────────────────────────────────────────────────────

type versionedInput struct {
	On []durable.TaskId `json:"on"`
}

type phaseOnly struct {
	Phase string `json:"phase"`
}

type versionedRuntime = durable.TaskRuntime[versionedInput, phaseOnly, string, any]
type versionedRecord = durable.RunningTask[versionedInput, phaseOnly, string]
type versionedNext = durable.NextTaskState[phaseOnly, string]

// versioned is a waiter of its own kind, so its definition can be removed or replaced.
func versioned(version int) durable.Task[versionedInput, phaseOnly, string, any] {
	complete := func(result string) durable.PhaseHandler[versionedInput, phaseOnly, string, any] {
		return func(ctx context.Context, _ versionedRecord, runtime versionedRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, versionedRecord) (*versionedNext, error) {
				return &versionedNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeCompleted, Result: &result}}, nil
			})
		}
	}
	definition := durable.TaskDefinition[versionedInput, phaseOnly, string, any]{
		Name:    "test.versioned",
		Version: version,
		Initial: func(versionedInput) phaseOnly { return phaseOnly{Phase: "wait"} },
		Phases: map[string]durable.PhaseHandler[versionedInput, phaseOnly, string, any]{
			"wait": func(ctx context.Context, task versionedRecord, runtime versionedRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, versionedRecord) (*versionedNext, error) {
					return &versionedNext{Status: durable.TaskWaiting, Checkpoint: &phaseOnly{Phase: "resume"}, On: task.Input.On, Policy: durable.JoinAllSettled}, nil
				})
			},
			"resume":   complete("v1"),
			"migrated": complete("v2"),
		},
		Abort: func(ctx context.Context, _ versionedRecord, runtime versionedRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, versionedRecord) (*versionedNext, error) {
				return &versionedNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted}}, nil
			})
		},
	}
	if version == 2 {
		definition.Migrate = func(input durable.JsonValue, _ durable.JsonValue, _ int) (versionedInput, phaseOnly, error) {
			decoded, err := durable.FromJsonValue[versionedInput](input)
			return decoded, phaseOnly{Phase: "migrated"}, err
		}
	}
	return durable.DefineTask(definition)
}

// spawnAndFinish scripts name to spawn child and then complete, holding while the child lives.
func spawnAndFinish(name, child string) *nodeIds {
	found := &nodeIds{}
	spawnAndResume(name, child, found, nil)
	return found
}

func inspectState(t *testing.T, harness Harness, id durable.TaskId) TaskInspectionState {
	t.Helper()
	inspection := must(harness.Inspect(testContext))
	index := slices.IndexFunc(inspection.Tasks, func(task TaskInspection) bool { return task.Record.Id == id })
	if index < 0 {
		t.Fatalf("task %d is not live", id)
	}
	return inspection.Tasks[index].State
}

func TestDefinitionsAndWaits(t *testing.T) {
	t.Run("keeps a waiting task blocked without its definition and resumes it migrated under a newer one", func(t *testing.T) {
		opened := openNodes(t)
		harness, root, registry := opened.harness, opened.root, opened.registry
		registration := addTask(t, registry, versioned(1))
		other := startNode(t, root, "other")
		waiter := commitValue(t, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, versioned(1), versionedInput{On: []durable.TaskId{other}}, conversationOwnedTask)
		})
		waitFor(t, statusIs(t, harness, waiter, durable.TaskWaiting))
		registration.dispose()
		openGate("other")
		must(harness.WaitForTask(testContext, other))
		if state := inspectState(t, harness, waiter); state.Kind != TaskInspectionBlocked || state.Reason != "missing_task" || state.Error != nil {
			t.Fatalf("inspection = %+v", state)
		}
		if status := taskState(t, harness, waiter).Status; status != durable.TaskWaiting {
			t.Fatalf("waiter = %s, want waiting", status)
		}
		addTask(t, registry, versioned(2))
		expectEqualJSON(t, must(harness.WaitForTask(testContext, waiter)).State.Outcome, `{"status":"completed","result":"v2"}`)
		closeHarness(t, harness)
	})

	t.Run("orphans an aborted waiting task without its definition at once, leaving the task it waits on running", func(t *testing.T) {
		opened := openNodes(t)
		harness, root, registry := opened.harness, opened.root, opened.registry
		registration := addTask(t, registry, versioned(1))
		other := startNode(t, root, "other")
		waiter := commitValue(t, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, versioned(1), versionedInput{On: []durable.TaskId{other}}, conversationOwnedTask)
		})
		waitFor(t, statusIs(t, harness, waiter, durable.TaskWaiting))
		registration.dispose()
		if result := must(harness.AbortTask(testContext, waiter)); result != "marked" {
			t.Fatalf("abort = %s, want marked", result)
		}
		expectEqualJSON(t, taskState(t, harness, waiter).Outcome, `{"status":"orphaned","reason":"missing_task"}`)
		if status := taskState(t, harness, other).Status; status != durable.TaskRunning {
			t.Fatalf("other = %s, want running", status)
		}
		openGate("other")
		closeHarness(t, harness)
	})

	t.Run("never migrates a held outcome: a newer definition leaves it and its version alone", func(t *testing.T) {
		opened := openNodes(t)
		harness, root, registry, reports := opened.harness, opened.root, opened.registry, opened.reports
		type holderRecord = durable.RunningTask[durable.JsonValue, phaseOnly, string]
		type holderRuntime = durable.TaskRuntime[durable.JsonValue, phaseOnly, string, any]
		type holderNext = durable.NextTaskState[phaseOnly, string]
		holder := func(version int) durable.Task[durable.JsonValue, phaseOnly, string, any] {
			return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, phaseOnly, string, any]{
				Name:    "test.holder",
				Version: version,
				Initial: func(durable.JsonValue) phaseOnly { return phaseOnly{Phase: "run"} },
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, phaseOnly, string, any]{
					"run": func(ctx context.Context, task holderRecord, runtime holderRuntime) error {
						if err := runtime.Commit(ctx, func(tx durable.Tx, _ holderRecord) (*holderNext, error) {
							_, err := spawn(tx, task.Id, "child")
							return nil, err
						}); err != nil {
							return err
						}
						held := "held"
						return runtime.Commit(ctx, func(durable.Tx, holderRecord) (*holderNext, error) {
							return &holderNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeCompleted, Result: &held}}, nil
						})
					},
				},
				Abort: func(ctx context.Context, _ holderRecord, runtime holderRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, holderRecord) (*holderNext, error) {
						return &holderNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted}}, nil
					})
				},
				Migrate: func(durable.JsonValue, durable.JsonValue, int) (durable.JsonValue, phaseOnly, error) {
					return nil, phaseOnly{}, errors.New("never migrates")
				},
			})
		}
		addTask(t, registry, holder(1))
		parent := commitValue(t, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask[durable.JsonValue](tx, holder(1), nil, conversationOwnedTask)
		})
		waitFor(t, statusIs(t, harness, parent, durable.TaskCompleting))
		// The same extension name replaces the old one in place.
		addTask(t, registry, holder(2))
		openGate("child")
		settledParent := must(harness.WaitForTask(testContext, parent))
		expectEqualJSON(t, settledParent.State.Outcome, `{"status":"completed","result":"held"}`)
		if settledParent.Version != 1 || len(reports.all()) != 0 {
			t.Fatalf("version = %d, reports = %v", settledParent.Version, reports.all())
		}
		closeHarness(t, harness)
	})

	t.Run("treats a held task as live: outcomes() rejects, and a task waiting on it resumes at its final commit", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		spawnAndFinish("held", "child")
		held := startNode(t, root, "held")
		waitFor(t, statusIs(t, harness, held, durable.TaskCompleting))
		var mu sync.Mutex
		rejection := ""
		scriptNode("reader", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			_, err := runtime.Outcomes(ctx, []durable.TaskId{held})
			mu.Lock()
			rejection = "resolved"
			if err != nil {
				rejection = err.Error()
			}
			mu.Unlock()
			return commitNext(ctx, runtime, func(durable.Tx) (*nodeNext, error) {
				return waitOn([]durable.TaskId{held}, durable.JoinAllSettled), nil
			})
		}})
		reader := startNode(t, root, "reader")
		waitFor(t, statusIs(t, harness, reader, durable.TaskWaiting))
		mu.Lock()
		if want := fmt.Sprintf("Task %d is not terminal", held); rejection != want {
			t.Fatalf("rejection = %q, want %q", rejection, want)
		}
		mu.Unlock()
		openGate("child")
		expectTaskOutcomeKind(t, harness, reader, "completed")
		if !logged("resume:reader") {
			t.Fatal("the reader never resumed")
		}
		closeHarness(t, harness)
	})
}

func TestConversationAbortAndBoundaries(t *testing.T) {
	t.Run("marks a held completed task with Conversation.abort(): the work below is aborted, the outcome stays", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		child := spawnAndFinish("held", "child")
		held := startNode(t, root, "held")
		waitFor(t, statusIs(t, harness, held, durable.TaskCompleting))
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		expectTaskOutcomeKind(t, harness, child.all()[0], "aborted")
		record := must(harness.WaitForTask(testContext, held))
		if record.State.Outcome.Status != durable.OutcomeCompleted || !record.AbortRequested {
			t.Fatalf("held = %s", jsonText(t, record))
		}
		closeHarness(t, harness)
	})

	t.Run("marks and awaits only the background work reached when { background: true } is admitted", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		scriptNode("first", nodeBehavior{abort: gatedAbort("abort.first")})
		background := conversationOwnedTask
		background.Background = true
		first := startNode(t, root, "first", background)
		waitFor(t, func() bool { return logged("run:first") })
		aborting := make(chan error, 1)
		go func() { aborting <- root.Abort(testContext, &durable.ConversationAbortOptions{Background: true}) }()
		waitFor(t, func() bool { return abortRequested(t, harness, first) })
		later := startNode(t, root, "later", background)
		openGate("abort.first")
		if err := <-aborting; err != nil {
			t.Fatal(err)
		}
		if taskState(t, harness, later).Status == durable.TaskTerminal || abortRequested(t, harness, later) {
			t.Fatal("later work was reached by an earlier abort")
		}
		must(harness.AbortTask(testContext, later))
		closeHarness(t, harness)
	})

	t.Run("stops a cascade at an unmarked background task, but not at a marked one", func(t *testing.T) {
		opened := openNodes(t)
		harness, root := opened.harness, opened.root
		scriptNode("owner", nodeBehavior{abort: gatedAbort("abort.owner")})
		type treeIds struct {
			owner, background durable.TaskId
			outer, inner      durable.ConversationId
		}
		tree := commitValue(t, root, func(tx durable.Tx) (treeIds, error) {
			owner, err := durable.CreateTask(tx, nodeTask, nodeInput{Name: "owner"}, conversationOwnedTask)
			if err != nil {
				return treeIds{}, err
			}
			outer, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
			if err != nil {
				return treeIds{}, err
			}
			options := conversationOwnedTask
			options.ConversationId = &outer.Id
			options.Background = true
			background, err := durable.CreateTask(tx, nodeTask, nodeInput{Name: "background"}, options)
			if err != nil {
				return treeIds{}, err
			}
			inner, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: background}})
			if err != nil {
				return treeIds{}, err
			}
			return treeIds{owner: owner, background: background, outer: outer.Id, inner: inner.Id}, nil
		})
		waitFor(t, func() bool { return logged("run:owner") && logged("run:background") })
		// The owner's abort handler starts at once: background work is not its ordinary owned work.
		must(harness.AbortTask(testContext, tree.owner))
		waitFor(t, func() bool { return logged("abort:owner") })
		inner := must(harness.Conversation(testContext, tree.inner))
		outer := must(harness.Conversation(testContext, tree.outer))
		shielded := startNode(t, inner, "shielded")
		exposed := startNode(t, outer, "exposed")
		expectTaskOutcomeKind(t, harness, exposed, "aborted")
		if abortRequested(t, harness, shielded) {
			t.Fatal("the cascade crossed an unmarked background task")
		}
		// Marked directly, the background task cascades into its own subtree.
		must(harness.AbortTask(testContext, tree.background))
		expectTaskOutcomeKind(t, harness, shielded, "aborted")
		expectTaskOutcomeKind(t, harness, tree.background, "aborted")
		openGate("abort.owner")
		expectTaskOutcomeKind(t, harness, tree.owner, "aborted")
		closeHarness(t, harness)
	})

	t.Run("retries a finalization the Storage rejected with the next commit", func(t *testing.T) {
		store := &rejectingStorage{MemoryStorage: storage.NewMemoryStorage()}
		opened := openNodes(t, store)
		harness, root, reports := opened.harness, opened.root, opened.reports
		child := spawnAndFinish("parent", "child")
		parent := startNode(t, root, "parent")
		waitFor(t, statusIs(t, harness, parent, durable.TaskCompleting))
		store.reject(parent)
		openGate("child")
		must(harness.WaitForTask(testContext, child.all()[0]))
		waitFor(t, func() bool {
			return slices.ContainsFunc(reports.all(), func(err error) bool {
				var rejected *durable.StorageRejected
				return errors.As(err, &rejected)
			})
		})
		if status := taskState(t, harness, parent).Status; status != durable.TaskCompleting {
			t.Fatalf("parent = %s, want completing", status)
		}
		appendNote(t, root, "note")
		expectTaskOutcomeKind(t, harness, parent, "completed")
		closeHarness(t, harness)
	})
}

func TestRecoveryThroughOwnershipEdges(t *testing.T) {
	t.Run("shows an abort-marked owner waiting for work in its owned conversation before resume after reopen", func(t *testing.T) {
		resetNodes(t)
		path := filepath.Join(t.TempDir(), "session.sqlite")
		line := session.CreateSession(openSqlite(t, path))
		type treeIds struct{ owner, inner durable.TaskId }
		tree := must(commitOnLine(testContext, line, session.TransactionScope{}, func(tx *session.Transaction) (treeIds, error) {
			conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
			if err != nil {
				return treeIds{}, err
			}
			options := conversationOwnedTask
			options.ConversationId = &conversation.Id
			owner, err := durable.CreateTask(tx, nodeTask, nodeInput{Name: "owner"}, options)
			if err != nil {
				return treeIds{}, err
			}
			child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
			if err != nil {
				return treeIds{}, err
			}
			innerOptions := conversationOwnedTask
			innerOptions.ConversationId = &child.Id
			inner, err := durable.CreateTask(tx, nodeTask, nodeInput{Name: "inner"}, innerOptions)
			return treeIds{owner: owner, inner: inner}, err
		}))
		must(commitOnLine(testContext, line, session.TransactionScope{}, func(tx *session.Transaction) (struct{}, error) {
			record, err := tx.Task(tree.owner)
			if err != nil {
				return struct{}{}, err
			}
			marked := *record
			marked.AbortRequested = true
			return struct{}{}, tx.SetTask(marked)
		}))
		if err := line.Close(testContext); err != nil {
			t.Fatal(err)
		}

		harness, _, _ := openTasks(t, openSqlite(t, path), []durable.AnyTask{nodeTask})
		if state := inspectState(t, harness, tree.owner); state.Kind != TaskInspectionWaiting || !slices.Equal(state.On, []durable.TaskId{tree.inner}) {
			t.Fatalf("inspection = %+v", state)
		}
		harness.Resume()
		expectTaskOutcomeKind(t, harness, tree.owner, "aborted")
		expectEqualJSON(t, logWithPrefix("abort:"), `["abort:inner","abort:owner"]`)
		closeHarness(t, harness)
	})

	t.Run("keeps the root busy after reopen for work below a child task's owned conversation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		inner := &nodeIds{}
		scriptNode("child", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: runtime.TaskId()}})
				if err != nil {
					return nil, err
				}
				options := conversationOwnedTask
				options.ConversationId = &conversation.Id
				id, err := durable.CreateTask(tx, nodeTask, nodeInput{Name: "inner"}, options)
				if err != nil {
					return nil, err
				}
				inner.add(id)
				return resumeNext(1), nil
			})
		}})
		joinParentOf("parent", []string{"child"}, durable.JoinAllSettled)
		opened := openNodes(t, openSqlite(t, path))
		parent := startNode(t, opened.root, "parent")
		waitFor(t, func() bool { return logged("run:inner") })
		closeHarness(t, opened.harness)

		opened = openNodes(t, openSqlite(t, path))
		idle := make(chan struct{})
		go func() {
			defer close(idle)
			_ = opened.root.WaitForIdle(testContext)
		}()
		if settled(idle) {
			t.Fatal("the root is idle with work below a child's conversation")
		}
		openGate("inner")
		<-idle
		expectTaskOutcomeKind(t, opened.harness, parent, "completed")
		if inner.len() == 0 {
			t.Fatal("inner was never created")
		}
		closeHarness(t, opened.harness)
	})

	t.Run("keeps a finished background ancestor a boundary after reopen, which { background: true } crosses", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		var mu sync.Mutex
		var conversation durable.ConversationId
		scriptNode("child", nodeBehavior{run: func(ctx context.Context, runtime nodeRuntime) error {
			return commitNext(ctx, runtime, func(tx durable.Tx) (*nodeNext, error) {
				record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: runtime.TaskId()}})
				if err != nil {
					return nil, err
				}
				mu.Lock()
				conversation = record.Id
				mu.Unlock()
				return resumeNext(1), nil
			})
		}})
		joinParentOf("background", []string{"child"}, durable.JoinAllSettled)
		opened := openNodes(t, openSqlite(t, path))
		backgroundOptions := conversationOwnedTask
		backgroundOptions.Background = true
		background := startNode(t, opened.root, "background", backgroundOptions)
		must(opened.harness.WaitForTask(testContext, background))
		closeHarness(t, opened.harness)

		opened = openNodes(t, openSqlite(t, path))
		mu.Lock()
		id := conversation
		mu.Unlock()
		below := startNode(t, must(opened.harness.Conversation(testContext, id)), "below")
		waitFor(t, func() bool { return logged("run:below") })
		if err := opened.root.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if err := opened.root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		if status := taskState(t, opened.harness, below).Status; status != durable.TaskRunning {
			t.Fatalf("below = %s, want running", status)
		}
		if err := opened.root.Abort(testContext, &durable.ConversationAbortOptions{Background: true}); err != nil {
			t.Fatal(err)
		}
		expectTaskOutcomeKind(t, opened.harness, below, "aborted")
		closeHarness(t, opened.harness)
	})
}

// ─── Tool rounds and events ─────────────────────────────────────────────────

type eventListener struct {
	stream *AgentEventStream
	mu     sync.Mutex
	events []AgentEvent
}

func listenEvents(t *testing.T, harness Harness, conversation Conversation) *eventListener {
	t.Helper()
	found := &eventListener{stream: must(WatchEvents(testContext, harness, conversation.Id()))}
	found.stream.Start(func(_ context.Context, batch []AgentEvent) error {
		found.mu.Lock()
		found.events = append(found.events, batch...)
		found.mu.Unlock()
		return nil
	})
	return found
}

func (found *eventListener) all() []AgentEvent {
	found.mu.Lock()
	defer found.mu.Unlock()
	return slices.Clone(found.events)
}

func (found *eventListener) count(eventType string) int {
	count := 0
	for _, event := range found.all() {
		if event.EventType() == eventType {
			count++
		}
	}
	return count
}

// labels are event types, with tool ends and message ends labelled by call ID and whether an entry came along.
func (found *eventListener) labels() []string {
	labels := []string{}
	for _, event := range found.all() {
		switch typed := event.(type) {
		case ToolExecutionEndEvent:
			labels = append(labels, fmt.Sprintf("end:%s:%v", typed.ToolCallId, typed.Entry != nil))
		case MessageEndEvent:
			if len(typed.Entry.Model) > 0 {
				if result, ok := typed.Entry.Model[0].(ai.ToolResultMessage); ok {
					labels = append(labels, "result:"+result.ToolCallID)
					continue
				}
				labels = append(labels, "message:"+roles(typed.Entry.Model[:1])[0])
			}
		}
	}
	return labels
}

func TestToolRoundsAndEvents(t *testing.T) {
	t.Run("ends an aborted sequential round's unstarted calls with their result entries, right before them", func(t *testing.T) {
		resetNodes(t)
		setup := chatSetup(t)
		one := structuredBlockingTool("one")
		addTool(t, setup.Registry, new(one.registration))
		addTool(t, setup.Registry, new(structuredBlockingTool("two").registration))
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCalls([2]string{"one", "c1"}, [2]string{"two", "c2"}, [2]string{"two", "c3"})})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		found := listenEvents(t, harness, root)
		submission := submitInput(t, root, "go")
		awaitGate(t, one.started)
		must(harness.AbortTask(testContext, liveOf(t, harness, root.Id()).Run.TaskId))
		must(submission.Wait(testContext))
		waitFor(t, func() bool { return slices.Contains(found.labels(), "result:c3") })
		labels := []string{}
		for _, label := range found.labels() {
			if !strings.HasPrefix(label, "message:") {
				labels = append(labels, label)
			}
		}
		expectEqualJSON(t, labels, `["end:c1:true","result:c1","end:c2:true","result:c2","end:c3:true","result:c3"]`)
		must(found.stream.Stop())
		closeHarness(t, harness)
	})

	t.Run("emits one turn_end per generation, also for a stream attached while it holds", func(t *testing.T) {
		resetNodes(t)
		setup := chatSetup(t)
		addTask(t, setup.Registry, nodeTask)
		addTool(t, setup.Registry, noopTool("noop"))
		var opened Harness
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{AfterTools: func(ctx context.Context, _ durable.EntryId, _ []durable.EntryId, api HookApi) error {
			_, err := durable.Commit(ctx, opened, func(tx durable.Tx) (durable.TaskId, error) {
				return durable.CreateTask(tx, nodeTask, nodeInput{Name: "hooked"}, ownedBy(api.TaskId()))
			})
			return err
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCalls([2]string{"noop", "c1"}), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		opened = harness
		early := listenEvents(t, harness, root)
		must(submitInput(t, root, "go").Wait(testContext))
		late := listenEvents(t, harness, root)
		openGate("hooked")
		kind := "pi.generation"
		first := scanTasks(t, harness, durable.TaskQuery{Kind: &kind}, 1)[0]
		must(harness.WaitForTask(testContext, first.Id))
		appendNote(t, root, "note")
		waitFor(t, func() bool { return late.count("entry_appended") > 0 })
		if early.count("turn_end") != 2 || late.count("turn_end") != 0 {
			t.Fatalf("turn_end: early %d, late %d", early.count("turn_end"), late.count("turn_end"))
		}
		must(early.stream.Stop())
		must(late.stream.Stop())
		closeHarness(t, harness)
	})

	t.Run("lets a tool that owns live work finish its call at the hold while the generation waits for its final commit", func(t *testing.T) {
		resetNodes(t)
		setup := chatSetup(t)
		addTask(t, setup.Registry, nodeTask)
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: "delegate", Description: "Starts work in a conversation it owns", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}, Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			_, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: api.TaskId()}})
				if err != nil {
					return nil, err
				}
				options := conversationOwnedTask
				options.ConversationId = &child.Id
				return durable.CreateTask(tx, nodeTask, nodeInput{Name: "sub"}, options)
			})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "started"}}}, nil
		}}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCalls([2]string{"delegate", "c1"}), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		found := listenEvents(t, harness, root)
		submission := submitInput(t, root, "go")
		waitFor(t, func() bool {
			tools := liveOf(t, harness, root.Id()).Tools
			return len(tools) > 0 && tools[0].Status == ToolSlotDone
		})
		state := liveOf(t, harness, root.Id())
		tool := *state.Tools[0].TaskId
		if state.Tools[0].Entry == nil || taskState(t, harness, tool).Status != durable.TaskCompleting || taskState(t, harness, state.Run.TaskId).Status != durable.TaskWaiting {
			t.Fatalf("live = %s", jsonText(t, state))
		}
		waitFor(t, func() bool { return slices.Contains(found.labels(), "end:c1:true") })
		waited := make(chan durable.SubmissionRecord, 1)
		go func() { waited <- must(submission.Wait(testContext)) }()
		flush()
		if len(waited) != 0 {
			t.Fatal("the submission settled while the tool's work runs")
		}
		openGate("sub")
		expectSettled(t, <-waited, durable.SubmissionDone, "")
		expectTaskOutcomeKind(t, harness, tool, "completed")
		must(found.stream.Stop())
		closeHarness(t, harness)
	})

	t.Run("holds a faulted tool's slot and task_failed until the work it owns drained", func(t *testing.T) {
		resetNodes(t)
		setup := chatSetup(t)
		addTask(t, setup.Registry, nodeTask)
		scriptNode("held", nodeBehavior{abort: gatedAbort("abort.held")})
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: "broken", Description: "Starts owned work, then returns a result that is not strict JSON", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}, Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			if _, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				return durable.CreateTask(tx, nodeTask, nodeInput{Name: "held"}, ownedBy(api.TaskId()))
			}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{"fn": math.NaN()}, HasDetails: true}, nil
		}}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCalls([2]string{"broken", "c1"}), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		found := listenEvents(t, harness, root)
		submission := submitInput(t, root, "go")
		waitFor(t, func() bool { return logged("abort:held") })
		state := liveOf(t, harness, root.Id())
		tool := *state.Tools[0].TaskId
		expectMatch(t, taskState(t, harness, tool), `{"status":"completing","outcome":{"status":"faulted"}}`)
		if state.Tools[0].Status == ToolSlotDone || found.count("task_failed") != 0 {
			t.Fatal("the faulted tool's slot or task_failed did not wait for its owned work")
		}
		openGate("abort.held")
		expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionDone, "")
		expectTaskOutcomeKind(t, harness, tool, "faulted")
		waitFor(t, func() bool { return found.count("task_failed") > 0 })
		if !slices.Contains(found.labels(), "end:c1:false") {
			t.Fatalf("labels = %v", found.labels())
		}
		must(found.stream.Stop())
		closeHarness(t, harness)
	})
}
