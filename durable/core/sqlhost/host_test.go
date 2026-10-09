// SPDX-License-Identifier: MIT

package sqlhost_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
	"github.com/MichaelKinsy/PiG/durable/core/probe"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

const openJSON = `{"settings":{},"agent":{"provider":"probe","modelId":"probe-1"},"models":[],"registry":[],"sidecar":"index","delta":false}`

type rig struct {
	t        *testing.T
	host     *sqlhost.Host
	db       *sqlhost.DB
	mu       sync.Mutex
	log      []string
	calls    map[uint32]*sqlhost.Call
	ctxs     map[uint32]context.Context
	notices  []abi.Notice
	rejected []string
	fatal    chan error
	release  map[uint32]chan struct{}
	path     string
	noTool   bool
	reader   *sql.DB
	// slowCommits delays every commit, so an effect that starts early observes a store that has not applied them.
	slowCommits atomic.Bool
}

func newRig(t *testing.T, path string, handlerErr func(kind abi.EffectKind) error) *rig {
	t.Helper()
	return newRigWith(t, path, handlerErr, false)
}

func newRigWith(t *testing.T, path string, handlerErr func(kind abi.EffectKind) error, noTool bool) *rig {
	t.Helper()
	db, err := sqlhost.OpenDB(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	r := &rig{reader: reader, t: t, db: db, path: path, noTool: noTool, calls: map[uint32]*sqlhost.Call{}, ctxs: map[uint32]context.Context{}, fatal: make(chan error, 1), release: map[uint32]chan struct{}{}}
	// The effect lives until the host cancels it or the test releases it.
	handler := func(name string) sqlhost.Handler {
		return func(ctx context.Context, call *sqlhost.Call) error {
			// An independent connection sees only what is committed, at the instant the effect starts.
			var n int
			if err := r.reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM probe_log").Scan(&n); err != nil {
				return err
			}
			r.mu.Lock()
			r.log = append(r.log, fmt.Sprintf("%s#%d log=%d", name, call.ID, n))
			r.calls[call.ID] = call
			r.ctxs[call.ID] = ctx
			ch := make(chan struct{})
			r.release[call.ID] = ch
			r.mu.Unlock()
			if handlerErr != nil {
				if err := handlerErr(call.Kind); err != nil {
					return err
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ch:
				return nil
			}
		}
	}
	core := probe.NewSession(func() string { return "01980000-0000-7000-8000-000000000000" })
	handlers := map[abi.EffectKind]sqlhost.Handler{abi.EffectModelContext: handler("model"), abi.EffectTool: handler("tool")}
	if noTool {
		delete(handlers, abi.EffectTool)
	}
	r.host = sqlhost.New(sqlhost.Options{
		Core: core, SQL: probe.SQL, DB: db, OwnDB: true,
		Clock:    func() float64 { return 5000 },
		Handlers: handlers,
		OnNotice: func(n abi.Notice) { r.mu.Lock(); r.notices = append(r.notices, n); r.mu.Unlock() },
		OnCommit: func(c abi.Commit, _ []string) {
			if r.slowCommits.Load() {
				time.Sleep(50 * time.Millisecond)
			}
			r.mu.Lock()
			r.log = append(r.log, fmt.Sprintf("commit %d", c.Seq))
			r.mu.Unlock()
		},
		OnFatal: func(err error) { r.fatal <- err },
		OnRejected: func(kind abi.EventKind, err *sqlhost.RejectedError) {
			r.mu.Lock()
			r.rejected = append(r.rejected, fmt.Sprintf("%d:%s", kind, err.Name))
			r.mu.Unlock()
		},
	})
	t.Cleanup(func() { _ = r.host.Close(context.Background()) })
	return r
}

func (r *rig) send(kind abi.EventKind, payload string) error {
	return r.host.Send(r.t.Context(), abi.Event{Kind: kind, Payload: []byte(payload)})
}

func (r *rig) open() {
	r.t.Helper()
	if err := r.send(abi.EventOpen, openJSON); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) logs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.log)
}

func (r *rig) waitCall(id uint32) *sqlhost.Call {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		c := r.calls[id]
		r.mu.Unlock()
		if c != nil {
			return c
		}
		time.Sleep(time.Millisecond)
	}
	r.t.Fatalf("effect %d never started", id)
	return nil
}

func (r *rig) logRows() []string {
	rows, err := r.host.Query(r.t.Context(), "SELECT n, note FROM probe_log ORDER BY n")
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, row := range rows {
		out = append(out, fmt.Sprintf("%d:%s", row[0].Int, row[1].Bytes))
	}
	return out
}

func settled(requestID string) func(abi.Notice) bool {
	return func(n abi.Notice) bool {
		if n.Kind != abi.NoticePublished {
			return false
		}
		var p payload.Published
		if json.Unmarshal(n.Payload, &p) != nil {
			return false
		}
		for _, s := range p.Submissions {
			if s.RequestID == requestID && s.Status != "queued" && s.Status != "placed" {
				return true
			}
		}
		return false
	}
}

func TestOpenAnswersReadsThenCreatesTables(t *testing.T) {
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), nil)
	r.open()
	rows, err := r.host.Query(t.Context(), "SELECT next_seq FROM durable_metadata")
	if err != nil || len(rows) != 1 || rows[0][0].Int != 1 {
		t.Fatalf("metadata after open: %v %v", rows, err)
	}
	c := r.host.Counters()
	if c.Reads != 1 || c.Commits != 1 || c.Events < 2 {
		t.Fatalf("open must run one read round and one commit: %+v", c)
	}
	if len(r.notices) != 1 || r.notices[0].Kind != abi.NoticeReport {
		t.Fatalf("notices: %v", r.notices)
	}
}

func TestReopenReadsExistingStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sqlite")
	a := newRig(t, path, nil)
	a.open()
	if err := a.send(abi.EventSubmit, `{"type":"input","content":"hello","requestId":"r1"}`); err != nil {
		t.Fatal(err)
	}
	if err := a.host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	b := newRig(t, path, nil)
	b.open()
	var last abi.Notice
	b.mu.Lock()
	last = b.notices[len(b.notices)-1]
	b.mu.Unlock()
	if !strings.Contains(string(last.Payload), `"created":false`) || !strings.Contains(string(last.Payload), `"nextSeq":3`) {
		t.Fatalf("reopen did not read the stored sequence: %s", last.Payload)
	}
	if got := b.logRows(); !slices.Equal(got, []string{"1:submit:r1", "2:placed:r1"}) {
		t.Fatalf("rows: %v", got)
	}
}

func TestCommitsApplyInOrderBeforeAnyEffect(t *testing.T) {
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), nil)
	r.open()
	r.slowCommits.Store(true)
	before := len(r.logs())
	if err := r.send(abi.EventSubmit, `{"type":"input","content":"hello","requestId":"r1"}`); err != nil {
		t.Fatal(err)
	}
	r.waitCall(2)
	got := r.logs()[before:]
	want := []string{"commit 1", "commit 2", "model#2 log=2"}
	if !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
}

func TestSettlementNoticeAfterCompletions(t *testing.T) {
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), nil)
	r.open()
	waitCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type res struct {
		n   abi.Notice
		err error
	}
	got := make(chan res, 1)
	go func() { n, err := r.host.WaitNotice(waitCtx, settled("r1")); got <- res{n, err} }()
	time.Sleep(10 * time.Millisecond)
	if err := r.send(abi.EventSubmit, `{"type":"input","content":"hello","requestId":"r1"}`); err != nil {
		t.Fatal(err)
	}
	model := r.waitCall(2)
	model.Post(abi.Event{Kind: abi.EventModelEvent, ID: model.ID, Payload: []byte(`[{"type":"start"},{"type":"done","message":{}}]`)})
	tool := r.waitCall(4)
	if tool.Kind != abi.EffectTool {
		t.Fatalf("effect 4 is %d", tool.Kind)
	}
	tool.Post(abi.Event{Kind: abi.EventToolDone, ID: tool.ID, Phase: abi.OutcomeResult, Payload: []byte(`{"content":[]}`)})
	select {
	case v := <-got:
		if v.err != nil {
			t.Fatal(v.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no settlement")
	}
	if got := len(r.logRows()); got != 4 {
		t.Fatalf("%d log rows", got)
	}
}

func TestRejectedStepChangesNothingAndHostStaysAlive(t *testing.T) {
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), nil)
	r.open()
	err := r.send(abi.EventSubmit, `{"type":"input","content":"reject","requestId":"r1"}`)
	var rej *sqlhost.RejectedError
	if !errors.As(err, &rej) || rej.Name != "InvalidSubmission" {
		t.Fatalf("err = %v", err)
	}
	if len(r.logRows()) != 0 || r.host.Err() != nil {
		t.Fatal("a rejected step changed state or discarded the host")
	}
	if err := r.send(abi.EventSubmit, `{"type":"input","content":"ok","requestId":"r2"}`); err != nil {
		t.Fatal(err)
	}
}

func TestPostedRejectionGoesToOnRejected(t *testing.T) {
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), nil)
	r.open()
	r.host.Post(abi.Event{Kind: abi.EventInspect, Payload: []byte(`{"query":"nope"}`)})
	if _, err := r.host.Query(t.Context(), "SELECT 1"); err != nil { // a later owner job orders after the post
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.rejected) != 1 || !strings.HasSuffix(r.rejected[0], ":InvalidInspect") {
		t.Fatalf("rejected = %v", r.rejected)
	}
}

func TestFatalStepDiscardsHandleCancelsEffectsDropsLateCompletions(t *testing.T) {
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), nil)
	r.open()
	if err := r.send(abi.EventSubmit, `{"type":"input","content":"multi","requestId":"r1"}`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint32{2, 3, 4} {
		r.waitCall(id)
	}
	err := r.send(abi.EventSubmit, `{"type":"input","content":"fatal","requestId":"r2"}`)
	var fe *sqlhost.FatalError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-r.fatal:
	case <-time.After(5 * time.Second):
		t.Fatal("OnFatal not called")
	}
	for _, id := range []uint32{2, 3, 4} {
		select {
		case <-r.ctxs[id].Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("effect %d was not canceled", id)
		}
	}
	// A late completion is dropped and a later Send fails with the same reason.
	events := r.host.Counters().Events
	r.calls[2].Post(abi.Event{Kind: abi.EventModelEvent, ID: 2, Payload: []byte(`[{"type":"done"}]`)})
	if err := r.send(abi.EventSubmit, `{"type":"input","content":"x","requestId":"r3"}`); !errors.As(err, &fe) {
		t.Fatalf("send after discard: %v", err)
	}
	if got := r.host.Counters().Events; got != events {
		t.Fatalf("the core received %d events after the handle was discarded", got-events)
	}
	if _, err := r.host.WaitNotice(t.Context(), func(abi.Notice) bool { return true }); !errors.As(err, &fe) {
		t.Fatalf("WaitNotice after discard: %v", err)
	}
}

func TestSingleWriterGuardIsFatalAndRollsBack(t *testing.T) {
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), nil)
	r.open()
	err := r.send(abi.EventSubmit, `{"type":"input","content":"stale","requestId":"r1"}`)
	var fe *sqlhost.FatalError
	if !errors.As(err, &fe) || !strings.Contains(err.Error(), "single-writer guard") {
		t.Fatalf("err = %v", err)
	}
	if err := r.host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The failed commit rolled back: the log row the first statement inserted is gone and next_seq is unchanged.
	again := newRig(t, r.path, nil)
	again.open()
	if rows := again.logRows(); len(rows) != 0 {
		t.Fatalf("a failed commit left rows behind: %v", rows)
	}
	if rows, _ := again.host.Query(t.Context(), "SELECT next_seq FROM durable_metadata"); rows[0][0].Int != 1 {
		t.Fatalf("next_seq = %v", rows)
	}
}

func TestAbortCancelsEffectsTheCoreNamed(t *testing.T) {
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), nil)
	r.open()
	if err := r.send(abi.EventSubmit, `{"type":"input","content":"multi","requestId":"r1"}`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint32{2, 3, 4} {
		r.waitCall(id)
	}
	if err := r.send(abi.EventAbort, `{"conversationId":1}`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint32{2, 3, 4} {
		select {
		case <-r.ctxs[id].Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("effect %d was not canceled by the core's cancel effect", id)
		}
	}
	if r.host.Err() != nil {
		t.Fatalf("abort must not discard the host: %v", r.host.Err())
	}
}

func TestTimerEffectFiresATimerEvent(t *testing.T) {
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), nil)
	r.open()
	// The probe arms a durable timer 100 ms after the submit; the rig clock is fixed, so it fires at once.
	if err := r.send(abi.EventSubmit, `{"type":"input","content":"hello","requestId":"r1"}`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := 0
		for _, no := range r.notices {
			if no.Kind == abi.NoticeReport && strings.Contains(string(no.Payload), `"timer":1`) {
				n++
			}
		}
		r.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the timer event never reached the core")
}

func TestHandlerErrorDiscardsHandle(t *testing.T) {
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), func(abi.EffectKind) error { return errors.New("boom") })
	r.open()
	if err := r.send(abi.EventSubmit, `{"type":"input","content":"hello","requestId":"r1"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-r.fatal:
		if !strings.Contains(err.Error(), "boom") {
			t.Fatalf("fatal = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a handler error did not discard the host")
	}
}

func TestMissingHandlerIsFatal(t *testing.T) {
	r := newRigWith(t, filepath.Join(t.TempDir(), "s.sqlite"), nil, true)
	r.open()
	if err := r.send(abi.EventSubmit, `{"type":"input","content":"hello","requestId":"r1"}`); err != nil {
		t.Fatal(err)
	}
	model := r.waitCall(2)
	model.Post(abi.Event{Kind: abi.EventModelEvent, ID: 2, Payload: []byte(`[{"type":"done"}]`)})
	select {
	case err := <-r.fatal:
		if !strings.Contains(err.Error(), "no handler for effect kind") {
			t.Fatalf("fatal = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an effect with no handler did not discard the host")
	}
}

func TestCloseWaitsForEffectsAndLeaksNothing(t *testing.T) {
	base := runtime.NumGoroutine()
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), nil)
	r.open()
	if err := r.send(abi.EventSubmit, `{"type":"input","content":"multi","requestId":"r1"}`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint32{2, 3, 4} {
		r.waitCall(id)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := r.host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint32{2, 3, 4} {
		select {
		case <-r.ctxs[id].Done():
		default:
			t.Fatalf("effect %d still running after Close", id)
		}
	}
	if err := r.send(abi.EventSubmit, `{"type":"input","content":"x","requestId":"r2"}`); !errors.Is(err, sqlhost.ErrClosed) {
		t.Fatalf("send after close: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > base+1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base+1 {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines %d > %d after Close\n%s", n, base, buf[:runtime.Stack(buf, true)])
	}
}

func TestConcurrentPostsAndSendsKeepOrderPerSender(t *testing.T) {
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), nil)
	r.open()
	const senders, each = 16, 50
	var wg sync.WaitGroup
	for s := range senders {
		wg.Go(func() {
			for i := range each {
				payload := fmt.Sprintf(`{"query":"uuid","requestId":%d}`, s*1000+i)
				if i%2 == 0 {
					r.host.Post(abi.Event{Kind: abi.EventInspect, Payload: []byte(payload)})
				} else if err := r.host.Send(t.Context(), abi.Event{Kind: abi.EventInspect, Payload: []byte(payload)}); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	if _, err := r.host.Query(t.Context(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	perSender := map[int][]int{}
	for _, n := range r.notices {
		if n.Kind != abi.NoticeAPIResult {
			continue
		}
		id := int(abi.NewPayload(n.Payload).U32())
		perSender[id/1000] = append(perSender[id/1000], id%1000)
	}
	for s := range senders {
		if len(perSender[s]) != each || !slices.IsSorted(perSender[s]) {
			t.Fatalf("sender %d: %d notices, ordered=%v", s, len(perSender[s]), slices.IsSorted(perSender[s]))
		}
	}
}

// An Expectation catches a notice that arrives before Wait is called, and Cancel stops it from catching one.
func TestExpectCatchesANoticeThatArrivesBeforeWait(t *testing.T) {
	r := newRig(t, filepath.Join(t.TempDir(), "s.sqlite"), nil)
	r.open()
	caught, err := r.host.Expect(settled("r1"))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := r.host.Expect(settled("r1"))
	if err != nil {
		t.Fatal(err)
	}
	cancelled.Cancel()
	if err := r.send(abi.EventSubmit, `{"type":"input","content":"hello","requestId":"r1"}`); err != nil {
		t.Fatal(err)
	}
	model := r.waitCall(2)
	model.Post(abi.Event{Kind: abi.EventModelEvent, ID: model.ID, Payload: []byte(`[{"type":"start"},{"type":"done","message":{}}]`)})
	tool := r.waitCall(4)
	tool.Post(abi.Event{Kind: abi.EventToolDone, ID: tool.ID, Phase: abi.OutcomeResult, Payload: []byte(`{"content":[]}`)})
	waitCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := caught.Wait(waitCtx); err != nil {
		t.Fatalf("the registered expectation missed the notice: %v", err)
	}
	short, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stop()
	if _, err := cancelled.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a cancelled expectation must not resolve: %v", err)
	}
}
