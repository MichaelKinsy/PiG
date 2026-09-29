package ai

import (
	"errors"
	"iter"
)

// The types and functions here reproduce tslib's __await, __asyncGenerator and __asyncValues exactly as compiled into @google/genai 2.21.0 (dist/node/index.mjs:7430-7452). Their microtask counts differ from native async generators: a plain `yield` resolves the consumer's promise without awaiting the value, and `yield __await(x)` awaits x with Promise.resolve(x).then(fulfill, reject).

// tslibAwaited is the __await wrapper (index.mjs:7430). A generator body yields it to ask the driver to await v.
type tslibAwaited struct{ v any }

// tslibIterResult is an iterator result object.
type tslibIterResult struct {
	value any
	done  bool
}

// tslibAsyncIterator is a JavaScript async iterator. ret is nil when the iterator has no return method; throw is nil when it has no throw method.
type tslibAsyncIterator struct {
	next      func(value any) *tslibPromise
	throw     func(err error) *tslibPromise
	ret       func(value any) *tslibPromise
	generator *tslibAsyncGenerator
}

// tslibSyncIterator is a JavaScript sync iterator for the __asyncValues fallback. ret is nil when the iterator has no return method.
type tslibSyncIterator struct {
	next func(value any) tslibIterResult
	ret  func(value any) tslibIterResult
}

type tslibResumeKind int

const (
	tslibResumeNext tslibResumeKind = iota
	tslibResumeThrow
	tslibResumeReturn
)

type tslibResume struct {
	kind  tslibResumeKind
	value any
	err   error
}

type tslibRequest struct {
	kind    tslibResumeKind
	value   any
	err     error
	resolve func(any)
	reject  func(error)
}

// tslibReturnSignal unwinds a generator body for gen.return(value) so its deferred finally blocks run.
type tslibReturnSignal struct{ value any }

// tslibThrowSignal replaces the pending completion with a thrown error from inside a finally block.
type tslibThrowSignal struct{ err error }

// tslibAbortSignal unwinds a body when its coroutine is closed without finishing.
type tslibAbortSignal struct{}

// tslibYield is the generator body's view of its own suspension points.
type tslibYield struct {
	yield   func(any) bool
	in      tslibResume
	aborted bool
}

type tslibGeneratorState int

const (
	tslibSuspendedStart tslibGeneratorState = iota
	tslibSuspendedYield
	tslibCompleted
)

// tslibAsyncGenerator is the queue, iterator object and inner JavaScript generator that __asyncGenerator builds (index.mjs:7434-7445). The body plays the inner generator: each y.Await is `yield __await(v)` and each y.Yield is a plain `yield v`.
type tslibAsyncGenerator struct {
	executor *continuationExecutor
	body     func(*tslibYield) (any, error)
	queue    []*tslibRequest
	y        *tslibYield
	pull     func() (any, bool)
	stop     func()
	state    tslibGeneratorState
	retValue any
	retErr   error
}

// newTslibAsyncGenerator is `__asyncGenerator(this, arguments, function* () { body })`. Like the inner generator, body does not start until the first next() request.
func newTslibAsyncGenerator(executor *continuationExecutor, body func(*tslibYield) (any, error)) *tslibAsyncIterator {
	g := &tslibAsyncGenerator{executor: executor, body: body}
	i := &tslibAsyncIterator{generator: g}
	i.next = g.verb(tslibResumeNext)
	throwVerb := g.verb(tslibResumeThrow)
	i.throw = func(err error) *tslibPromise { return throwVerb(err) }
	returnVerb := g.verb(tslibResumeReturn)
	// awaitReturn (index.mjs:7438): Promise.resolve(v).then(f, reject). The promise returned by f is adopted, which costs the two extra reactions.
	i.ret = func(value any) *tslibPromise {
		return tslibPromiseResolve(executor, value).then(func(v any) (any, error) { return returnVerb(v), nil }, func(err error) (any, error) {
			return nil, g.resume(tslibResumeThrow, nil, err)
		})
	}
	return i
}

// verb is tslib's verb(n) (index.mjs:7439): the returned function queues a request and starts it when the queue was empty.
func (g *tslibAsyncGenerator) verb(kind tslibResumeKind) func(any) *tslibPromise {
	return func(value any) *tslibPromise {
		promise := newTslibPromise(g.executor)
		request := &tslibRequest{kind: kind, resolve: promise.resolve, reject: promise.reject}
		if kind == tslibResumeThrow {
			request.err, _ = value.(error)
		} else {
			request.value = value
		}
		g.queue = append(g.queue, request)
		if len(g.queue) == 1 {
			_ = g.resume(kind, request.value, request.err)
		}
		return promise
	}
}

var errTslibEmptyQueue = errors.New("tslib generator queue is empty")

// resume is tslib's resume(n, v) (index.mjs:7440): run the inner generator and step its result; a thrown error settles the head request.
func (g *tslibAsyncGenerator) resume(kind tslibResumeKind, value any, err error) error {
	if len(g.queue) == 0 {
		return errTslibEmptyQueue
	}
	result, thrown := g.call(tslibResume{kind: kind, value: value, err: err})
	if thrown != nil {
		g.settle(func(request *tslibRequest) { request.reject(thrown) })
		return nil
	}
	g.step(result)
	return nil
}

// step is tslib's step(r) (index.mjs:7441): an __await result is awaited with Promise.resolve(v).then(fulfill, reject); anything else settles the head request.
func (g *tslibAsyncGenerator) step(result tslibIterResult) {
	if awaited, ok := result.value.(*tslibAwaited); ok {
		tslibPromiseResolve(g.executor, awaited.v).then(func(value any) (any, error) {
			return nil, g.resume(tslibResumeNext, value, nil)
		}, func(err error) (any, error) {
			return nil, g.resume(tslibResumeThrow, nil, err)
		})
		return
	}
	g.settle(func(request *tslibRequest) { request.resolve(result) })
}

// settle is tslib's settle(f, v) (index.mjs:7444): settle the head request, drop it, and synchronously resume the next queued request.
func (g *tslibAsyncGenerator) settle(settle func(*tslibRequest)) {
	settle(g.queue[0])
	g.queue[0] = nil
	g.queue = g.queue[1:]
	if len(g.queue) > 0 {
		next := g.queue[0]
		_ = g.resume(next.kind, next.value, next.err)
	}
}

// call is the inner generator's next, throw or return method.
func (g *tslibAsyncGenerator) call(resume tslibResume) (tslibIterResult, error) {
	switch g.state {
	case tslibCompleted:
		switch resume.kind {
		case tslibResumeReturn:
			return tslibIterResult{value: resume.value, done: true}, nil
		case tslibResumeThrow:
			return tslibIterResult{}, resume.err
		}
		return tslibIterResult{done: true}, nil
	case tslibSuspendedStart:
		switch resume.kind {
		case tslibResumeReturn:
			g.state = tslibCompleted
			return tslibIterResult{value: resume.value, done: true}, nil
		case tslibResumeThrow:
			g.state = tslibCompleted
			return tslibIterResult{}, resume.err
		}
		g.y = &tslibYield{}
		g.pull, g.stop = iter.Pull(g.run)
	}
	g.y.in = resume
	out, suspended := g.pull()
	if suspended {
		g.state = tslibSuspendedYield
		return tslibIterResult{value: out}, nil
	}
	value, err := g.retValue, g.retErr
	g.state = tslibCompleted
	g.stop()
	if err != nil {
		return tslibIterResult{}, err
	}
	return tslibIterResult{value: value, done: true}, nil
}

// run is the coroutine that hosts the body. Unwinding signals end it with the completion they carry.
func (g *tslibAsyncGenerator) run(yield func(any) bool) {
	g.y.yield = yield
	defer func() {
		switch signal := recover().(type) {
		case nil:
		case tslibReturnSignal:
			g.retValue, g.retErr = signal.value, nil
		case tslibThrowSignal:
			g.retValue, g.retErr = nil, signal.err
		case tslibAbortSignal:
			g.retValue, g.retErr = nil, nil
		default:
			panic(signal)
		}
	}()
	g.retValue, g.retErr = g.body(g.y)
}

// close releases the body's coroutine when a stream is abandoned without running the generator to completion. Deferred finally blocks in the body run without suspending again.
func (i *tslibAsyncIterator) close() {
	g := i.generator
	if g == nil || g.state == tslibCompleted {
		return
	}
	g.state = tslibCompleted
	if g.stop != nil {
		g.y.aborted = true
		g.stop()
	}
}

func (y *tslibYield) suspend(out any) (any, error) {
	if y.aborted || !y.yield(out) {
		y.aborted = true
		panic(tslibAbortSignal{})
	}
	switch y.in.kind {
	case tslibResumeThrow:
		return nil, y.in.err
	case tslibResumeReturn:
		panic(tslibReturnSignal{value: y.in.value})
	}
	return y.in.value, nil
}

// Await is `yield __await(v)`. It returns the fulfilled value, or the rejection as an error thrown at the yield.
func (y *tslibYield) Await(v any) (any, error) { return y.suspend(&tslibAwaited{v: v}) }

// Yield is a plain `yield v`. It returns the next() argument, or the error a throw() request injects. A return() request unwinds the body so deferred finally blocks run.
func (y *tslibYield) Yield(v any) (any, error) { return y.suspend(v) }

// YieldAwaited is `yield yield __await(v)`: await v, then yield the result to the consumer.
func (y *tslibYield) YieldAwaited(v any) (any, error) {
	awaited, err := y.Await(v)
	if err != nil {
		return nil, err
	}
	return y.Yield(awaited)
}

// Throw replaces the completion being unwound with a thrown error, as `throw` inside a finally block does.
func (y *tslibYield) Throw(err error) { panic(tslibThrowSignal{err: err}) }

// tslibAsyncValues is __asyncValues(o) for an async iterable: `m.call(o)` returns the iterator itself (index.mjs:7449).
func tslibAsyncValues(iterator *tslibAsyncIterator) *tslibAsyncIterator { return iterator }

// tslibAsyncValuesFromSync is __asyncValues(o) for a sync iterable: each method wraps the sync result in Promise.resolve(value).then(...) (index.mjs:7449-7451).
func tslibAsyncValuesFromSync(executor *continuationExecutor, source *tslibSyncIterator) *tslibAsyncIterator {
	verb := func(method func(any) tslibIterResult) func(any) *tslibPromise {
		return func(value any) *tslibPromise {
			promise := newTslibPromise(executor)
			result := method(value)
			tslibPromiseResolve(executor, result.value).then(func(v any) (any, error) {
				promise.resolve(tslibIterResult{value: v, done: result.done})
				return nil, nil
			}, func(err error) (any, error) {
				promise.reject(err)
				return nil, nil
			})
			return promise
		}
	}
	iterator := &tslibAsyncIterator{next: verb(source.next)}
	if source.ret != nil {
		iterator.ret = verb(source.ret)
	}
	return iterator
}

var errTslibIterResult = errors.New("iterator result is not an object")

// ForAwait is the `for await` loop tsc emits inside a generator body (index.mjs:7735-7752):
//
//	for (var _f = true, it = __asyncValues(src), r; r = yield __await(it.next()), _a = r.done, !_a; _f = true) { ... }
//	catch (e_1_1) { e_1 = { error: e_1_1 } }
//	finally { try { if (!_f && !_a && (_b = it.return)) yield __await(_b.call(it)); } finally { if (e_1) throw e_1.error; } }
//
// each receives every value and returns stop=true for `break` or `return`, or an error for a throw. A next() rejection, an each error and an error thrown by a nested finally all reach the catch. The iterator's return() runs only when the loop exits early, including when a generator return() request unwinds each. The finally block rethrows the caught error, which replaces an error from return().
func (y *tslibYield) ForAwait(source *tslibAsyncIterator, each func(value any) (stop bool, err error)) (loopErr error) {
	first, done := true, false
	normal := false
	var caught error
	defer func() {
		var closeErr error
		if !first && !done && source.ret != nil {
			_, closeErr = y.Await(source.ret(nil))
		}
		thrown := closeErr
		if caught != nil {
			thrown = caught
		}
		switch {
		case thrown == nil:
		case normal:
			loopErr = thrown
		default:
			y.Throw(thrown)
		}
	}()
	caught = func() error {
		for ; ; first = true {
			result, err := y.Await(source.next(nil))
			if err != nil {
				return err
			}
			iterResult, ok := result.(tslibIterResult)
			if !ok {
				return errTslibIterResult
			}
			done = iterResult.done
			if done {
				return nil
			}
			first = false
			stop, err := tslibCatchThrown(func() (bool, error) { return each(iterResult.value) })
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
		}
	}()
	normal = true
	return nil
}

// tslibCatchThrown converts a tslibThrowSignal raised by a nested finally block into the error a JavaScript catch clause would see. Return and abort signals pass through untouched.
func tslibCatchThrown(run func() (bool, error)) (stop bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			signal, ok := r.(tslibThrowSignal)
			if !ok {
				panic(r)
			}
			stop, err = false, signal.err
		}
	}()
	return run()
}

// tslibForAwait is a native `for await` in an async function turn. It awaits each next() result, and on an early exit runs AsyncIteratorClose: return() is called and awaited, and a throw completion from each wins over an error from return() (ECMA-262 7.4.13).
func tslibForAwait(turn *continuationTurn, source *tslibAsyncIterator, each func(value any) (stop bool, err error)) error {
	for {
		settled := awaitContinuation(turn, source.next(nil).settled)
		if settled.err != nil {
			return settled.err
		}
		result, ok := settled.value.(tslibIterResult)
		if !ok {
			return errTslibIterResult
		}
		if result.done {
			return nil
		}
		stop, err := each(result.value)
		if err == nil && !stop {
			continue
		}
		if source.ret == nil {
			return err
		}
		closed := awaitContinuation(turn, source.ret(nil).settled)
		switch {
		case err != nil:
			return err
		case closed.err != nil:
			return closed.err
		}
		if _, ok := closed.value.(tslibIterResult); !ok {
			return errTslibIterResult
		}
		return nil
	}
}
