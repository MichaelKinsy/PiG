package uiview

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// These tests pin which changes of a mount belong to an intent's answer. A transport marks a call the answering handler
// made with the intent's ticket (Answering); the tests set it the way the subprocess bridge does.

var answerDone = Result{Outcome: OutcomeDone, Answer: json.RawMessage(`1`)}

// handler returns an invoker that runs do as the plugin's handler and then answers res.
func handler(do func(inv Invocation), res Result) *fakeInvoker {
	return &fakeInvoker{invoke: func(_ context.Context, inv Invocation) (Result, error) {
		do(inv)
		return res, nil
	}}
}

// raised is the ref the intent of inv was raised on.
func raised(inv Invocation) Ref {
	return Ref{Key: inv.Mount.Key, Epoch: inv.Mount.Epoch, Rev: inv.Rev}
}

// ownUpdate is an update the handler of ticket makes; foreignUpdate one made by any other hand. Both return the new ref
// and the error.
func ownUpdate(r *Registry, ticket string, a UpdateArgs) (Ref, *Error) {
	a.Answering = ticket
	st, err := r.Update("p", a)
	return st.Mount, err
}

func foreignUpdate(r *Registry, a UpdateArgs) (Ref, *Error) {
	st, err := r.Update("p", a)
	return st.Mount, err
}

func invoke(r *Registry, ref Ref, inv Invoker) Result {
	return r.Invoke(context.Background(), "p", ref, "", "p.go", json.RawMessage(`1`), inv)
}

func mountRev(t *testing.T, r *Registry, key string) uint64 {
	t.Helper()
	st, err := r.State("p", StateArgs{Key: key})
	wantOK(t, "state", err)
	return st.Mount.Rev
}

func TestOwnUpdateBelongsToTheAnswer(t *testing.T) {
	r, st := intentMount(t, false, 0)
	var seen []Delivery
	_, _, err := r.Attach(*abilities(), func(d Delivery) bool {
		seen = append(seen, d)
		return true
	})
	wantOK(t, "attach", err)
	redraw := updateArgs(t, st.Mount, "redrawn")
	var uerr *Error
	res := invoke(r, st.Mount, handler(func(inv Invocation) { _, uerr = ownUpdate(r, inv.Ticket, redraw) }, answerDone))
	wantOK(t, "own update", uerr)
	if res.Outcome != OutcomeDone || string(res.Answer) != "1" {
		t.Fatalf("an intent whose handler redrew its own mount = %+v, want done", res)
	}
	// The update returned to the handler after the client's sink took it, so the client holds the new view when the
	// answer comes back.
	if len(seen) != 1 || seen[0].Snapshot.Mount.Rev != 2 || !strings.Contains(string(seen[0].Snapshot.View), "redrawn") {
		t.Fatalf("the client was delivered %+v before the answer, want the redrawn view at rev 2", seen)
	}
	d, derr := r.Diagnostics("p", StateArgs{Key: "k"})
	wantOK(t, "diagnostics", derr)
	if len(d.Notices) != 0 {
		t.Errorf("notices = %+v, want none", d.Notices)
	}
}

func TestOwnUpdateThenAChangeFromElsewhereIsStale(t *testing.T) {
	for name, order := range map[string][2]string{"own first": {"own", "foreign"}, "foreign first": {"foreign", "own"}} {
		t.Run(name, func(t *testing.T) {
			r, st := intentMount(t, false, 0)
			var errs []*Error
			res := invoke(r, st.Mount, handler(func(inv Invocation) {
				ref := raised(inv)
				for _, who := range order {
					a := UpdateArgs{Mount: ref, Content: st2content(t, who)}
					var err *Error
					if who == "own" {
						ref, err = ownUpdate(r, inv.Ticket, a)
					} else {
						ref, err = foreignUpdate(r, a)
					}
					errs = append(errs, err)
				}
			}, answerDone))
			for _, err := range errs {
				wantOK(t, "update", err)
			}
			if res.Outcome != OutcomeStale || res.Error != "mount changed while the plugin answered" {
				t.Fatalf("result = %+v, want stale", res)
			}
			if rev := mountRev(t, r, "k"); rev != 3 {
				t.Errorf("rev = %d, want 3: both updates were applied", rev)
			}
		})
	}
}

// st2content is the content of an update whose view is text.
func st2content(t *testing.T, text string) Content {
	t.Helper()
	return updateArgs(t, Ref{Key: "k"}, text).Content
}

func TestOwnTransitionKeepsTheAnswer(t *testing.T) {
	for op, want := range map[string]State{"hide": StateHidden, "finish": StateFinished, "remove": StateRemoved} {
		t.Run(op, func(t *testing.T) {
			r, st := intentMount(t, false, 0)
			var seen []Delivery
			_, _, err := r.Attach(*abilities(), func(d Delivery) bool {
				seen = append(seen, d)
				return true
			})
			wantOK(t, "attach", err)
			last := updateArgs(t, st.Mount, "last")
			var errs [2]*Error
			res := invoke(r, st.Mount, handler(func(inv Invocation) {
				var ref Ref
				ref, errs[0] = ownUpdate(r, inv.Ticket, last)
				if op == "finish" {
					_, errs[1] = r.Finish("p", FinishArgs{Mount: ref})
				} else {
					_, errs[1] = r.Transition("p", op, RefArgs{Mount: ref})
				}
			}, answerDone))
			wantOK(t, "own update", errs[0])
			wantOK(t, op, errs[1])
			if res.Outcome != OutcomeDone {
				t.Fatalf("a handler that ended its own mount with %s = %+v, want done", op, res)
			}
			// The client was sent the last view and then the mount's end, both before the answer.
			if len(seen) != 2 || !strings.Contains(string(seen[0].Snapshot.View), "last") || seen[1].Snapshot.State != want || seen[1].Snapshot.Mount.Rev != 2 {
				t.Fatalf("the client was delivered %+v, want the last view and then state %s", seen, want)
			}
		})
	}
}

func TestOwnUpdateOfAnotherMountLeavesBothAnswersAlone(t *testing.T) {
	r, st := intentMount(t, false, 0)
	decl := IntentDecl{Name: "p.go", Arg: Shape{Type: "integer"}, Answer: &Shape{Type: "integer"}}
	other, err := r.Mount("p", mountArgs(t, "k2", "y", decl))
	wantOK(t, "mount k2", err)
	elsewhere := updateArgs(t, other.Mount, "y2")
	entered, release := make(chan struct{}), make(chan struct{})
	onOther := make(chan Result, 1)
	var uerr *Error
	res := invoke(r, st.Mount, handler(func(inv Invocation) {
		// An intent on the other mount is in flight while this handler updates that mount.
		go func() {
			onOther <- invoke(r, other.Mount, handler(func(Invocation) {
				close(entered)
				<-release
			}, answerDone))
		}()
		<-entered
		_, uerr = ownUpdate(r, inv.Ticket, elsewhere)
		close(release)
	}, answerDone))
	wantOK(t, "update of the other mount", uerr)
	if res.Outcome != OutcomeDone {
		t.Errorf("the intent whose handler updated another mount = %+v, want done", res)
	}
	// This intent's ticket names an answer on k, so on k2 the update is a change from elsewhere.
	if got := <-onOther; got.Outcome != OutcomeStale {
		t.Errorf("the intent in flight on the other mount = %+v, want stale", got)
	}
	if rev := mountRev(t, r, "k2"); rev != 2 {
		t.Errorf("k2 rev = %d, want 2", rev)
	}
}

func TestOwnUpdateStaysWhenTheHandlerRefusesOrFails(t *testing.T) {
	for _, answer := range []Result{{Outcome: OutcomeRefused, Error: "not now"}, {Outcome: OutcomeFailed, Error: "broke"}} {
		t.Run(string(answer.Outcome), func(t *testing.T) {
			r, st := intentMount(t, false, 0)
			kept := updateArgs(t, st.Mount, "kept")
			var uerr *Error
			res := invoke(r, st.Mount, handler(func(inv Invocation) { _, uerr = ownUpdate(r, inv.Ticket, kept) }, answer))
			wantOK(t, "own update", uerr)
			if res.Outcome != answer.Outcome || res.Error != answer.Error {
				t.Fatalf("result = %+v, want the handler's %+v", res, answer)
			}
			if rev := mountRev(t, r, "k"); rev != 2 {
				t.Errorf("rev = %d, want 2: the update stays", rev)
			}
		})
	}
}

func TestSettledTicketIsAPlainUpdate(t *testing.T) {
	for name, c := range map[string]struct {
		cancellable bool
		timeoutMs   int
		want        Outcome
	}{"timeout": {false, 100, OutcomeTimeout}, "cancelled": {true, 0, OutcomeCancelled}} {
		t.Run(name, func(t *testing.T) {
			r, st := intentMount(t, c.cancellable, c.timeoutMs)
			late := updateArgs(t, st.Mount, "late")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, settled := make(chan struct{}), make(chan struct{})
			lateErr := make(chan *Error, 1)
			first := &fakeInvoker{invoke: func(_ context.Context, inv Invocation) (Result, error) {
				close(entered)
				<-settled
				_, err := ownUpdate(r, inv.Ticket, late)
				lateErr <- err
				return answerDone, nil
			}}
			out := make(chan Result, 1)
			go func() { out <- r.Invoke(ctx, "p", st.Mount, "", "p.go", json.RawMessage(`1`), first) }()
			<-entered
			if c.want == OutcomeCancelled {
				cancel()
			}
			if res := <-out; res.Outcome != c.want {
				t.Fatalf("first intent = %+v, want %s", res, c.want)
			}
			// A second intent is in flight when the first handler, whose intent has settled, updates the mount.
			var uerr *Error
			res := invoke(r, st.Mount, handler(func(Invocation) {
				close(settled)
				uerr = <-lateErr
			}, answerDone))
			wantOK(t, "an update that carries a settled ticket", uerr)
			if res.Outcome != OutcomeStale {
				t.Errorf("the intent in flight = %+v, want stale: a settled ticket attributes nothing", res)
			}
			if rev := mountRev(t, r, "k"); rev != 2 {
				t.Errorf("rev = %d, want 2", rev)
			}
		})
	}
}

func TestTwoIntentsInFlightTheFirstUpdateWins(t *testing.T) {
	r, st := intentMount(t, false, 0)
	fromA := updateArgs(t, st.Mount, "a")
	fromB := updateArgs(t, st.Mount, "b")
	type step struct {
		entered, proceed chan struct{}
		errs             []*Error
	}
	a, b := &step{make(chan struct{}), make(chan struct{}), nil}, &step{make(chan struct{}), make(chan struct{}), nil}
	outA, outB := make(chan Result, 1), make(chan Result, 1)
	go func() {
		outA <- invoke(r, st.Mount, handler(func(inv Invocation) {
			close(a.entered)
			<-a.proceed
			_, err := ownUpdate(r, inv.Ticket, fromA)
			a.errs = append(a.errs, err)
		}, answerDone))
	}()
	go func() {
		outB <- invoke(r, st.Mount, handler(func(inv Invocation) {
			close(b.entered)
			<-b.proceed
			// The view this handler was raised on is gone: its update of that rev is rejected, and the one it computes
			// from the current rev is applied but does not make its answer current.
			_, err := ownUpdate(r, inv.Ticket, fromB)
			b.errs = append(b.errs, err)
			fromB.Mount.Rev = 2
			_, err = ownUpdate(r, inv.Ticket, fromB)
			b.errs = append(b.errs, err)
		}, answerDone))
	}()
	<-a.entered
	<-b.entered
	close(a.proceed)
	if res := <-outA; res.Outcome != OutcomeDone {
		t.Fatalf("the first handler to update = %+v, want done", res)
	}
	wantOK(t, "first handler's update", a.errs[0])
	// A third click on the view the client drew before is refused at the start by the rev check.
	if res := invoke(r, st.Mount, handler(func(Invocation) {}, answerDone)); res.Outcome != OutcomeStale || res.Error != "mount changed" {
		t.Errorf("an intent raised on the old rev = %+v, want stale at the start", res)
	}
	close(b.proceed)
	if res := <-outB; res.Outcome != OutcomeStale {
		t.Fatalf("the second handler = %+v, want stale", res)
	}
	wantCode(t, b.errs[0], CodeStale)
	wantOK(t, "second handler's update of the current rev", b.errs[1])
	if rev := mountRev(t, r, "k"); rev != 3 {
		t.Errorf("rev = %d, want 3", rev)
	}
}

func TestOwnUpdateWithNoClientAttached(t *testing.T) {
	decl := IntentDecl{Name: "p.go", Arg: Shape{Type: "integer"}, Answer: &Shape{Type: "integer"}, Cancellable: true}
	t.Run("the attachment that raised the intent ended", func(t *testing.T) {
		r := New(Options{})
		att, _, err := r.Attach(*abilities(), nil)
		wantOK(t, "attach", err)
		st, err := r.Mount("p", mountArgs(t, "k", "x", decl))
		wantOK(t, "mount", err)
		redraw := updateArgs(t, st.Mount, "redrawn")
		// The intent settles when its attachment ends; the handler runs on and reports its update's error afterwards.
		updated := make(chan *Error, 1)
		res := att.Invoke(context.Background(), "p", st.Mount, "", "p.go", json.RawMessage(`1`), &fakeInvoker{invoke: func(ctx context.Context, inv Invocation) (Result, error) {
			att.Detach()
			<-ctx.Done()
			_, uerr := ownUpdate(r, inv.Ticket, redraw)
			updated <- uerr
			return answerDone, nil
		}})
		wantCode(t, <-updated, CodeUnsupported)
		if res.Outcome != OutcomeStale || res.Error != "attachment ended" {
			t.Errorf("result = %+v, want stale: the attachment ended", res)
		}
		if rev := mountRev(t, r, "k"); rev != 1 {
			t.Errorf("rev = %d, want 1: nothing changes without a client", rev)
		}
	})
	t.Run("the one client detached", func(t *testing.T) {
		r, st := intentMount(t, false, 0)
		redraw := updateArgs(t, st.Mount, "redrawn")
		var derr, uerr *Error
		res := invoke(r, st.Mount, handler(func(inv Invocation) {
			derr = r.SetAbilities(nil)
			_, uerr = ownUpdate(r, inv.Ticket, redraw)
		}, answerDone))
		wantOK(t, "detach", derr)
		wantCode(t, uerr, CodeUnsupported)
		if res.Outcome != OutcomeDone {
			t.Errorf("result = %+v, want done: the rejected update changed nothing", res)
		}
	})
}

func TestOwnRemountBelongsToTheAnswer(t *testing.T) {
	decl := IntentDecl{Name: "p.go", Arg: Shape{Type: "integer"}, Answer: &Shape{Type: "integer"}}
	for name, c := range map[string]struct {
		own  bool
		want Outcome
	}{"by the handler": {true, OutcomeDone}, "from elsewhere": {false, OutcomeStale}} {
		t.Run(name, func(t *testing.T) {
			r, st := intentMount(t, false, 0)
			again := mountArgs(t, "k", "again", decl)
			var errs [2]*Error
			var next Status
			res := invoke(r, st.Mount, handler(func(inv Invocation) {
				_, errs[0] = r.Transition("p", "remove", RefArgs{Mount: raised(inv)})
				if c.own {
					again.Answering = inv.Ticket
				}
				next, errs[1] = r.Mount("p", again)
			}, answerDone))
			wantOK(t, "remove", errs[0])
			wantOK(t, "mount again", errs[1])
			if next.Mount.Epoch <= st.Mount.Epoch {
				t.Fatalf("the new mount has epoch %d, the old one %d", next.Mount.Epoch, st.Mount.Epoch)
			}
			if res.Outcome != c.want {
				t.Errorf("result = %+v, want %s", res, c.want)
			}
		})
	}
}
