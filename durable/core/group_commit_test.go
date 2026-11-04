// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// Group commit through the production path: the real core behind sqlhost over SQLite, with the host's Storage scripted by the test
// (ABI.md section 5.5, host/PROTOCOL.md "Storage gate"). The core's writer sends the commits admitted while no Storage call is in flight
// as one `storageCommit` hook; the scripted Storage holds, receives, refuses or rejects the call, and the tests check what the Session
// line does meanwhile (packages/durable/test/session-group-commit.test.ts, harness-group-commit.test.ts: batching, gates, failures per
// part, publication a macrotask apart, the Storage's publication scope).

// storageScript is what the scripted Storage does with one call.
type storageScript struct {
	// hold keeps the call in flight until it is closed.
	hold chan struct{}
	// refuseAt, when not nil, is the index of the batch Durable's checks refuse: the batches before it are received.
	refuseAt *int
	// reject, when set, makes the call reject after its receipts.
	reject string
}

type recordedCall struct {
	index   int
	batches int
	entered chan struct{}
}

type gcNotice struct {
	kind    abi.NoticeKind
	id      uint32
	payload []byte
}

type gcHost struct {
	t    *testing.T
	host *sqlhost.Host
	ctx  context.Context

	mu      sync.Mutex
	cond    *sync.Cond
	notices []gcNotice
	calls   []*recordedCall
	scripts map[int]*storageScript
	scopes  int // storagePublishing hooks answered
	txID    uint32
	reqID   uint32
}

func openGC(t *testing.T, gate ...string) *gcHost {
	t.Helper()
	g := &gcHost{t: t, ctx: t.Context(), scripts: map[int]*storageScript{}}
	g.cond = sync.NewCond(&g.mu)
	db, err := sqlhost.OpenDB(g.ctx, filepath.Join(t.TempDir(), "gc.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	g.host = sqlhost.New(sqlhost.Options{
		Core: core.NewSession(func() string { return "00000000-0000-7000-8000-000000000000" }), SQL: core.SQLTable(), DB: db, OwnDB: true,
		Handlers: map[abi.EffectKind]sqlhost.Handler{abi.EffectHook: g.hook},
		OnNotice: g.onNotice,
	})
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("\n" + g.dump())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		g.mu.Lock()
		for _, s := range g.scripts {
			if s.hold != nil {
				select {
				case <-s.hold:
				default:
					close(s.hold)
				}
			}
		}
		g.mu.Unlock()
		_ = g.host.Close(ctx)
	})
	gates, _ := json.Marshal(gate)
	open := `{"mode":"session","settings":{},"models":[],"registry":{"extensions":[]},"sidecar":"index","delta":true,"hostAdmission":true,"storageProfile":"memory","storageGate":` + string(gates) + `}`
	if err := g.host.Send(g.ctx, abi.Event{Kind: abi.EventOpen, Payload: []byte(open)}); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *gcHost) onNotice(n abi.Notice) {
	rec := gcNotice{kind: n.Kind}
	data := n.Data()
	switch n.Kind {
	case abi.NoticeTxResult, abi.NoticeAPIResult:
		rec.id = binary.LittleEndian.Uint32(data)
		rec.payload = append([]byte(nil), data[4:]...)
	default:
		rec.payload = append([]byte(nil), data...)
	}
	g.mu.Lock()
	g.notices = append(g.notices, rec)
	g.mu.Unlock()
	g.cond.Broadcast()
}

// script sets what the scripted Storage does with call number index (counted from the first call of the handle).
func (g *gcHost) script(index int, s *storageScript) {
	g.mu.Lock()
	g.scripts[index] = s
	g.mu.Unlock()
}

// holdNext holds the next call that has not started yet, and returns its index and the function that releases it.
func (g *gcHost) holdNext() (index int, release func()) {
	g.mu.Lock()
	index = len(g.calls)
	hold := make(chan struct{})
	g.scripts[index] = &storageScript{hold: hold}
	g.mu.Unlock()
	return index, func() { close(hold) }
}

func (g *gcHost) hook(ctx context.Context, call *sqlhost.Call) error {
	var effect struct {
		Name    string
		Payload struct{ Batches []json.RawMessage }
	}
	if err := json.Unmarshal(call.Payload, &effect); err != nil {
		return err
	}
	switch effect.Name {
	case "storagePublishing":
		// Storage.publishing(publish): the scope runs the publication and the core goes on from it.
		if err := g.sendAPI(ctx, `{"op":"publish"}`); err != nil {
			return err
		}
		g.mu.Lock()
		g.scopes++
		g.mu.Unlock()
		g.cond.Broadcast()
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: []byte("null")})
		return nil
	case "storageCommit":
	default:
		return errors.New("unexpected hook " + effect.Name)
	}
	g.mu.Lock()
	rec := &recordedCall{index: len(g.calls), batches: len(effect.Payload.Batches), entered: make(chan struct{})}
	g.calls = append(g.calls, rec)
	script := g.scripts[rec.index]
	g.mu.Unlock()
	close(rec.entered)
	g.cond.Broadcast()
	if script != nil && script.hold != nil {
		select {
		case <-script.hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	stored := rec.batches
	if script != nil && script.refuseAt != nil {
		stored = *script.refuseAt
	}
	rows, err := g.host.Query(ctx, "SELECT next_seq FROM durable_metadata")
	if err != nil {
		return err
	}
	seq := rows[0][0].Int
	for i := range stored {
		if err := g.sendAPI(ctx, `{"op":"receive","index":`+strconv.Itoa(i)+`,"seq":`+strconv.FormatInt(seq+int64(i), 10)+`}`); err != nil {
			g.t.Errorf("receipt %d of call %d: %v", i, rec.index, err)
			return err
		}
	}
	switch {
	case script != nil && script.reject != "":
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeThrown, Payload: []byte(`{"name":"Error","message":` + strconv.Quote(script.reject) + `}`)})
	case script != nil && script.refuseAt != nil:
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: []byte(`{"stored":` + strconv.Itoa(stored) + `,"refused":{"name":"Error","message":"refused"}}`)})
	default:
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: []byte(`{"stored":` + strconv.Itoa(stored) + `}`)})
	}
	return nil
}

func (g *gcHost) sendAPI(ctx context.Context, body string) error {
	g.mu.Lock()
	g.reqID++
	id := g.reqID
	g.mu.Unlock()
	return g.host.Send(ctx, abi.Event{Kind: abi.EventAPI, ID: id, Payload: []byte(body)})
}

// waitFor blocks until test holds under the lock, or fails after a timeout.
func (g *gcHost) waitFor(what string, test func() bool) {
	g.t.Helper()
	done := make(chan struct{})
	go func() {
		g.mu.Lock()
		for !test() {
			g.cond.Wait()
		}
		g.mu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		g.mu.Lock()
		g.cond.Broadcast()
		g.mu.Unlock()
		g.t.Fatalf("timed out waiting for %s\n%s", what, g.dump())
	}
}

// txResult is the nth tx_result notice of transaction id, from position from of the notices: its JSON, and the position after it.
func (g *gcHost) nextTxResult(id uint32, from int) (string, int) {
	g.t.Helper()
	var out string
	var next int
	g.waitFor("a result of transaction "+strconv.Itoa(int(id)), func() bool {
		for i := from; i < len(g.notices); i++ {
			if n := g.notices[i]; n.kind == abi.NoticeTxResult && n.id == id {
				out, next = string(n.payload), i+1
				return true
			}
		}
		return false
	})
	return out, next
}

// commit runs one host transaction: begin, the operations, end. Its result channel delivers the JSON of the end's answer (a result or a
// rejection) once the commit's outcome is delivered; the operations' answers are returned to the caller that wants them through ops.
type commitRun struct {
	id   uint32
	pos  int
	done chan string
}

func (g *gcHost) beginTx() *commitRun {
	g.t.Helper()
	g.mu.Lock()
	g.txID++
	id := g.txID
	pos := len(g.notices)
	g.mu.Unlock()
	if err := g.host.Send(g.ctx, abi.Event{Kind: abi.EventTx, ID: id, Payload: []byte(`{"op":"begin"}`)}); err != nil {
		g.t.Fatal(err)
	}
	run := &commitRun{id: id, pos: pos, done: make(chan string, 1)}
	for {
		var res string
		res, run.pos = g.nextTxResult(id, run.pos)
		if strings.Contains(res, `"ready"`) {
			return run
		}
	}
}

// beginRejected begins a transaction the Session refuses (it failed): the begin's answer is the rejection.
func (g *gcHost) beginRejected() string {
	g.t.Helper()
	g.mu.Lock()
	g.txID++
	id := g.txID
	pos := len(g.notices)
	g.mu.Unlock()
	if err := g.host.Send(g.ctx, abi.Event{Kind: abi.EventTx, ID: id, Payload: []byte(`{"op":"begin"}`)}); err != nil {
		g.t.Fatal(err)
	}
	res, _ := g.nextTxResult(id, pos)
	return res
}

// op sends one operation and returns its answer.
func (g *gcHost) op(run *commitRun, body string) string {
	g.t.Helper()
	if err := g.host.Send(g.ctx, abi.Event{Kind: abi.EventTx, ID: run.id, Payload: []byte(body)}); err != nil {
		g.t.Fatal(err)
	}
	res, pos := g.nextTxResult(run.id, run.pos)
	run.pos = pos
	return res
}

// end sends the transaction's end; the answer arrives on run.done when the commit's outcome is delivered.
func (g *gcHost) end(run *commitRun) {
	g.t.Helper()
	if err := g.host.Send(g.ctx, abi.Event{Kind: abi.EventTx, ID: run.id, Payload: []byte(`{"op":"end"}`)}); err != nil {
		g.t.Fatal(err)
	}
	go func() {
		res, _ := g.nextTxResult(run.id, run.pos)
		run.done <- res
	}()
}

func (g *gcHost) result(run *commitRun) string {
	g.t.Helper()
	select {
	case res := <-run.done:
		return res
	case <-time.After(8 * time.Second):
		g.t.Fatal("the commit's outcome never arrived")
	}
	return ""
}

// createConversation commits one ownerless conversation and returns its ID.
func (g *gcHost) createConversation() int {
	g.t.Helper()
	run := g.beginTx()
	res := g.op(run, `{"op":"createConversation","ownership":{"kind":"ownerless"}}`)
	var parsed struct{ Result struct{ ID int } }
	if err := json.Unmarshal([]byte(res), &parsed); err != nil || parsed.Result.ID == 0 {
		g.t.Fatalf("createConversation answered %s", res)
	}
	g.end(run)
	if res := g.result(run); res != `{"result":null}` {
		g.t.Fatalf("the creating commit ended with %s", res)
	}
	return parsed.Result.ID
}

// appendEntry stages one entry in conversation conv in a transaction it ends; the returned run delivers the outcome.
func (g *gcHost) appendEntry(conv int) *commitRun {
	g.t.Helper()
	run := g.beginTx()
	res := g.op(run, `{"op":"appendEntry","conversationId":`+strconv.Itoa(conv)+`,"value":{"kind":"pi.user","model":[{"role":"user","content":"x","timestamp":1}]}}`)
	if !strings.Contains(res, `"result"`) {
		g.t.Fatalf("appendEntry answered %s", res)
	}
	g.end(run)
	return run
}

// flush is Pi's `await flush()` before `held.release()` (packages/durable/test/session-group-commit.test.ts, session-support.ts: pending
// microtasks and one macrotask turn): it returns once the line has admitted every commit whose end was sent, so the call the release lets
// go sends them all. Admission is a hops chain of the host's owner after the end, and the release races it through the Storage's own
// goroutine, so a commit whose end was sent last can miss the call and join the next one, which a failed call rejects as SessionFailed. A
// job takes the line only after the job before it admitted its part (session/loop.go workReturned, LineRelease), so a transaction that has
// begun proves the commits before it are admitted. It is aborted as a callback that threw, which admits no part (loop.go End), and its
// answer comes in line order after the publications this flush precedes, so it is not waited for. The line must be free: not while a
// job waits at a gate, and not once the Session closes.
func (g *gcHost) flush() {
	g.t.Helper()
	probe := g.beginTx()
	if err := g.host.Send(g.ctx, abi.Event{Kind: abi.EventTx, ID: probe.id, Payload: []byte(`{"op":"abort","error":{"name":"Error","message":"flush"}}`)}); err != nil {
		g.t.Fatal(err)
	}
}

func (g *gcHost) publishedSeqs() []int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []int64
	for _, n := range g.notices {
		if n.kind != abi.NoticePublished {
			continue
		}
		var body struct{ Seq *int64 }
		if json.Unmarshal(n.payload, &body) == nil && body.Seq != nil {
			out = append(out, *body.Seq)
		}
	}
	return out
}

// dump is the calls and notices so far, for a failure message.
func (g *gcHost) dump() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var b strings.Builder
	if err := g.host.Err(); err != nil {
		b.WriteString("host failed: " + err.Error() + "\n")
	}
	for _, c := range g.calls {
		b.WriteString("call " + strconv.Itoa(c.index) + " batches " + strconv.Itoa(c.batches) + "\n")
	}
	for _, n := range g.notices {
		if n.kind == abi.NoticeDocCache || n.kind == abi.NoticeIndexSizes {
			continue
		}
		p := string(n.payload)
		if len(p) > 120 {
			p = p[:120]
		}
		b.WriteString("notice " + strconv.Itoa(int(n.kind)) + " id " + strconv.Itoa(int(n.id)) + " " + p + "\n")
	}
	return b.String()
}

func (g *gcHost) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.calls)
}

// callBatches is the number of batches Storage call number index carried.
func (g *gcHost) callBatches(index int) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[index].batches
}

// The commits admitted while a call is in flight leave in one call, each with its own seq, published in order; the line runs their
// callbacks meanwhile, because a Storage call holds the writer, not the line.
func TestCommitsAdmittedDuringACallLeaveInOneCall(t *testing.T) {
	g := openGC(t, "commit")
	a, b, c := g.createConversation(), g.createConversation(), g.createConversation()
	before := g.callCount()
	index, release := g.holdNext()
	first := g.appendEntry(a)
	g.waitFor("the held call", func() bool { return len(g.calls) > index })
	// Their callbacks run (their operations are answered) while the first commit is in flight.
	second := g.appendEntry(b)
	third := g.appendEntry(c)
	if pubs := len(g.publishedSeqs()); pubs != before {
		t.Fatalf("%d commits published while the call was in flight, want the %d before it", pubs, before)
	}
	g.flush()
	release()
	for i, run := range []*commitRun{first, second, third} {
		if res := g.result(run); res != `{"result":null}` {
			t.Fatalf("commit %d ended with %s", i, res)
		}
	}
	sizes := []int{}
	g.mu.Lock()
	for _, call := range g.calls[before:] {
		sizes = append(sizes, call.batches)
	}
	g.mu.Unlock()
	if len(sizes) != 2 || sizes[0] != 1 || sizes[1] != 2 {
		t.Fatalf("the calls after the setup carried %v batches, want [1 2]", sizes)
	}
	seqs := g.publishedSeqs()
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("published seqs %v are not strictly increasing", seqs)
		}
	}
}

// A job that reads a key an unpublished commit writes waits for its publication (session.ts touch, keys.ts): latestEntryId of a
// conversation an in-flight commit appends to is answered only once that commit is published.
func TestAReadOfAKeyAnUnpublishedCommitWritesWaitsForItsPublication(t *testing.T) {
	g := openGC(t, "commit")
	conv := g.createConversation()
	index, release := g.holdNext()
	writer := g.appendEntry(conv)
	g.waitFor("the held call", func() bool { return len(g.calls) > index })
	reader := g.beginTx()
	if err := g.host.Send(g.ctx, abi.Event{Kind: abi.EventTx, ID: reader.id, Payload: []byte(`{"op":"latestEntryId","conversationId":` + strconv.Itoa(conv) + `}`)}); err != nil {
		t.Fatal(err)
	}
	answered := make(chan string, 1)
	go func() {
		res, _ := g.nextTxResult(reader.id, reader.pos)
		answered <- res
	}()
	select {
	case res := <-answered:
		t.Fatalf("latestEntryId answered %s while the commit that appends to the conversation was unpublished", res)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	if res := g.result(writer); res != `{"result":null}` {
		t.Fatalf("the appending commit ended with %s", res)
	}
	select {
	case res := <-answered:
		if !strings.Contains(res, `"result"`) || strings.Contains(res, "null") {
			t.Fatalf("latestEntryId answered %s, want the entry the commit appended", res)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("latestEntryId never answered after the publication")
	}
}

// A batch Durable's checks refuse stops the call: the parts before it are received and published, it rejects with the refusal, the part
// after it and every later commit reject with SessionFailed (group-commit.md 3.1, 3.4).
func TestARefusedBatchRejectsWithTheRefusalAndFailsTheSession(t *testing.T) {
	g := openGC(t, "commit")
	a, b, c := g.createConversation(), g.createConversation(), g.createConversation()
	index, release := g.holdNext()
	hold := g.appendEntry(a)
	g.waitFor("the held call", func() bool { return len(g.calls) > index })
	refuseAt := 1
	g.script(index+1, &storageScript{refuseAt: &refuseAt})
	ok := g.appendEntry(b)
	refused := g.appendEntry(c)
	g.flush()
	release()
	if res := g.result(hold); res != `{"result":null}` {
		t.Fatalf("the held commit ended with %s", res)
	}
	if res := g.result(ok); res != `{"result":null}` {
		t.Fatalf("the batch before the refused one ended with %s, want it published and resolved", res)
	}
	res := g.result(refused)
	if !strings.Contains(res, `"rejected"`) || !strings.Contains(res, "refused") {
		t.Fatalf("the refused batch ended with %s, want its refusal", res)
	}
	if res = g.beginRejected(); !strings.Contains(res, "SessionFailed") {
		t.Fatalf("a commit after the failure began with %s, want SessionFailed", res)
	}
}

// A call that rejects after its receipts publishes the parts it received, then rejects each with its error and fails the Session
// (group-commit.md 3.4).
func TestACallThatRejectsAfterItsReceiptsPublishesThenRejects(t *testing.T) {
	g := openGC(t, "commit")
	a := g.createConversation()
	published := len(g.publishedSeqs())
	g.script(g.callCount(), &storageScript{reject: "connection lost"})
	run := g.appendEntry(a)
	res := g.result(run)
	if !strings.Contains(res, `"rejected"`) || !strings.Contains(res, "connection lost") {
		t.Fatalf("the commit of the rejected call ended with %s, want its error", res)
	}
	if got := len(g.publishedSeqs()); got != published+1 {
		t.Fatalf("%d commits published, want the received part published first (%d)", got, published+1)
	}
	if res = g.beginRejected(); !strings.Contains(res, "SessionFailed") {
		t.Fatalf("a commit after the failure began with %s, want SessionFailed", res)
	}
}

// failedCall runs the failure fan-out of one Storage call of n parts (packages/durable/src/session/writer.ts #publish, test
// session-group-commit.test.ts "failures per part"): a first commit holds call 0 in flight while n more are admitted; they leave as call
// 1, which script describes. It returns the n parts' answers, in admission order.
func failedCall(t *testing.T, n int, script *storageScript, noWriteAfter int) []string {
	t.Helper()
	g := openGC(t, "commit")
	convs := make([]int, n+1)
	for i := range convs {
		convs[i] = g.createConversation()
	}
	index, release := g.holdNext()
	hold := g.appendEntry(convs[0])
	g.waitFor("the held call", func() bool { return len(g.calls) > index })
	g.script(index+1, script)
	runs := make([]*commitRun, 0, n+1)
	for i := range n {
		runs = append(runs, g.appendEntry(convs[i+1]))
		if i == noWriteAfter {
			// A job without writes between two parts of the call: not stored, and after a stop it was not sent either.
			run := g.beginTx()
			g.end(run)
			runs = append(runs, run)
		}
	}
	g.flush()
	release()
	if res := g.result(hold); res != `{"result":null}` {
		t.Fatalf("the held commit ended with %s", res)
	}
	out := make([]string, len(runs))
	for i, run := range runs {
		out[i] = g.result(run)
	}
	if got := g.callBatches(index + 1); got != n {
		t.Fatalf("the failing call carried %d batches, want the %d parts admitted before the held call ended", got, n)
	}
	return out
}

func sessionFailed(res string) bool {
	return strings.Contains(res, `"rejected"`) && strings.Contains(res, "SessionFailed")
}

// A batch Durable's checks refuse at index k of n stops the call: the parts before k are published and resolve, k rejects with the
// refusal, and every part after k was never stored, so it rejects with SessionFailed and does not resolve (writer.ts #failed(stop.error)).
func TestThePartsAfterARefusedOneRejectWithSessionFailed(t *testing.T) {
	const n = 4
	for k := range n {
		t.Run("refused="+strconv.Itoa(k), func(t *testing.T) {
			got := failedCall(t, n, &storageScript{refuseAt: &k}, -1)
			for i, res := range got {
				switch {
				case i < k:
					if res != `{"result":null}` {
						t.Errorf("part %d before the refusal ended with %s, want it published and resolved", i, res)
					}
				case i == k:
					if !strings.Contains(res, `"rejected"`) || !strings.Contains(res, "refused") || sessionFailed(res) {
						t.Errorf("the refused part %d ended with %s, want its refusal", i, res)
					}
				default:
					if !sessionFailed(res) {
						t.Errorf("part %d after the refusal ended with %s, want SessionFailed", i, res)
					}
				}
			}
		})
	}
}

// A call that rejects after its k-th receipt publishes the received parts, which reject with the call's error; every stored part after
// the last receipt has the call's error too, while a job without writes between them is SessionFailed (writer.ts #publish: "A part after a
// rejected call's last receipt has the call's error").
func TestThePartsAfterARejectedCallsLastReceiptRejectWithItsError(t *testing.T) {
	const n = 4
	for k := range n {
		t.Run("receipts="+strconv.Itoa(k), func(t *testing.T) {
			got := failedCall(t, n, &storageScript{refuseAt: &k, reject: "connection lost"}, 1)
			// The job without writes follows part 1.
			for i, res := range got {
				part := i
				if i > 2 {
					part = i - 1
				}
				if i == 2 {
					if !sessionFailed(res) {
						t.Errorf("the job without writes ended with %s, want SessionFailed", res)
					}
					continue
				}
				if !strings.Contains(res, `"rejected"`) || !strings.Contains(res, "connection lost") || sessionFailed(res) {
					t.Errorf("part %d (received %v) ended with %s, want the call's error", part, part < k, res)
				}
			}
		})
	}
}

// The parts of one call are published a macrotask apart: what the first resolves (the host's end answer and what it wakes) is delivered
// before the second is published (session.ts #publish, group-commit.md 5.3).
func TestThePartsOfOneCallArePublishedAMacrotaskApart(t *testing.T) {
	g := openGC(t, "commit")
	a, b, c := g.createConversation(), g.createConversation(), g.createConversation()
	index, release := g.holdNext()
	first := g.appendEntry(a)
	g.waitFor("the held call", func() bool { return len(g.calls) > index })
	second := g.appendEntry(b)
	third := g.appendEntry(c)
	g.flush()
	release()
	for _, run := range []*commitRun{first, second, third} {
		if res := g.result(run); res != `{"result":null}` {
			t.Fatalf("commit ended with %s", res)
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	// Position of the answer of the second commit's end and of the publication of the third (the second call's two parts).
	endSecond, publishThird := -1, -1
	pubs := 0
	published := 0
	for _, n := range g.notices {
		if n.kind == abi.NoticePublished {
			published++
		}
	}
	for i, n := range g.notices {
		if n.kind == abi.NoticePublished {
			pubs++
			if pubs == published {
				publishThird = i
			}
		}
		if n.kind == abi.NoticeTxResult && n.id == second.id && strings.Contains(string(n.payload), `"result":null`) {
			endSecond = i
		}
	}
	if endSecond < 0 || publishThird < 0 || endSecond > publishThird {
		t.Fatalf("the second commit's end was answered at notice %d and the third part published at %d: the answer must come first", endSecond, publishThird)
	}
}

// With the Storage's publication scope (Storage.publishing) the core publishes each part inside it: one scope per published commit.
func TestEachPartIsPublishedInsideTheStoragesScope(t *testing.T) {
	g := openGC(t, "commit", "publishing")
	// The scope's answer reaches the test after the notice of the publication it ran, so a count waits for the scope of every published commit.
	scoped := func() (scopes, published int) {
		g.waitFor("the scope of every published commit", func() bool {
			published = 0
			for _, n := range g.notices {
				if n.kind == abi.NoticePublished {
					published++
				}
			}
			return g.scopes == published
		})
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.scopes, published
	}
	g.createConversation()
	conv := g.createConversation()
	if scopes, published := scoped(); scopes == 0 {
		t.Fatalf("%d scopes ran for %d published commits, want one each", scopes, published)
	}
	run := g.appendEntry(conv)
	if res := g.result(run); res != `{"result":null}` {
		t.Fatalf("the commit ended with %s", res)
	}
	scoped()
}

// Close waits for the call in flight and its publication (group-commit.md 5.4): the close is answered only after the Storage answered.
func TestCloseDrainsTheCallInFlight(t *testing.T) {
	g := openGC(t, "commit")
	conv := g.createConversation()
	index, release := g.holdNext()
	run := g.appendEntry(conv)
	g.waitFor("the held call", func() bool { return len(g.calls) > index })
	if err := g.host.Send(g.ctx, abi.Event{Kind: abi.EventSeal}); err != nil {
		t.Fatal(err)
	}
	if err := g.host.Send(g.ctx, abi.Event{Kind: abi.EventClose}); err != nil {
		t.Fatal(err)
	}
	closed := func() bool {
		for _, n := range g.notices {
			if n.kind == abi.NoticeAPIResult && n.id == 0 {
				return true
			}
		}
		return false
	}
	time.Sleep(300 * time.Millisecond)
	g.mu.Lock()
	early := closed()
	g.mu.Unlock()
	if early {
		t.Fatal("close was answered while a Storage call was in flight")
	}
	release()
	g.waitFor("the close", closed)
	if res := g.result(run); res != `{"result":null}` {
		t.Fatalf("the commit in flight at close ended with %s, want it stored and resolved", res)
	}
}
