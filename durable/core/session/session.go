// SPDX-License-Identifier: MIT

// Package session is pi-durable's Session and Harness semantics as a synchronous state machine: the Session line,
// the scheduler with Pi's commit order under concurrency, task lifecycle (spec §5), documents (§3, built-ins of §6
// and §8), submissions and the inbox (§6), forks, edits, compaction runs (§8.7), sub-agents, reload (§7.5), custom task
// phases and extension commits through the transaction channel (ABI section 8), and the api requests (ABI 8.1).
//
// The built-in pi.generation and pi.tool tasks are TaskMachines that package turn registers; session schedules them
// like any task and never knows their phases.
//
// Owner: dcore-session. Consumers: core (routing), turn (Runtime).
package session

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/rec"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// ErrNotImplemented is returned for an event this build does not handle yet; the core turns it into a rejected step.
// Read-only.
var ErrNotImplemented = errors.New("session: not implemented")

// TaskMachine runs one built-in task kind. The scheduler calls Invoke for every phase it starts (reservation,
// handover, resumption after waiting, recovery after reopen) and Deliver for the completion of every effect the
// machine started through rt; Abort runs the abort handler (spec §5.1). A machine keeps no state between calls except
// what it keeps per task in its own maps keyed by task ID, and it must tolerate losing that state at any idle point:
// after a reopen the scheduler invokes the phase from its checkpoint again (ADR D9).
type TaskMachine interface {
	Kind() string
	Version() int
	Invoke(rt *Runtime, task *rec.Task) error
	Deliver(rt *Runtime, task *rec.Task, ev *abi.Event) error
	Abort(rt *Runtime, task *rec.Task) error
}

// Config is what the core passes to New.
type Config struct {
	Store    *store.Store
	Builder  *abi.Builder
	History  *history.Store
	Machines []TaskMachine
	// UUIDv7 returns one pi-ai uuidv7() value from the host's process-level generator (ABI section 4.2).
	UUIDv7 func() string
}

// Session is one handle's Pi Session and Harness.
type Session struct {
	cfg      Config
	machines map[string]TaskMachine
}

// New returns a session that has not opened.
func New(cfg Config) *Session {
	m := make(map[string]TaskMachine, len(cfg.Machines))
	for _, tm := range cfg.Machines {
		m[tm.Kind()] = tm
	}
	return &Session{cfg: cfg, machines: m}
}

// Handle processes one event other than rows (the core routes rows to the store and re-delivers deferred events). It
// returns store.ErrPending when the event must be re-delivered after reads, a *Rejection for Pi's rejections, and any
// other error as fatal.
func (s *Session) Handle(ev *abi.Event) error {
	switch ev.Kind {
	case abi.EventOpen:
		return s.handleOpen(ev)
	default:
		return ErrNotImplemented
	}
}

// handleOpen parses OpenOptions, runs store cold open, reconciles running tasks to pending (spec §5.1) and starts the
// scheduler.
func (s *Session) handleOpen(ev *abi.Event) error { return ErrNotImplemented }

// Rejection is Pi's error for a rejected event (ConversationBusy, ReadAfterWrite, unknown conversation, ...).
type Rejection struct{ Name, Message string }

func (r *Rejection) Error() string { return r.Name + ": " + r.Message }

// Runtime is what a TaskMachine gets for one task invocation: pi-durable's TaskRuntime (spec §5.1) on the Session
// line, plus the effect plumbing of the synchronous core. Every method acts immediately on the step being built.
type Runtime struct{}

// Now is the Harness clock of the current event.
func (rt *Runtime) Now() float64 { panic("session.Runtime.Now: dcore-session") }

// Commit runs change on the Session line as one Pi commit gated like TaskRuntime.commit: change appends entries,
// mutates documents and creates tasks through tx, and returns the task's next state (nil keeps it). Commit returns a
// *Rejection when the gate refuses (invocation ended, closing, terminal, abort mark).
func (rt *Runtime) Commit(change func(tx *Tx) (*NextState, error)) error {
	panic("session.Runtime.Commit: dcore-session")
}

// NextState is the state a task commits for itself (spec §5.1 NextTaskState): Running with a checkpoint, Waiting on
// tasks, or Terminal with an outcome.
type NextState struct {
	Status     rec.TaskStatus
	Checkpoint JSON    // Running, Waiting
	On         []int64 // Waiting
	AllSettled bool    // Waiting: allSettled, else failFast
	Outcome    JSON    // Terminal
}

// Effect starts an effect on behalf of the task; its completion is delivered to the machine's Deliver.
func (rt *Runtime) Effect(kind abi.EffectKind, payload []byte) uint32 {
	panic("session.Runtime.Effect: dcore-session")
}

// Timer arms a timer whose timer event is delivered to the machine's Deliver.
func (rt *Runtime) Timer(at float64, durable bool) uint32 {
	panic("session.Runtime.Timer: dcore-session")
}

// Hook starts the next handler of a hook chain (§7.2) as a hook effect; the chain rules stay in the caller.
func (rt *Runtime) Hook(name string, handler int, payload []byte) uint32 {
	panic("session.Runtime.Hook: dcore-session")
}

// Context derives conversation conv's model context through entry at (0: the tail), loading the transcript first
// (store.ErrPending: return it from Invoke or Deliver and the
// event is re-delivered after the rows).
func (rt *Runtime) Context(conv int64, at int64) (*history.View, error) {
	panic("session.Runtime.Context: dcore-session")
}

// Settings returns HarnessOptions.settings as resolved at this access (spec §5.1).
func (rt *Runtime) Settings() *Settings { panic("session.Runtime.Settings: dcore-session") }

// Agent resolves the task conversation's agent for this phase (spec §7.1).
func (rt *Runtime) Agent() (*Agent, error) { panic("session.Runtime.Agent: dcore-session") }

// Report forwards a non-fatal failure as a report notice.
func (rt *Runtime) Report(errJSON []byte) { panic("session.Runtime.Report: dcore-session") }

// Tx is pi-durable's Tx for one commit (spec §4): table reads before the first table write, ReadAfterWrite after it,
// documents with read-your-writes, creation methods that return IDs.
type Tx struct{}

// AppendEntry appends an entry to conv from an EntryDraft JSON object (byTaskId is the committing task) and returns
// its ID; the record bytes come from history.AppendEntryRecord.
func (tx *Tx) AppendEntry(conv int64, draft []byte) (int64, error) {
	panic("session.Tx.AppendEntry: dcore-session")
}

// Doc opens the draft of a document by kind (conversation scope) for read and write in this commit.
func (tx *Tx) Doc(kind string, conv int64) (*Doc, error) { panic("session.Tx.Doc: dcore-session") }

// CreateTask creates a task owned by owner (0: the conversation) and returns its ID.
func (tx *Tx) CreateTask(kind string, input JSON, conv, owner int64, background bool) (int64, error) {
	panic("session.Tx.CreateTask: dcore-session")
}

// SettleSubmission stages a submission's new status (§8.2: resolved against the latest candidate during assembly).
func (tx *Tx) SettleSubmission(id int64, status rec.SubmissionStatus, answer int64, reason string, detail []byte) error {
	panic("session.Tx.SettleSubmission: dcore-session")
}

// Doc is a document draft inside one commit.
type Doc struct{}

// Settings is the resolved Harness settings the built-in tasks read (spec §2.2 Settings).
type Settings struct {
	PartialIntervalMs float64
	OutputIntervalMs  float64
	ToolExecution     string // "parallel" or "sequential"
	Retry             JSON
	Compaction        JSON
	StreamOptions     JSON
	Raw               JSON
}

// Agent is a resolved agent (spec §7.1): model, thinking level, tools, sections.
type Agent struct {
	Provider, ModelID string
	ThinkingLevel     string
	Raw               JSON
}

// JSON is a value of package jv (nil, bool, float64, string, []any or *jv.Object; ECMAScript property order), the
// session lane's ordered JSON model at durable/core/jv.
type JSON = any
