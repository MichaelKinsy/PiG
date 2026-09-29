package ai

// tslibSettled is a settled JavaScript Promise: fulfilled with value, or rejected with err.
type tslibSettled struct {
	value any
	err   error
}

// tslibPromise is a JavaScript Promise whose reactions run on a continuationExecutor. Every reaction costs one executor reaction after the promise settles, and resolving with another tslibPromise costs the two extra reactions of NewPromiseResolveThenableJob (ECMA-262 27.2.1.3.2 and 27.2.2.2).
type tslibPromise struct {
	settled *continuationPromise[tslibSettled]
	locked  bool
}

func newTslibPromise(executor *continuationExecutor) *tslibPromise {
	return &tslibPromise{settled: newContinuationPromise[tslibSettled](executor)}
}

func (promise *tslibPromise) executor() *continuationExecutor { return promise.settled.executor }

// resolve is the resolve function handed to a Promise executor. The first resolve or reject locks the promise even while it adopts a pending promise.
func (promise *tslibPromise) resolve(value any) {
	if promise.locked {
		return
	}
	promise.locked = true
	thenable, ok := value.(*tslibPromise)
	if !ok {
		promise.settled.resolve(tslibSettled{value: value})
		return
	}
	promise.executor().post(func() { thenable.settled.onResolved(promise.settled.resolve) })
}

func (promise *tslibPromise) reject(err error) {
	if promise.locked {
		return
	}
	promise.locked = true
	promise.settled.resolve(tslibSettled{err: err})
}

// then is Promise.prototype.then. A handler's returned tslibPromise is adopted; a returned error rejects the derived promise; a nil handler passes the settled state through.
func (promise *tslibPromise) then(onFulfilled func(any) (any, error), onRejected func(error) (any, error)) *tslibPromise {
	derived := newTslibPromise(promise.executor())
	promise.settled.onResolved(func(settled tslibSettled) {
		value, err := settled.value, settled.err
		switch {
		case err == nil && onFulfilled != nil:
			value, err = onFulfilled(value)
		case err != nil && onRejected != nil:
			value, err = onRejected(err)
		}
		if err != nil {
			derived.reject(err)
			return
		}
		derived.resolve(value)
	})
	return derived
}

// tslibPromiseResolve is Promise.resolve: a tslibPromise is returned as is, any other value becomes an already fulfilled promise.
func tslibPromiseResolve(executor *continuationExecutor, value any) *tslibPromise {
	if promise, ok := value.(*tslibPromise); ok {
		return promise
	}
	promise := newTslibPromise(executor)
	promise.resolve(value)
	return promise
}
