package ai

import "errors"

// jsAsyncGenerator models a native async generator object: the AsyncGenerator request queue, `yield`, `return` and `throw`. The body is straight-line Go code on a jsCoroutine. tslib's __asyncGenerator, compiled into some SDKs, costs different reactions and is modeled separately.
//
// Ports (ECMA-262 2025):
//   - 27.6.3.2 AsyncGenerator.prototype.next, .return, .throw
//   - 27.6.3.4 AsyncGeneratorCompleteStep, 27.6.3.5 AsyncGeneratorResume, 27.6.3.6 AsyncGeneratorUnwrapYieldResumption
//   - 27.6.3.7 AsyncGeneratorYield, 27.6.3.8 AsyncGeneratorAwaitReturn, 27.6.3.9 AsyncGeneratorDrainQueue
//   - 15.6.2 evaluation of `yield` and `return` in an async generator body: each awaits its operand first.

type jsGeneratorState uint8

const (
	jsGeneratorSuspendedStart jsGeneratorState = iota
	jsGeneratorSuspendedYield
	jsGeneratorExecuting
	jsGeneratorAwaitingReturn
	jsGeneratorCompleted
)

type jsGeneratorRequestKind uint8

const (
	jsGeneratorNext jsGeneratorRequestKind = iota
	jsGeneratorReturn
	jsGeneratorThrow
)

// jsIterResult is the {value, done} object a generator request resolves with.
type jsIterResult[T any] struct {
	value T
	done  bool
}

type jsGeneratorRequest[T any] struct {
	kind    jsGeneratorRequestKind
	value   jsOperand[T]
	err     error
	promise *jsPromise[jsIterResult[T]]
}

// jsGeneratorReturnCompletion is the error a body's yield returns when the generator resumes with a return completion. A body propagates it like any error, running its own finally logic first; the generator completes with its value.
type jsGeneratorReturnCompletion[T any] struct{ value T }

func (*jsGeneratorReturnCompletion[T]) Error() string { return "async generator return completion" }

type jsAsyncGenerator[T any] struct {
	executor   *continuationExecutor
	body       func(*jsGeneratorBody[T]) (T, error)
	co         *jsCoroutine
	state      jsGeneratorState
	queue      []*jsGeneratorRequest[T]
	resumption *jsGeneratorRequest[T]
}

// jsGeneratorBody is the body's view of its generator. It awaits like a coroutine.
type jsGeneratorBody[T any] struct {
	*jsCoroutine
	generator *jsAsyncGenerator[T]
}

// newJSAsyncGenerator creates a suspended-start generator. No body code runs until the first request.
func newJSAsyncGenerator[T any](executor *continuationExecutor, body func(*jsGeneratorBody[T]) (T, error)) *jsAsyncGenerator[T] {
	return &jsAsyncGenerator[T]{executor: executor, body: body}
}

// next is generator.next(). The body starts or resumes synchronously in the caller's stack when the generator is suspended; otherwise the request queues.
func (generator *jsAsyncGenerator[T]) next() *jsPromise[jsIterResult[T]] {
	promise := newJSPromise[jsIterResult[T]](generator.executor)
	if generator.state == jsGeneratorCompleted {
		promise.resolve(jsValue(jsIterResult[T]{done: true}))
		return promise
	}
	request := &jsGeneratorRequest[T]{kind: jsGeneratorNext, promise: promise}
	generator.queue = append(generator.queue, request)
	if generator.state == jsGeneratorSuspendedStart || generator.state == jsGeneratorSuspendedYield {
		generator.resume(request)
	}
	return promise
}

// return is generator.return(value). A suspended-start or completed generator awaits the value and completes; a suspended-yield generator resumes its body with a return completion.
func (generator *jsAsyncGenerator[T]) returnWith(value jsOperand[T]) *jsPromise[jsIterResult[T]] {
	promise := newJSPromise[jsIterResult[T]](generator.executor)
	request := &jsGeneratorRequest[T]{kind: jsGeneratorReturn, value: value, promise: promise}
	generator.queue = append(generator.queue, request)
	switch generator.state {
	case jsGeneratorSuspendedStart, jsGeneratorCompleted:
		generator.state = jsGeneratorAwaitingReturn
		generator.awaitReturn()
	case jsGeneratorSuspendedYield:
		generator.resume(request)
	}
	return promise
}

// throw is generator.throw(err). A suspended-start generator completes without running its body.
func (generator *jsAsyncGenerator[T]) throw(err error) *jsPromise[jsIterResult[T]] {
	promise := newJSPromise[jsIterResult[T]](generator.executor)
	if generator.state == jsGeneratorSuspendedStart {
		generator.state = jsGeneratorCompleted
	}
	if generator.state == jsGeneratorCompleted {
		promise.reject(err)
		return promise
	}
	request := &jsGeneratorRequest[T]{kind: jsGeneratorThrow, err: err, promise: promise}
	generator.queue = append(generator.queue, request)
	if generator.state == jsGeneratorSuspendedYield {
		generator.resume(request)
	}
	return promise
}

// dispose abandons a suspended body without running further reactions.
func (generator *jsAsyncGenerator[T]) dispose() {
	if generator.co != nil {
		generator.co.dispose()
	}
}

func (generator *jsAsyncGenerator[T]) resume(request *jsGeneratorRequest[T]) {
	generator.executor.assertJSRunning()
	generator.state = jsGeneratorExecuting
	generator.resumption = request
	if generator.co == nil {
		var result T
		var failure error
		generator.co = newJSCoroutine(generator.executor, func(co *jsCoroutine) {
			result, failure = generator.body(&jsGeneratorBody[T]{jsCoroutine: co, generator: generator})
		}, func() {
			generator.state = jsGeneratorCompleted
			var completion *jsGeneratorReturnCompletion[T]
			switch {
			case failure == nil:
				generator.completeStep(result, nil, true)
			case errors.As(failure, &completion):
				generator.completeStep(completion.value, nil, true)
			default:
				generator.completeStep(result, failure, true)
			}
			generator.drainQueue()
		})
	}
	generator.co.advance()
}

// completeStep resolves the oldest request's promise with a plain {value, done} result, or rejects it.
func (generator *jsAsyncGenerator[T]) completeStep(value T, err error, done bool) {
	request := generator.queue[0]
	generator.queue[0] = nil
	generator.queue = generator.queue[1:]
	if len(generator.queue) == 0 {
		generator.queue = nil
	}
	if err != nil {
		request.promise.reject(err)
		return
	}
	request.promise.resolve(jsValue(jsIterResult[T]{value: value, done: done}))
}

func (generator *jsAsyncGenerator[T]) drainQueue() {
	var zero T
	for len(generator.queue) != 0 {
		request := generator.queue[0]
		switch request.kind {
		case jsGeneratorReturn:
			generator.state = jsGeneratorAwaitingReturn
			generator.awaitReturn()
			return
		case jsGeneratorThrow:
			generator.completeStep(zero, request.err, true)
		default:
			generator.completeStep(zero, nil, true)
		}
	}
}

func (generator *jsAsyncGenerator[T]) awaitReturn() {
	request := generator.queue[0]
	var zero T
	jsPromiseResolve(generator.executor, request.value).react(func(value T) {
		generator.state = jsGeneratorCompleted
		generator.completeStep(value, nil, true)
		generator.drainQueue()
	}, func(reason error) {
		generator.state = jsGeneratorCompleted
		generator.completeStep(zero, reason, true)
		generator.drainQueue()
	})
}

// yield is `yield operand`. It awaits the operand, resolves the oldest request with it, and then either continues at once when another request is queued or suspends. It returns nil for next, the thrown error for throw, and a *jsGeneratorReturnCompletion for return, after awaiting the returned value. An awaiting failure at the yield returns the rejection.
func (body *jsGeneratorBody[T]) yield(operand jsOperand[T]) error {
	value, err := jsAwait(body, operand)
	if err != nil {
		return err
	}
	generator := body.generator
	body.suspend(func() bool {
		generator.completeStep(value, nil, false)
		if len(generator.queue) != 0 {
			generator.resumption = generator.queue[0]
			return true
		}
		generator.state = jsGeneratorSuspendedYield
		return false
	})
	switch request := generator.resumption; request.kind {
	case jsGeneratorThrow:
		return request.err
	case jsGeneratorReturn:
		awaited, err := jsAwait(body, request.value)
		if err != nil {
			return err
		}
		return &jsGeneratorReturnCompletion[T]{value: awaited}
	}
	return nil
}

// returnValue is `return operand`, which awaits the operand.
func (body *jsGeneratorBody[T]) returnValue(operand jsOperand[T]) (T, error) {
	return jsAwait(body, operand)
}
