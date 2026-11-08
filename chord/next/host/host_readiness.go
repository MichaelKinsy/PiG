package host

import (
	"context"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/host/readiness.ts. A restart's readiness (decision "Reload restarts every affected object; old work
// finishes on old code", rule 3): the requests its new factory made in staging, bound once every fate of the reload started
// (every served type is the new one then), and held until the successor's own requests bound. The old attempt is sealed only
// once they are ready, so old work that reaches the object meanwhile still finds its old attempt open.
//
// Forced Go differences. Every method runs in a section of the host's loop. Pi's `Promise.race([timer, allReady])` is a
// promise settled by the first of the two, the timer stopped when it settles; the `AbortController` is a chordctx cancel.

// readiness is one restart's readiness: ready once each recorded type request is live (an open old instance that binds its
// version counts) or failed for a reason other than another host's version skew (which heals by itself), or at its bound.
// Registry requests are served by facets in the commit and kept by the old attempt's contributions until its seal: it does not
// wait for them. Its failure follows the requests: `shows` gets each change of it.
type readiness struct {
	host     *chordHost
	from     caller
	requests []request
	shows    func(cause *data.FailureCause)
	ctx      context.Context
	bindings []*binding
	started  *keyedPromise[struct{}]
	// typed is how many of its requests are type requests (the ones it binds); a readiness with none is ready once started.
	typed int
	// abort ends the waits on its bindings at its release; aborted is the context it cancels (both nil with no type
	// request: nothing waits).
	aborted context.Context
	abort   func(cause error)
	// refused is a request refused for good at the start (an authority, a host address): it heals with no change of a
	// binding.
	refused *data.FailureCause
	// failed is the failed request's cause `shows` got last (`from`), as the successor waits for it (`waiting`).
	failed *readinessFailure
}

// readinessFailure is the failed request's cause shown last, and the `waiting` cause the successor shows for it.
type readinessFailure struct {
	from    data.FailureCause
	waiting data.FailureCause
}

// newReadiness makes the readiness of requests, as from asks them; shows gets each change of its failure.
func newReadiness(host *chordHost, from caller, requests []request, shows func(cause *data.FailureCause), ctx context.Context) *readiness {
	r := &readiness{host: host, from: from, requests: requests, shows: shows, ctx: ctx, started: newKeyedPromise[struct{}](host.loop())}
	for _, each := range requests {
		if each.kind == requestType {
			r.typed++
		}
	}
	// Most restarts depend on no other type: nothing to wait on, so no context to abort it with.
	if r.typed > 0 {
		r.aborted, r.abort = chordctx.WithCancel(chordctx.Background())
	}
	return r
}

// following is the context of the waits on the bindings: the object's, ended with the readiness's release.
func (r *readiness) following() context.Context {
	if r.aborted == nil {
		return r.ctx
	}
	return chordctx.WithAbortSignal(r.ctx, r.aborted)
}

// cause is what the successor waits for now: a request refused for good, or `waiting` for the first failed one; nil when none.
func (r *readiness) cause() *data.FailureCause {
	if r.refused != nil {
		return r.refused
	}
	if r.failed != nil {
		return &r.failed.waiting
	}
	return nil
}

// start binds the recorded type requests as the object's own would be (once every fate of the reload started), kept out of
// presence lists (they are no consumers of their own), and follows them: shows gets each change of cause.
func (r *readiness) start() {
	following := r.following()
	for _, each := range r.requests {
		if each.kind != requestType {
			continue
		}
		binding, err := r.host.reference(each.contract, nil, r.from, GetOptions{})
		if err == nil {
			binding.unlist()
			r.bindings = append(r.bindings, binding)
			var checked checkedParams
			if checked, err = checkParams(each.contract, each.params, r.ctx); err == nil {
				_, err = r.host.bind(binding, each.contract, checked, r.ctx)
			}
		}
		if err != nil {
			// Refused for good (an authority, a host address): the successor's factory meets the same refusal.
			if r.refused == nil {
				r.refused = &data.FailureCause{Kind: data.FailureBroken, Error: data.AsChordError(err, each.contract.Name)}
			}
			continue
		}
		// Checked at every change of the binding until the release (never done): the failure shown follows it. A wait ends
		// with the binding's release or the abort.
		binding.until(func(*RefState) decision {
			r.changed()
			return waitDecision
		}, following).then(func(struct{}, error) {})
	}
	r.changed()
	r.started.resolve(struct{}{})
}

// ready resolves once every request is ready, or at due (the loop's `Now`, milliseconds), whichever is first.
func (r *readiness) ready(due int64) *keyedPromise[struct{}] {
	if r.typed == 0 {
		// No binding to wait for: ready as soon as every fate of the reload started (nothing to bound).
		return r.started
	}
	loop := r.host.loop()
	result := newKeyedPromise[struct{}](loop)
	timer := loop.AfterFunc(time.Duration(max(0, due-loop.Now()))*time.Millisecond, func() { result.resolve(struct{}{}) })
	result.then(func(struct{}, error) { timer.Stop() })
	r.allReady().then(func(struct{}, error) { result.resolve(struct{}{}) })
	return result
}

// release: the successor's own requests bound: its bindings are let go (a dependency held only by them never ends).
func (r *readiness) release() {
	if r.abort != nil {
		r.abort(nil)
	}
	bindings := r.bindings
	r.bindings = nil
	for _, each := range bindings {
		each.release()
	}
}

// changed: a binding changed: shows gets the failure if it is another one now.
func (r *readiness) changed() {
	before := r.cause()
	found, label := r.firstFailed()
	if found == nil {
		if r.failed != nil {
			r.failed = nil
		}
	} else if r.failed == nil || r.failed.from != *found {
		r.failed = &readinessFailure{from: *found, waiting: data.WaitingFor(label, found.Error)}
	}
	if after := r.cause(); after != before && (after == nil || before == nil || *after != *before) {
		r.shows(after)
	}
}

// firstFailed is the first request failed for a reason other than another host's version skew, with its cause and label; nil
// when none.
func (r *readiness) firstFailed() (*data.FailureCause, string) {
	for _, each := range r.bindings {
		current := each.refState()
		if current.Kind == RefStateFailed && !versionSkew(each) {
			return &current.Cause, each.label()
		}
	}
	return nil, ""
}

// allReady resolves once every binding is ready (or ended: nothing waits any more).
func (r *readiness) allReady() *keyedPromise[struct{}] {
	loop := r.host.loop()
	all := newKeyedPromise[struct{}](loop)
	r.started.then(func(struct{}, error) {
		following := r.following()
		// A wait ends with the binding (released) or the abort: either way nothing waits any more.
		waits := make([]*keyedPromise[struct{}], 0, len(r.bindings))
		for _, each := range r.bindings {
			done := newKeyedPromise[struct{}](loop)
			each.untilState(func(*RefState) bool { return isReady(each) }, following).then(func(struct{}, error) { done.resolve(struct{}{}) })
			waits = append(waits, done)
		}
		promiseAllVoid(loop, waits).then(func(struct{}, error) { all.resolve(struct{}{}) })
	})
	return all
}

// isReady is whether a request is ready: live, ended, or failed for a reason other than a version skew.
func isReady(b *binding) bool {
	current := b.refState()
	if current.Kind == RefStateFailed {
		return !versionSkew(b)
	}
	return current.Kind != RefStateLoading
}

// versionSkew is whether b failed for another host's version refusal (its own, or a dependency's along the chain: `waiting`
// causes keep the reason): that heals when the other host's reload lands, so the old attempt stays open meanwhile, bounded as
// always. A refusal on this host heals by no other reload: it is no skew.
func versionSkew(b *binding) bool {
	current := b.refState()
	goal := b.target()
	if current.Kind != RefStateFailed || goal == nil || goal.kind() != "remote" {
		return false
	}
	err := current.Cause.Error
	return err.Code == data.CodeContractMismatch || (err.Code == data.CodeUnavailable && err.Reason == data.ReasonVersion)
}
