package session_test

import (
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// The failure fan-out of the writer: what each part of a Storage call settles with when the call refuses a batch, rejects, or something
// else fails the Session between two publications (packages/durable/src/session/writer.ts #publish; session-group-commit.test.ts "failures
// per part"). The Storage is the test: it receives the batches it chooses and answers the call as it chooses.

// fanoutCall is one Storage call the writer made: the host answers it through receive and done.
type fanoutCall struct {
	batches int
	receive func(index int, seq session.Seq)
	done    func(session.CommitResult, error)
}

type fanout struct {
	t     *testing.T
	sess  *session.Session
	loop  *session.Loop
	calls []*fanoutCall
	// published is the order in which the parts' Publish ran, by part number.
	published []int
	macros    []session.Resumer
}

func newFanout(t *testing.T) *fanout {
	t.Helper()
	mem := sessiontest.New()
	f := &fanout{t: t}
	f.sess = session.New(mem, mem.NextID(), func() float64 { return 1 })
	f.loop = bound(f.sess)
	f.loop.Macro = func(r session.Resumer) bool { f.macros = append(f.macros, r); return true }
	f.sess.CommitBatches = func(batches [][]session.Write, _ session.ID, receive func(int, session.Seq), done func(session.CommitResult, error)) {
		f.calls = append(f.calls, &fanoutCall{batches: len(batches), receive: receive, done: done})
	}
	return f
}

// admit admits part number n: stored (with writes) or held (without), numbered as a line index.
func (f *fanout) admit(n int, stored bool) *session.Part {
	p := &session.Part{Index: n, Publish: func(session.Seq) error { f.published = append(f.published, n); return nil }}
	if stored {
		p.Writes = []session.Write{{Kind: session.WriteConversation, Record: obj("id", float64(n+1))}}
		p.Keys = []string{session.ConversationKey(int64(n + 1))}
	}
	f.sess.Writer().Admit(p)
	return p
}

// settle runs the line and every macrotask the writer waits for until nothing is left to run.
func (f *fanout) settle() {
	f.t.Helper()
	for {
		if err := f.loop.Run(); err != nil {
			f.t.Fatal(err)
		}
		if len(f.macros) == 0 {
			return
		}
		r := f.macros[0]
		f.macros = f.macros[1:]
		r.Resume()
	}
}

// secondCall admits part 0 alone, which is in flight while the parts of the second call are admitted (stored[i] says whether part i + 1
// has writes), and returns the second call, in flight, with its parts.
func (f *fanout) secondCall(stored []bool) (*fanoutCall, []*session.Part) {
	f.t.Helper()
	first := f.admit(0, true)
	f.settle()
	if len(f.calls) != 1 {
		f.t.Fatalf("%d calls in flight, want the first", len(f.calls))
	}
	parts := make([]*session.Part, len(stored))
	for i, s := range stored {
		parts[i] = f.admit(i+1, s)
	}
	f.settle()
	f.calls[0].receive(0, 100)
	f.calls[0].done(session.CommitResult{Stored: 1}, nil)
	f.settle()
	if first.Err() != nil || !first.Done() {
		f.t.Fatalf("the first part ended with %v (done %v)", first.Err(), first.Done())
	}
	if len(f.calls) != 2 {
		f.t.Fatalf("%d calls, want the parts admitted meanwhile in a second one", len(f.calls))
	}
	return f.calls[1], parts
}

func (f *fanout) wasPublished(n int) bool {
	return slices.Contains(f.published, n)
}

// sessionFailedWith reports whether err is SessionFailed with cause as its cause.
func sessionFailedWith(err, cause error) bool {
	var failed *session.FailedError
	return errors.Is(err, session.ErrSessionFailed) && errors.As(err, &failed) && errors.Is(failed.Cause, cause)
}

// storedBefore is the number of parts with writes before part i.
func storedBefore(stored []bool, i int) int {
	n := 0
	for _, s := range stored[:i] {
		if s {
			n++
		}
	}
	return n
}

// A refusal at stored batch k of n: the parts before k are received, published and resolve; k rejects with the refusal and fails the
// Session; every part after k was never stored or published and rejects with SessionFailed(refusal), a held one included (writer.ts
// #failed(stop.error)).
func TestThePartsAfterARefusedBatchRejectWithSessionFailedAndTheRefusal(t *testing.T) {
	// Parts 1..5 of the second call; part 3 is held, without writes.
	stored := []bool{true, true, false, true, true}
	for k := range 4 {
		t.Run("refused="+strconv.Itoa(k), func(t *testing.T) {
			f := newFanout(t)
			call, parts := f.secondCall(stored)
			refusal := errors.New("refused")
			for i := range k {
				call.receive(i, session.Seq(200+i))
			}
			call.done(session.CommitResult{Stored: k, Refused: refusal}, nil)
			f.settle()
			if got := f.sess.Failure(); !errors.Is(got, refusal) {
				t.Fatalf("the Session failed with %v, want the refusal", got)
			}
			for i, p := range parts {
				published := f.wasPublished(i + 1)
				before := storedBefore(stored, i)
				switch {
				case stored[i] && before == k:
					if !errors.Is(p.Err(), refusal) || errors.Is(p.Err(), session.ErrSessionFailed) || published {
						t.Errorf("the refused part %d ended with %v (published %v), want the refusal", i, p.Err(), published)
					}
				case before < k || !stored[i] && before == k:
					if p.Err() != nil || !published {
						t.Errorf("part %d before the refusal ended with %v (published %v), want it published and resolved", i, p.Err(), published)
					}
				default:
					if !p.Done() || !sessionFailedWith(p.Err(), refusal) || published {
						t.Errorf("part %d after the refusal ended with %v (done %v, published %v), want SessionFailed(refusal)", i, p.Err(), p.Done(), published)
					}
				}
			}
		})
	}
}

// A call that rejects after its k-th receipt: the parts published before its last receipt (a held one among them) are durable and reject
// with the call's error; every stored part after it was not published and has the call's error too; a held part after it is
// SessionFailed (writer.ts #publish: "A part after a rejected call's last receipt has the call's error").
func TestThePartsAfterARejectedCallsLastReceiptRejectWithItsError(t *testing.T) {
	stored := []bool{true, true, false, true, true}
	for k := range 4 {
		t.Run("receipts="+strconv.Itoa(k), func(t *testing.T) {
			f := newFanout(t)
			call, parts := f.secondCall(stored)
			lost := errors.New("connection lost")
			for i := range k {
				call.receive(i, session.Seq(200+i))
			}
			call.done(session.CommitResult{}, lost)
			f.settle()
			if got := f.sess.Failure(); !errors.Is(got, lost) {
				t.Fatalf("the Session failed with %v, want the call's error", got)
			}
			for i, p := range parts {
				published := f.wasPublished(i + 1)
				before := storedBefore(stored, i)
				switch {
				case stored[i]:
					if !errors.Is(p.Err(), lost) || errors.Is(p.Err(), session.ErrSessionFailed) || published != (before < k) {
						t.Errorf("stored part %d ended with %v (published %v), want the call's error, published only with a receipt (%v)", i, p.Err(), published, before < k)
					}
				case before <= k:
					// A part published while its call rejected is durable but rejects (spec section 10.1).
					if !errors.Is(p.Err(), lost) || errors.Is(p.Err(), session.ErrSessionFailed) || !published {
						t.Errorf("held part %d ended with %v (published %v), want it published, then the call's error", i, p.Err(), published)
					}
				default:
					if !sessionFailedWith(p.Err(), lost) || published {
						t.Errorf("held part %d after the stop ended with %v (published %v), want SessionFailed(call's error)", i, p.Err(), published)
					}
				}
			}
		})
	}
}

// Something else fails the Session during the macrotask between two parts of a call: the later part is not published, and rejects with
// SessionFailed(that failure), not as if it had been stored (session-group-commit.test.ts "publishes no part of a call after a failure
// that a continuation of an earlier part caused").
func TestAPartAfterAFailureBetweenTwoPublicationsRejectsWithSessionFailed(t *testing.T) {
	f := newFanout(t)
	call, parts := f.secondCall([]bool{true, true, true})
	call.receive(0, 200)
	call.receive(1, 201)
	call.receive(2, 202)
	call.done(session.CommitResult{Stored: 3}, nil)
	if err := f.loop.Run(); err != nil {
		t.Fatal(err)
	}
	if !f.wasPublished(1) || f.wasPublished(2) || len(f.macros) != 1 {
		t.Fatalf("published %v with %d macrotasks pending, want the first part published and the writer waiting to publish the second", f.published, len(f.macros))
	}
	first := errors.New("first failure")
	f.sess.Fail(first)
	f.settle()
	if !errors.Is(f.sess.Failure(), first) {
		t.Fatalf("the Session failed with %v, want the first failure", f.sess.Failure())
	}
	if err := parts[0].Err(); err != nil {
		t.Errorf("the part published before the failure ended with %v, want it resolved", err)
	}
	for i, p := range parts[1:] {
		if !p.Done() || !sessionFailedWith(p.Err(), first) || f.wasPublished(i+2) {
			t.Errorf("part %d after the failure ended with %v (done %v, published %v), want SessionFailed(first failure), unpublished", i+1, p.Err(), p.Done(), f.wasPublished(i+2))
		}
	}
}

// A call rejects after the receipt of its only part, and the Session fails before the Storage's scope runs the part's publication, so the
// call has no part of its own: Pi's `own[0]!` is then undefined and the Session's first failure stands, instead of a panic.
func TestARejectedCallWithoutAnOwnPartDoesNotPanic(t *testing.T) {
	f := newFanout(t)
	var scopes []func()
	f.sess.Publishing = func(publish func()) bool { scopes = append(scopes, publish); return true }
	part := f.admit(0, true)
	f.settle()
	if len(f.calls) != 1 {
		t.Fatalf("%d calls, want one", len(f.calls))
	}
	f.calls[0].receive(0, 100)
	f.calls[0].done(session.CommitResult{}, errors.New("connection lost"))
	f.settle()
	if len(scopes) != 1 {
		t.Fatalf("%d scopes asked for, want the part's publication waiting for the Storage's scope", len(scopes))
	}
	first := errors.New("first failure")
	f.sess.Fail(first)
	scopes[0]()
	f.settle()
	if !errors.Is(f.sess.Failure(), first) {
		t.Fatalf("the Session failed with %v, want the first failure", f.sess.Failure())
	}
	if !part.Done() || !sessionFailedWith(part.Err(), first) || f.wasPublished(0) {
		t.Errorf("the part ended with %v (done %v, published %v), want SessionFailed(first failure), unpublished", part.Err(), part.Done(), f.wasPublished(0))
	}
}
