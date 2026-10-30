package host

import (
	"context"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/next/host/parked.ts. Parked calls (§3, §7): calls made on a loading reference, queued in the
// order they were made until it goes live, each with its own deadline from the moment it was called (the reference's
// `readyTimeout`, never renewed).

// parkedCallResult is the Promise a parked call's caller gets: settled once, by the call it becomes once sent, or by why it
// left the queue. The caller waits on it; a call's result chained with then runs as a microtask of the loop, as a Promise
// reaction does.
type parkedCallResult struct {
	mu      sync.Mutex
	done    chan struct{}
	settled bool
	value   any
	err     error
	thens   []parkedThen
}

type parkedThen struct {
	loop eventLoop
	then func(value any, err error)
}

func newParkedCallResult() *parkedCallResult { return &parkedCallResult{done: make(chan struct{})} }

// settle resolves (err nil) or rejects the result; only the first settle counts, as a Promise's.
func (r *parkedCallResult) settle(value any, err error) {
	r.mu.Lock()
	if r.settled {
		r.mu.Unlock()
		return
	}
	r.settled, r.value, r.err = true, value, err
	thens := r.thens
	r.thens = nil
	close(r.done)
	r.mu.Unlock()
	for _, each := range thens {
		each.loop.Queue(func() { each.then(value, err) })
	}
}

// wait blocks until the result is settled: Pi's `await`. Call it outside the host's sections.
func (r *parkedCallResult) wait() (any, error) {
	<-r.done
	return r.value, r.err
}

// then is Pi's `.then(onFulfilled, onRejected)` with one callback: then runs on a microtask of loop once the result is
// settled (at once queued when it is).
func (r *parkedCallResult) then(loop eventLoop, then func(value any, err error)) {
	r.mu.Lock()
	if !r.settled {
		r.thens = append(r.thens, parkedThen{loop: loop, then: then})
		r.mu.Unlock()
		return
	}
	value, err := r.value, r.err
	r.mu.Unlock()
	loop.Queue(func() { then(value, err) })
}

// parkedCall is one queued call: what to send, how its caller is settled, its deadline (`Date.now()` time) and what ends its
// wait.
type parkedCall struct {
	method   string
	args     any
	ctx      context.Context
	result   *parkedCallResult
	due      float64
	deadline Timer
	// cancel stops the abort listener of the caller's context; nil when the context has none.
	cancel func() bool
	// queued is whether the call is still in the queue.
	queued bool
}

// parkedCalls is the queue of one reference's parked calls. A call leaves it when it is sent, at its deadline
// (`unavailable/park_timeout`), when its caller cancels, or rejected with the queue (the reference failed or was released).
// It is used inside the host's sections.
type parkedCalls struct {
	loop sectionLoop
	// calls are in the order the calls were made (Pi's `Set` keeps insertion order and deletes in constant time): a left
	// call is marked, and the slice is compacted when no walk runs and most of it is gone. A walk sees the calls queued
	// while it runs, as a Set's iteration does.
	calls   []*parkedCall
	size    int
	removed int
	walking int
	timeout time.Duration
	label   string
}

func newParkedCalls(loop sectionLoop, timeout time.Duration, label string) *parkedCalls {
	return &parkedCalls{loop: loop, timeout: timeout, label: label}
}

// waiting reports whether calls are queued: a new call waits behind them, also while they are being sent.
func (p *parkedCalls) waiting() bool { return p.size > 0 }

// park queues a call; the result settles as the call it becomes once sent, or with why it left the queue. A call whose
// context is aborted already returns its abort reason, as `throwIfAborted` throws it.
func (p *parkedCalls) park(method string, args any, ctx context.Context) (*parkedCallResult, error) {
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	call := &parkedCall{method: method, args: args, ctx: ctx, result: newParkedCallResult(), queued: true}
	call.deadline = p.loop.AfterFunc(p.timeout, func() { p.expire(call) })
	if ctx.Done() != nil {
		call.cancel = context.AfterFunc(ctx, func() {
			p.loop.Run(func() {
				if !call.queued {
					return
				}
				p.take(call)
				call.result.settle(nil, context.Cause(ctx))
			})
		})
	}
	call.due = float64(p.loop.Now()) + millis(p.timeout)
	p.calls = append(p.calls, call)
	p.size++
	return call.result, nil
}

// flush sends the queued calls in order through send, each only while live() still holds: sending one runs code (a
// handler's synchronous part) that may release or drop the reference, and the rest then stays queued or is rejected by
// that; or that makes a new call, which is queued behind the rest and sent in this loop. A call whose deadline passed while
// earlier ones ran (their timers could not fire) is rejected instead of sent.
func (p *parkedCalls) flush(send func(method string, args any, ctx context.Context) *parkedCallResult, live func() bool) {
	p.walking++
	defer p.walked()
	for at := 0; at < len(p.calls); at++ {
		call := p.calls[at]
		if !call.queued {
			continue
		}
		if !live() {
			return
		}
		if float64(p.loop.Now()) >= call.due {
			p.expire(call)
			continue
		}
		p.take(call)
		send(call.method, call.args, call.ctx).then(p.loop, call.result.settle)
	}
}

// reject rejects every queued call with err.
func (p *parkedCalls) reject(err error) {
	p.walking++
	defer p.walked()
	for at := 0; at < len(p.calls); at++ {
		call := p.calls[at]
		if !call.queued {
			continue
		}
		p.take(call)
		call.result.settle(nil, err)
	}
}

// expire: call's deadline passed: `unavailable/park_timeout`.
func (p *parkedCalls) expire(call *parkedCall) {
	if !call.queued {
		return
	}
	p.take(call)
	message := p.label + ": a call waited " + millisText(p.timeout) + " ms for its reference to go live"
	call.result.settle(nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Reason: data.ReasonParkTimeout, Message: message}, nil))
}

// take takes call out of the queue: its deadline and cancellation stop.
func (p *parkedCalls) take(call *parkedCall) {
	call.queued = false
	p.size--
	p.removed++
	call.deadline.Stop()
	if call.cancel != nil {
		call.cancel()
	}
	if p.walking == 0 && p.removed > 16 && p.removed*2 > len(p.calls) {
		p.compact()
	}
}

// walked ends a walk of the queue; the calls left meanwhile are dropped once no walk runs.
func (p *parkedCalls) walked() {
	p.walking--
	if p.walking == 0 && p.removed > 0 {
		p.compact()
	}
}

func (p *parkedCalls) compact() {
	kept := p.calls[:0]
	for _, call := range p.calls {
		if call.queued {
			kept = append(kept, call)
		}
	}
	clear(p.calls[len(kept):])
	p.calls = kept
	p.removed = 0
}

// millis is d in milliseconds, as JavaScript's number of it.
func millis(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// millisText is d's milliseconds as JavaScript prints the number.
func millisText(d time.Duration) string {
	ms := millis(d)
	if math.IsInf(ms, 1) {
		return "Infinity"
	}
	return strconv.FormatFloat(ms, 'f', -1, 64)
}
