package jsloop

import (
	"context"
	"sync"
	"sync/atomic"
)

// Task tells the owner of a run where the run executes one task of Pi's event loop, so the owner can keep what Pi's loop runs only between two tasks from happening inside one. The interactive mode's frame is such a thing: a timer callback (packages/tui/src/tui.ts requestRender), which cannot run between the microtasks of a task.
//
// One task is every event of a response that the loop handles before it has nothing ready, which is where its reader would wait for the provider (Begin, Park), and after the response's last event the events the loop emits without awaiting anything: message_end, the tool_execution_start of each call, turn_end and agent_end (Tail, End; packages/agent/src/agent-loop.ts runLoop).
//
// The observer is told true when a task begins and false when it ends, in order, by the goroutine that makes the change, and must not wait for the run. Ending a task early is always safe: the owner then sees what it sees without a Task.
//
// A nil *Task observes nothing.
type Task struct {
	observe func(running bool)
	// mu orders the changes and their reports.
	mu sync.Mutex
	// running is what observe was last told. tail is set from Tail to End and implies running.
	running atomic.Bool
	tail    bool
}

// NewTask returns a Task that reports to observe.
func NewTask(observe func(running bool)) *Task {
	return &Task{observe: observe}
}

// Running reports whether a task runs.
func (t *Task) Running() bool { return t != nil && t.running.Load() }

// Begin says the reader of a response took an event: a task runs from here unless one already does.
func (t *Task) Begin() {
	if t == nil || t.running.Load() {
		return
	}
	t.mu.Lock()
	t.beginLocked()
	t.mu.Unlock()
}

// Park says the run has nothing ready, so the reader of the response would wait for the provider: the task ends. After Tail it does not, because the loop's end events follow the response without a wait.
func (t *Task) Park() {
	if t == nil || !t.running.Load() {
		return
	}
	t.mu.Lock()
	if !t.tail {
		t.endLocked()
	}
	t.mu.Unlock()
}

// Tail says the response ended and the loop goes on to its end events without awaiting anything: a task runs until End.
func (t *Task) Tail() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.tail = true
	t.beginLocked()
	t.mu.Unlock()
}

// End says the run is about to await real work (a tool, an extension handler, the preparation of the next request) or is over: the task ends.
func (t *Task) End() {
	if t == nil || !t.running.Load() {
		return
	}
	t.mu.Lock()
	t.tail = false
	t.endLocked()
	t.mu.Unlock()
}

func (t *Task) beginLocked() {
	if !t.running.Load() {
		t.running.Store(true)
		t.observe(true)
	}
}

func (t *Task) endLocked() {
	if t.running.Load() {
		t.running.Store(false)
		t.observe(false)
	}
}

type (
	tasksWantedKey struct{}
	taskKey        struct{}
	taskReaderKey  struct{}
)

// WithTasks returns ctx asking the run it starts to report its tasks to its owner. The interactive mode asks; a run of any other owner reports none.
func WithTasks(ctx context.Context) context.Context {
	return context.WithValue(ctx, tasksWantedKey{}, true)
}

// TasksWanted reports whether ctx asks for the run's tasks (WithTasks).
func TasksWanted(ctx context.Context) bool {
	wanted, _ := ctx.Value(tasksWantedKey{}).(bool)
	return wanted
}

// WithTask returns ctx carrying task for the run it starts. A nil task hides the one ctx carries, for work that is not the run's own, such as a tool.
func WithTask(ctx context.Context, task *Task) context.Context {
	return context.WithValue(ctx, taskKey{}, task)
}

// TaskFrom returns the Task of the run ctx belongs to, or nil.
func TaskFrom(ctx context.Context) *Task {
	task, _ := ctx.Value(taskKey{}).(*Task)
	return task
}

// WithTaskReader returns ctx naming the iteration it is given to as the run's reader of a response: that iteration begins a task at each event it takes and parks it when nothing is ready. Only the run's own reader carries it, so a stream that forwards another reports nothing.
func WithTaskReader(ctx context.Context, task *Task) context.Context {
	return context.WithValue(ctx, taskReaderKey{}, task)
}

// TaskReaderFrom returns the Task whose reader ctx names, or nil.
func TaskReaderFrom(ctx context.Context) *Task {
	task, _ := ctx.Value(taskReaderKey{}).(*Task)
	return task
}
