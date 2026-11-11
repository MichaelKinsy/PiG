// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"encoding/binary"
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/inbox"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
	"github.com/MichaelKinsy/PiG/durable/core/store"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// ErrNotImplemented is returned for an event the handler does not serve yet; the core turns it into a rejected step.
// Read-only: assigned once at initialization.
var ErrNotImplemented = errors.New("session: not implemented")

// Rejection is an error the host sees as a rejected step: Pi's error name and message (ABI section 4.4).
type Rejection struct{ Name, Message string }

func (r *Rejection) Error() string { return r.Name + ": " + r.Message }

// reject is a rejection with Pi's error name and message: one allocation site for the rejections that name a kind of their own (the
// RangeError of a window, a TypeError of a payload), where a literal at each would be one site apiece.
//
//go:noinline
func reject(name, message string) *Rejection { return &Rejection{name, message} }

// Text is the message of the thrown host error (Error.message).
func (r *Rejection) Text() string { return r.Message }

// Handler applies one ABI event (ABI section 5) to the Session kernel, emitting commits, reads, notices and effects into
// the builder it was made with. A store.ErrPending result means the event needs rows; the core re-delivers it after them.
type Handler interface {
	Handle(ev *abi.Event) error
}

// handler is the event handler of one handle: cold open into the Session kernel over the store adapter, the extension
// transaction channel (ABI section 8) and the committed-state api reads (section 8.1).
type handler struct {
	// payloadFree and payloadUsed recycle the large effect payloads (a model request is the whole transcript, megabytes): the
	// Builder keeps a payload only until the step is encoded, so the next step reuses the buffers instead of allocating new ones.
	payloadFree, payloadUsed [][]byte
	// telPayload is the phaseSpan hook's payload being handed to hookEffect (telemetry.go), which copies it at once.
	telPayload jv.Raw

	b *abi.Builder
	// sent is the documents whose value the host holds (tx.go sentValues).
	sent idSet
	// closeErr is the error of the store of held changes at close, which close() rejects with.
	closeErr error
	// closeStage is 1 once the close job wrote the stopped tasks back (closeJob).
	closeStage uint8
	// refs counts what holds each origin of a host call; pinned are the holds a parked Loop's queue has taken; bareOrigin is the
	// current origin of a core without a Loop (origin.go).
	refs       originRefs
	pinned     []uint32
	bareOrigin uint32
	// joinNext marks the next commit of the writer's Storage call as joined to the one before it in the call (adapter.Commit); adapter.BeginCall clears it.
	joinNext bool
	// endingTx is the host transaction whose callback ended: its job's duration is Pi's less the passes the host's callback spent (elapsed).
	endingTx *hostTx
	// hopArena holds the payloads of the hops effects of the step being built; StepStart reuses it once the step was encoded.
	hopArena []byte
	// invocations maps a host-run task's running invocation to the ID of its latest phase effect: the host keeps one
	// Runtime for the invocation's phases and ends it when the core cancels that effect after the invocation ended.
	invocations map[invocationKey]uint32
	st          *store.Store
	uuid        func() string
	reader      Reader // the host's synchronous read; nil loads every record at open
	now         float64

	// changes makes every published notice carry the commit's complete publication (Session.observeCommits).
	changes bool
	opened  bool
	mode    string // "harness" or "session"
	// cacheSent and cacheSizes are the last document cache sizes published.
	// stepNo numbers the steps the handler ran; hops chains carry the number of the step that opened them.
	stepNo     uint64
	cacheSent  bool
	cacheSizes [7]int
	indexSent  bool
	indexSizes [8]int
	open       *jv.Object
	mir        *mirror
	sess       *session.Session

	txs   map[uint32]*hostTx
	loop  *session.Loop      // the Session line's job queue
	sched *session.Scheduler // Harness mode: the task scheduler
	snap  *registrySnapshot
	// snapAsk is the state of the step job's registry snapshot request (freshSnapshot); resumeWait reports that the
	// answer came and the parked job runs again.
	snapAsk    uint8
	snapErr    error
	resumeWait bool
	timers     []armedTimer // armed timers, by timer effect id
	// runtimes are the runtimes of the custom task invocations running in the host, by task.
	runtimes map[session.ID]*session.Runtime
	taskKeys map[string]taskKey
	// registry is the host's latest registry snapshot JSON; builtins are the built-in task implementations, created once
	// so their identity survives registry events.
	registry *jv.Object
	agents   map[session.ID]*turn.Agent // the last agent resolved for each conversation (tool handler and replay lookup)
	graph    *taskGraph                 // the task graph mount while a graph watch is open
	builtins []*session.TaskImpl

	watches map[uint32]*docWatch
	views   map[uint32]*viewWatch

	effects     map[uint32]*pending // started effects by ID (effects.go)
	heldDone    session.HeldAdopter // the writer's call parked on the host's adoption of a held commit (gate.go)
	heldPending pending             // its effect's continuation
	hops        []parkedHops        // the work parked on each open hops chain (hops.go)
	q           *hopcost.Queue      // the gate profiles' capsql statement queue (capsql.go); nil in every other profile
	vnow        int                 // the virtual tick of the last hops chain that ended (capsql.go)
	qInit       bool                // q was decided
	timed       bool                // the profile's Storage runs statements through a serial queue (capsql.go): vnow follows the hops chains
	busy        hopcost.Busy        // the transaction of the Storage call in flight holds that queue until its COMMIT (storeTicks)
	scans       int                 // the off-line context scans in flight, which a line job's statements queue behind (capsql.go)
	job         jobStats            // what the running line job read and wrote
	closed      bool                // the Session finished closing (finishClose)
	gate        map[string]bool     // the Pi Storage methods the host's Storage overrides (gate.go)
	inTxOp      bool                // a host transaction operation runs: it reads the host's Storage itself (tx.go)
	shadow      shadowReads         // the host Storage reads the running job or request issued (ctxread.go)
	call        commitCall          // the Storage call waiting for the host's Storage.commit (gate.go)
	// publishFn is the publication of the part the host's Storage.publishing scope waits to run (gate.go).
	publishFn func()
	gating    gateReads // the host Storage reads of the running line job (ctxread.go)
	// finishing is set while a host transaction's end settles it (txEnd): its Storage reads wait like a line job's, and the end is run
	// again (finishAgain) from finishTx and finishState once the host's Storage answered.
	finishing   bool
	finishTx    *hostTx
	finishState *jv.Object

	// cancels are the signals of the submits and aborts whose context can abort, by request, until they are answered (bound).
	cancels     map[uint32]*session.Cancel
	lateAnswers []lateAnswer // the answers deferAnswer queued as microtasks that have not run (submit.go)
	// openDone, openErr and openWaiters are how the scheduler's open ended and the "opened" requests waiting for it.
	openDone    bool
	openErr     error
	openWaiters []uint32

	hist *history.Store // transcripts for context derivation, loaded from the mirror on first use

	mintNext      session.ID // storage mode: the next ID mintId returns
	storageClosed bool
	// opening is the scheduler's open job running: its statements are not held for a COMMIT hop.
	opening bool
	// closing is a close waiting for the parked hops chains to run (closeWhenQuiet).
	closing bool
}

// newHandler builds the event handler over the Session kernel (durable/core/session) for one handle.
func newHandler(b *abi.Builder, st *store.Store, uuid func() string) Handler {
	h := &handler{b: b, st: st, uuid: uuid, txs: map[uint32]*hostTx{}}
	b.Origin = h.origin
	return h
}

// Handle applies ev, holding its origin for the length of the step (origin.go).
func (h *handler) Handle(ev *abi.Event) error {
	h.holdOrigin(ev.Origin)
	err := h.handleEvent(ev)
	h.dropOrigin(ev.Origin)
	return err
}

func (h *handler) handleEvent(ev *abi.Event) error {
	h.stepNo++
	if err := h.handle(ev); err != nil {
		h.dropStepHops()
		return h.readFailed(err)
	}
	err := h.pump()
	if err != nil {
		h.dropStepHops()
	}
	// What is left to do belongs to no host call.
	h.setOrigin(0)
	h.publishDocCache()
	return h.readFailed(err)
}

// readFailed makes a failed storage read of the mirror (mirror_lazy.go) the step's error: the handle can no longer answer
// from storage, so the step is fatal whatever the read's caller made of it.
func (h *handler) readFailed(err error) error {
	if h.mir != nil && h.mir.lazy.err != nil {
		return errors.Join(errors.New("core: a storage read failed"), h.mir.lazy.err)
	}
	if h.hist != nil && h.hist.Err() != nil {
		return errors.Join(errors.New("core: a storage read failed"), h.hist.Err())
	}
	return err
}

//go:noinline
func (h *handler) handle(ev *abi.Event) error {
	h.now = ev.Now
	h.setOrigin(ev.Origin)
	if ev.Kind == abi.EventOpen {
		return h.onOpen(ev)
	}
	if !h.opened {
		return &Rejection{"Error", "the handle is not open"}
	}
	switch ev.Kind {
	case abi.EventSeal:
		// Harness.close() starts: Session.close's synchronous part (admission closes, the scheduler seals).
		h.sess.Seal()
		return nil
	case abi.EventClose:
		// Pi's close enqueues the Session's close behind the line (session.ts close): admitted jobs, the commits in the writer and their
		// publications settle first. The api_result of request 0 reports the close.
		// Close writes the tasks it stopped back as pending, on the line after the commits admitted before the seal (session.ts
		// writeAtClose, read when its job runs), then the held changes of deferred commits; a part still in the writer holds the job,
		// which runs again once it is published.
		if h.loop.Busy() || h.sess.Writer().Busy() || (h.sched != nil && len(h.sched.CloseWrites()) > 0) || (h.sess.HeldDocuments() > 0 && h.sess.Failure() == nil) {
			h.loop.LineMaintain(h.closeJob)
		} else {
			h.closeWhenQuiet()
		}
		return nil
	case abi.EventTx:
		return h.onTx(ev)
	case abi.EventModelEvent, abi.EventModelBytes, abi.EventToolProgress, abi.EventHookDone, abi.EventToolDone, abi.EventPhaseDone:
		return h.onCompletion(ev)
	case abi.EventSubmit:
		return h.onSubmit(ev)
	case abi.EventAbort:
		return h.onAbort(ev)
	case abi.EventAPI:
		return h.onAPI(ev)
	case abi.EventInspect:
		return h.onInspect(ev)
	case abi.EventResume:
		if h.sched != nil {
			h.sched.Resume()
		}
		return nil
	case abi.EventRegistry:
		return h.onRegistry(ev)
	case abi.EventTimer:
		return h.onTimer(ev)
	case abi.EventHopped:
		return h.onHopped(ev)
	}
	return ErrNotImplemented
}

// closeJob is the close's line job: the write-back of the tasks it stopped, then the held changes of deferred commits, which Pi
// stores last, after the writes at close and while the trackers are still here (session.ts close, #storeHolds), then the writer's
// last publication. Each is a Storage commit through the writer: a *NeedPublication runs the job again from the start once the part
// is published, so closeStage remembers what was done. A failed Session writes nothing; a failing store fails it and still ends the
// close, which then rejects with that error.
func (h *handler) closeJob() error {
	if h.closeStage == 0 {
		if h.sched != nil && h.sess.Failure() == nil {
			if err := h.sched.WriteBack(h.sched.CloseWrites()); err != nil && session.IsNeedPublication(err) {
				return err
			}
		}
		h.closeStage = 1
	}
	// A store the job's earlier run admitted: it failed the Session while the job waited for it (#storeHolds rejects).
	if err, ok := h.sess.HoldsOutcome(); ok {
		h.closeErr = err
	} else if h.sess.HeldDocuments() > 0 && h.sess.Failure() == nil {
		err := h.sess.StoreHolds()
		if session.IsNeedPublication(err) {
			return err
		}
		if err != nil {
			h.closeErr = err
		}
	}
	if err := h.sess.Touch(session.AllKeys); err != nil {
		return err
	}
	h.closeWhenQuiet()
	return nil
}

// closeWhenQuiet closes once every hops chain the core parked has run: a line job's statements (and its completion) wait on one
// (hops.go holdJob), and Pi's close resolves after the line's jobs have, statements included. onHopped completes a close that waited.
func (h *handler) closeWhenQuiet() {
	if len(h.hops) > 0 {
		h.closing = true
		return
	}
	h.finishClose()
}

// finishClose closes the Session and reports it to the host: the api_result of request 0.
func (h *handler) finishClose() {
	h.sess.Close()
	h.closed = true
	if h.closeErr != nil {
		// The store of the held changes failed: close() rejects with that error, the backend closed all the same.
		h.apiError(0, h.closeErr)
		return
	}
	h.apiOK(0, nil)
}

// Closed reports whether the Session finished closing: the handle then rejects every event.
func (h *handler) Closed() bool { return h.closed }

// onOpen is cold open: the store's open reads, then the tables (store.LoadAll) into the mirror the Session reads. With the
// host's synchronous read the tasks and submissions loaded are the live ones; the mirror reads the rest on demand.
//
//go:noinline
func (h *handler) onOpen(ev *abi.Event) error {
	if h.opened {
		return &Rejection{"Error", "the handle is already open"}
	}
	if h.open == nil {
		v, err := jv.Parse(ev.Payload)
		if err != nil {
			return err
		}
		o, ok := v.(*jv.Object)
		if !ok {
			return reject("TypeError", "open: the payload is not an object")
		}
		h.open = o
		h.mode = strOf(o, "mode")
		if h.mode == "" {
			h.mode = "harness"
		}
	}
	live, err := h.st.Open(h.b)
	if err != nil {
		return err
	}
	dump, err := h.st.LoadAll(h.b, h.reader != nil)
	if err != nil {
		return err
	}
	h.mir = newMirror()
	h.mir.lazy = lazyState{read: h.reader, below: live.NextID}
	if err := h.mir.load(dump, live.NextID, live.NextSeq); err != nil {
		return err
	}
	h.st.ReleaseDump()
	h.sess = session.New(&adapter{h: h}, live.NextID, func() float64 { return h.now })
	h.sess.SetHopProfile(h.hopProfile())
	h.sess.DocumentRetention = h.documentRetentionMs
	h.sess.Microtask = h.sweepMicrotask
	h.sess.ArmTimer = h.armBackgroundTimer
	h.sess.EnqueueSweep = h.enqueueSweep
	h.sess.ArmForeground = h.armTimer
	h.sess.PersistWithin = h.persistWithinMs
	// The host admits calls itself (the native driver's Close seals ahead of events its admitted callers have not posted yet): a
	// call it admitted runs after the seal, as a call that entered Pi's line before close() does.
	h.sess.HostAdmits, _ = boolOf(h.open, "hostAdmission")
	// HarnessOptions.readOnly: the Session refuses commits with writes, and the scheduler loads live tasks as committed and runs nothing.
	h.sess.ReadOnly, _ = boolOf(h.open, "readOnly")
	h.sess.SubscribeCommits(h.publish)
	h.startLine()
	h.syncGate()
	h.opened = true
	h.mintNext = live.NextID
	if h.mode == "harness" {
		h.sess.ConversationCreated = h.conversationCreated
		return h.startScheduler()
	}
	return nil
}

// adapter is the session.Store the Session commits through: reads come from the mirror, and a commit becomes one Pi
// commit of store.Store (its statements in the step) and is then applied to the mirror.
type adapter struct{ h *handler }

func (a *adapter) Conversation(id session.ID) (*jv.Object, error) {
	a.h.readRecord(hopcost.RecordConversation, id)
	return a.h.mir.Conversation(id)
}
func (a *adapter) ScanConversations(q session.ConversationQuery, limit int, after session.ID) (session.Page, error) {
	a.h.read(hopcost.ReadRow)
	return a.h.mir.ScanConversations(q, limit, after)
}
func (a *adapter) Entry(id session.ID) (*jv.Object, session.Seq, error) {
	a.h.read(hopcost.ReadEntry)
	return a.h.mir.Entry(id)
}
func (a *adapter) EntryIn(conv, id session.ID) (*jv.Object, session.Seq, error) {
	a.h.read(hopcost.ReadEntryIn)
	return a.h.mir.EntryIn(conv, id)
}
func (a *adapter) LatestHeadMarker(conv, atOrBefore session.ID) (*jv.Object, error) {
	a.h.read(hopcost.ReadHeadMarker)
	return a.h.mir.LatestHeadMarker(conv, atOrBefore)
}
func (a *adapter) ScanEntries(q session.EntryQuery, limit int, after session.ID) (session.Page, error) {
	a.h.read(hopcost.ReadScanEntries)
	return a.h.mir.ScanEntries(q, limit, after)
}
func (a *adapter) Task(id session.ID) (*jv.Object, error) {
	hit := a.h.readRecord(hopcost.RecordTask, id)
	t, err := a.h.mir.Task(id)
	if err == nil && t != nil && hit == hopcost.CacheMiss {
		a.h.sess.KeepRecord(hopcost.RecordTask, id, t)
	}
	return t, err
}
func (a *adapter) ScanTasks(q session.TaskQuery, limit int, after session.ID) (session.Page, error) {
	a.h.read(hopcost.ReadRow)
	return a.h.mir.ScanTasks(q, limit, after)
}
func (a *adapter) Submission(id session.ID) (*jv.Object, error) {
	a.h.readRecord(hopcost.RecordSubmission, id)
	return a.h.mir.Submission(id)
}
func (a *adapter) ScanSubmissions(q session.SubmissionQuery, limit int, after session.ID) (session.Page, error) {
	a.h.read(hopcost.ReadRow)
	return a.h.mir.ScanSubmissions(q, limit, after)
}
func (a *adapter) SubmissionByRequest(conv session.ID, request string) (*jv.Object, error) {
	a.h.read(hopcost.ReadRow)
	return a.h.mir.SubmissionByRequest(conv, request)
}
func (a *adapter) FindDocument(addr session.Address, at session.Point) (*jv.Object, error) {
	if err := a.h.gateRead("findDocument", addressValue(addr), pointJSON(at)); err != nil {
		return nil, err
	}
	a.h.read(hopcost.ReadRow)
	return a.h.mir.FindDocument(addr, at)
}
func (a *adapter) Document(id session.ID, at session.Point) (*session.StoredDoc, error) {
	if err := a.h.gateRead("document", float64(id), pointJSON(at)); err != nil {
		return nil, err
	}
	a.h.read(hopcost.ReadDocument)
	return a.h.mir.Document(id, at)
}
func (a *adapter) ScanDocuments(scope session.Scope, at session.Point, kind string, limit int, after session.ID) (session.Page, error) {
	a.h.read(hopcost.ReadRow)
	return a.h.mir.ScanDocuments(scope, at, kind, limit, after)
}

// Commit is the Store's per-batch primitive of the writer's Storage call (Session.commitBatches): it emits the writes as one Pi
// commit, joined to the call's earlier batches, and applies them to the mirror. A batch Durable's own checks refuse is a
// *session.RefusedError and changes nothing; any other failure is an unknown outcome, which fails the Session.
func (a *adapter) Commit(writes []session.Write, nextID session.ID) (session.Seq, error) {
	seq, err := a.store(writes, nextID, a.h.joinNext)
	a.h.joinNext = err == nil
	return seq, err
}

// BeginCall is the start of a Storage call (session.callStarter): its first commit is not joined to the commit before it in the step,
// which belongs to another call.
func (a *adapter) BeginCall() { a.h.joinNext = false }

// store is one batch of a Storage call: checked against the state the earlier batches left, emitted as one commit (joined: the
// same Storage call as the commit before it in the step, ABI section 4.4) and applied to the mirror.
func (a *adapter) store(writes []session.Write, nextID session.ID, joined bool) (session.Seq, error) {
	m := a.h.mir
	if err := m.checkCopies(writes); err != nil {
		return 0, refusal(err)
	}
	resolved := m.resolveCopies(writes)
	// The mirror holds every record ID (Pi's record_ids), so it validates the batch before the store emits it.
	if err := m.checkGlobalIDs(resolved); err != nil {
		return 0, refusal(err)
	}
	actions, order, err := m.prepareDocActions(resolved)
	if err != nil {
		return 0, refusal(err)
	}
	if err := m.checkDocActions(actions, order); err != nil {
		return 0, refusal(err)
	}
	tx := a.h.st.Begin()
	// scratch is the array stageWrite encodes each record text in before it copies the text to its own length. It lives for
	// this commit only: kept on the handler, the largest record of the session (an entry of kilobytes) stayed live for good.
	var scratch []byte
	for i := range resolved {
		if err := stageWrite(tx, &resolved[i], &scratch); err != nil {
			return 0, err
		}
	}
	a.h.st.ObserveNextID(nextID)
	seq, err := a.h.st.Commit(a.h.b, tx)
	if err != nil {
		return 0, err
	}
	if joined {
		a.h.b.JoinLast()
	}
	mseq, err := m.Commit(writes, nextID, tx)
	if err != nil {
		return 0, err
	}
	if mseq != seq {
		return 0, errors.New("core: the mirror and the store disagree on the commit sequence")
	}
	if err := a.h.observeCommit(writes, seq); err != nil {
		return 0, err
	}
	return seq, nil
}

// refusal is the error of a batch Durable's own checks refuse (session.RefusedError): one allocation site for the checks of store.
//
//go:noinline
func refusal(err error) error { return &session.RefusedError{Err: err} }

// errorName is the name Pi gives an error the Session returns.
func errorName(err error) string {
	var rej *Rejection
	var thrown *ThrownError
	switch {
	// First: a SessionFailed carries the Storage error that failed the Session as its cause, which may be a thrown error.
	case errors.Is(err, session.ErrSessionFailed):
		return "SessionFailed"
	case errors.Is(err, session.ErrReadOnlySession):
		return "ReadOnlySession"
	case errors.As(err, &rej):
		return rej.Name
	case errors.As(err, &thrown):
		return thrown.Name
	case errors.Is(err, session.ErrReadAfterWrite):
		return "ReadAfterWrite"
	case errors.Is(err, session.ErrStorageRequest), errors.As(err, new(*store.RequestError)):
		return "StorageRequestError"
	case errors.Is(err, inbox.ErrConversationBusy):
		return "ConversationBusy"
	case errors.Is(err, inbox.ErrConversationIdle):
		return "ConversationIdle"
	}
	var te *typeError
	if errors.As(err, &te) || errors.Is(err, session.ErrTypeError) {
		return "TypeError"
	}
	return "Error"
}

// errorJSON is the wire form of an error: {name, message} and, for ConversationBusy, the structured conversationId
// (the TS host's reviveError reads body.conversationId first; the message is the fallback).
func errorJSON(err error) []byte {
	out := abi.ErrorJSON(errorName(err), errorMessage(err))
	if busy, ok := errors.AsType[*inbox.BusyError](err); ok {
		out = append(out[:len(out)-1], `,"conversationId":`...)
		out = jv.Append(out, float64(busy.ConversationID))
		out = append(out, '}')
	}
	if idle, ok := errors.AsType[*inbox.IdleError](err); ok {
		out = append(out[:len(out)-1], `,"conversationId":`...)
		out = jv.Append(out, float64(idle.ConversationID))
		out = append(out, '}')
	}
	// An AggregateError carries its errors (the afterRun hook's rejected task requests).
	if thrown, ok := errors.AsType[*ThrownError](err); ok && len(thrown.Errors) > 0 {
		out = append(out[:len(out)-1], `,"errors":[`...)
		for i, inner := range thrown.Errors {
			if i > 0 {
				out = append(out, ',')
			}
			out = append(out, abi.ErrorJSON(inner.Name, inner.Message)...)
		}
		out = append(out, ']', '}')
	}
	return out
}

func errorMessage(err error) string {
	if rej, ok := errors.AsType[*Rejection](err); ok {
		return rej.Message
	}
	return err.Error()
}

// typeError is a JavaScript TypeError.
type typeError struct{ msg string }

func (e *typeError) Error() string { return e.msg }

// notice emits a notice whose payload is u32 id then JSON.
func (h *handler) notice(kind abi.NoticeKind, id uint32, body []byte) {
	out := binary.LittleEndian.AppendUint32(make([]byte, 0, 4+len(body)), id)
	h.b.Notice(kind, append(out, body...))
}

// maxPayloadBuffers bounds the recycled payload buffers kept between steps, and payloadKeep the size of one: a request
// that sent a whole model context (the first of a conversation, megabytes) leaves its buffer to the collector instead of
// holding it for the life of the handle.
const (
	maxPayloadBuffers = 4
	payloadKeep       = 256 << 10
	// hopArenaSize is the one buffer the hops payloads of a step are built in; a chain that carries more than is left of it
	// gets a buffer of its own.
	hopArenaSize = 8 << 10
)

// payloadBuffer returns an empty buffer of at least size bytes for an effect payload built in this step.
func (h *handler) payloadBuffer(size int) []byte {
	if n := len(h.payloadFree); n > 0 && cap(h.payloadFree[n-1]) >= size {
		buf := h.payloadFree[n-1]
		h.payloadFree[n-1] = nil
		h.payloadFree = h.payloadFree[:n-1]
		return buf[:0]
	}
	return make([]byte, 0, max(size, 4096))
}

// keepPayload records a payload built with payloadBuffer so StepStart recycles it once its step has been encoded.
func (h *handler) keepPayload(buf []byte) { h.payloadUsed = append(h.payloadUsed, buf) }

// StepStart runs at the start of every Step: the previous step's payloads were encoded into the host's buffer, so their
// storage is free again. Without it each model request left a transcript-sized buffer for the collector, and a collector
// that frees large blocks late (TinyGo's) doubled the linear memory until it trapped.
//
//go:noinline
func (h *handler) StepStart() {
	for i, buf := range h.payloadUsed {
		if len(h.payloadFree) < maxPayloadBuffers && cap(buf) <= payloadKeep {
			h.payloadFree = append(h.payloadFree, buf)
		}
		h.payloadUsed[i] = nil
	}
	h.payloadUsed = h.payloadUsed[:0]
	h.hopArena = h.hopArena[:0]
	if h.mir != nil {
		// ABI section 3 rule 2: the host applied every commit of the previous steps before this Step, so storage has every record the
		// mirror holds.
		h.mir.releaseApplied()
		if h.hist != nil {
			h.hist.Evict()
		}
	}
}

// invocationKey names one invocation of a host-run task: its task and whether it runs the abort handler.
type invocationKey struct {
	task  session.ID
	abort bool
}
