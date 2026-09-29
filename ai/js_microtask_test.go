package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"testing"
)

// The scenarios below mirror ai/testdata/microtask/probe.mjs one to one. The probe prints the exact microtask order Node produces; every Go scenario must print the same entries, so a reaction the model adds or drops fails the scenario that exercises it.

type jsProbe struct {
	executor *continuationExecutor
	logs     []string
}

type jsOp = jsOperand[int]

func (p *jsProbe) log(format string, args ...any) {
	p.logs = append(p.logs, fmt.Sprintf(format, args...))
}

// ticker queues the reaction chain t0, t1, ... that measures every other entry's depth.
func (p *jsProbe) ticker() {
	chain := jsResolved(p.executor, 0)
	for i := range 16 {
		chain = jsThen(chain, func(int) (jsOp, error) {
			p.log("t%d", i)
			return jsValue(0), nil
		}, nil)
	}
}

func (p *jsProbe) atTick(count int, fn func()) {
	chain := jsResolved(p.executor, 0)
	for range count {
		chain = jsThen(chain, func(int) (jsOp, error) { return jsValue(0), nil }, nil)
	}
	jsThen(chain, func(int) (jsOp, error) {
		fn()
		return jsValue(0), nil
	}, nil)
}

func (p *jsProbe) thenableOf(operand jsOp, tag string) jsThenable[int] {
	return jsFuncThenable[int](func(resolve func(jsOp), _ func(error)) error {
		p.log("%s then called", tag)
		resolve(operand)
		return nil
	})
}

func (p *jsProbe) thenable(value int) jsThenable[int] {
	return p.thenableOf(jsValue(value), "thenable")
}

func (p *jsProbe) lateThenable(value, count int) jsThenable[int] {
	return jsFuncThenable[int](func(resolve func(jsOp), _ func(error)) error {
		p.log("late then called")
		p.atTick(count, func() { resolve(jsValue(value)) })
		return nil
	})
}

func (p *jsProbe) throwingThenable() jsThenable[int] {
	return jsFuncThenable[int](func(func(jsOp), func(error)) error { return errors.New("then threw") })
}

func (p *jsProbe) pendingAt(value, count int) *jsPromise[int] {
	promise := newJSPromise[int](p.executor)
	p.atTick(count, func() { promise.resolve(jsValue(value)) })
	return promise
}

func (p *jsProbe) resolved(value int) *jsPromise[int] { return jsResolved(p.executor, value) }

func (p *jsProbe) rejected(message string) *jsPromise[int] {
	return jsRejected[int](p.executor, errors.New(message))
}

// after is promise.then((v) => log(...)) with a handler that returns undefined.
func (p *jsProbe) after(promise *jsPromise[int], format string) *jsPromise[int] {
	return jsThen(promise, func(value int) (jsOp, error) {
		p.log(format, value)
		return jsValue(0), nil
	}, nil)
}

func (p *jsProbe) never(promise *jsPromise[int]) *jsPromise[int] {
	return jsThen(promise, func(int) (jsOp, error) {
		p.log("never")
		return jsValue(0), nil
	}, nil)
}

func (p *jsProbe) catchLog(promise *jsPromise[int], tag string) *jsPromise[int] {
	return jsThen(promise, nil, func(err error) (jsOp, error) {
		p.log("%s caught %s", tag, err)
		return jsValue(0), nil
	})
}

func (p *jsProbe) settled(promise *jsPromise[int], format, tag string) {
	jsThen(promise, func(value int) (jsOp, error) {
		p.log(format, value)
		return jsValue(0), nil
	}, func(err error) (jsOp, error) {
		p.log("%s caught %s", tag, err)
		return jsValue(0), nil
	})
}

func (p *jsProbe) spawn(body func(co *jsCoroutine)) {
	runJSAsync(p.executor, func(co *jsCoroutine) (jsOp, error) {
		body(co)
		return jsValue(0), nil
	})
}

func showJSResult(result jsIterResult[int]) string {
	if result.value == 0 {
		return fmt.Sprintf("undefined:%t", result.done)
	}
	return fmt.Sprintf("%d:%t", result.value, result.done)
}

// result is promise.then(logResult(tag)); results is promise.then(logResult(tag), logError(tag)).
func (p *jsProbe) result(promise *jsPromise[jsIterResult[int]], tag string) {
	jsThen(promise, func(result jsIterResult[int]) (jsOperand[int], error) {
		p.log("%s %s", tag, showJSResult(result))
		return jsValue(0), nil
	}, nil)
}

func (p *jsProbe) resultOrError(promise *jsPromise[jsIterResult[int]], tag string) {
	jsThen(promise, func(result jsIterResult[int]) (jsOperand[int], error) {
		p.log("%s %s", tag, showJSResult(result))
		return jsValue(0), nil
	}, func(err error) (jsOperand[int], error) {
		p.log("%s caught %s", tag, err)
		return jsValue(0), nil
	})
}

func (p *jsProbe) generator(body func(b *jsGeneratorBody[int]) (int, error)) *jsAsyncGenerator[int] {
	return newJSAsyncGenerator(p.executor, body)
}

type jsAwaitScenario func(p *jsProbe, awaiter jsAwaiter)

// jsAwaitScenarios run identically in a coroutine (an async function) and in a turn, the two awaiters the model offers. They contain no synchronous statement after the first await starts, which a blocking turn cannot express.
var jsAwaitScenarios = map[string]jsAwaitScenario{
	"await-settled-promise": func(p *jsProbe, a jsAwaiter) {
		v, _ := jsAwait(a, jsPromiseOperand(p.resolved(2)))
		p.log("after %d", v)
	},
	"await-pending-promise": func(p *jsProbe, a jsAwaiter) {
		v, _ := jsAwait(a, jsPromiseOperand(p.pendingAt(3, 3)))
		p.log("after %d", v)
	},
	"await-thenable": func(p *jsProbe, a jsAwaiter) {
		v, _ := jsAwait(a, jsThenableOperand(p.thenable(4)))
		p.log("after %d", v)
	},
	"await-late-thenable": func(p *jsProbe, a jsAwaiter) {
		v, _ := jsAwait(a, jsThenableOperand(p.lateThenable(4, 2)))
		p.log("after %d", v)
	},
	"await-rejected-caught": func(p *jsProbe, a jsAwaiter) {
		if _, err := jsAwait(a, jsPromiseOperand(p.rejected("x"))); err != nil {
			p.log("caught %s", err)
		} else {
			p.log("never")
		}
	},
	"await-thenable-throws": func(p *jsProbe, a jsAwaiter) {
		if _, err := jsAwait(a, jsThenableOperand(p.throwingThenable())); err != nil {
			p.log("caught %s", err)
		} else {
			p.log("never")
		}
	},
	"await-sequence": func(p *jsProbe, a jsAwaiter) {
		_, _ = jsAwait(a, jsValue(1))
		p.log("one")
		_, _ = jsAwait(a, jsPromiseOperand(p.resolved(0)))
		p.log("two")
		_, _ = jsAwait(a, jsThenableOperand(p.thenable(0)))
		p.log("three")
	},
	"gen-forawait-consumer": func(p *jsProbe, a jsAwaiter) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			for i := 1; i <= 3; i++ {
				if err := b.yield(jsValue(i)); err != nil {
					return 0, err
				}
			}
			return 0, nil
		})
		defer generator.dispose()
		for {
			result, _ := jsAwait(a, jsPromiseOperand(generator.next()))
			if result.done {
				break
			}
			p.log("item %d", result.value)
		}
		p.log("loop done")
	},
}

var jsScenarios = map[string]func(p *jsProbe){
	// Primitive 1: Promise, resolve, then, thenable adoption.
	"then-settled": func(p *jsProbe) {
		p.after(p.resolved(1), "a %d")
		p.log("sync")
	},
	"then-chain": func(p *jsProbe) {
		step := func(name string) func(int) (jsOp, error) {
			return func(v int) (jsOp, error) {
				p.log("%s %d", name, v)
				return jsValue(v + 1), nil
			}
		}
		chain := jsThen(p.resolved(1), step("a"), nil)
		chain = jsThen(chain, step("b"), nil)
		p.after(chain, "c %d")
	},
	"then-returns-promise": func(p *jsProbe) {
		chain := jsThen(p.resolved(0), func(int) (jsOp, error) { return jsPromiseOperand(p.resolved(1)), nil }, nil)
		p.after(chain, "after %d")
	},
	"then-returns-pending-promise": func(p *jsProbe) {
		chain := jsThen(p.resolved(0), func(int) (jsOp, error) { return jsPromiseOperand(p.pendingAt(2, 3)), nil }, nil)
		p.after(chain, "after %d")
	},
	"then-returns-thenable": func(p *jsProbe) {
		chain := jsThen(p.resolved(0), func(int) (jsOp, error) { return jsThenableOperand(p.thenable(3)), nil }, nil)
		p.after(chain, "after %d")
	},
	"then-returns-late-thenable": func(p *jsProbe) {
		chain := jsThen(p.resolved(0), func(int) (jsOp, error) { return jsThenableOperand(p.lateThenable(4, 2)), nil }, nil)
		p.after(chain, "after %d")
	},
	"resolve-with-promise": func(p *jsProbe) {
		promise := newJSPromise[int](p.executor)
		promise.resolve(jsPromiseOperand(p.resolved(1)))
		p.after(promise, "after %d")
	},
	"resolve-with-thenable": func(p *jsProbe) {
		promise := newJSPromise[int](p.executor)
		promise.resolve(jsThenableOperand(p.thenable(2)))
		p.after(promise, "after %d")
		p.log("sync")
	},
	"resolve-with-thenable-of-thenable": func(p *jsProbe) {
		promise := newJSPromise[int](p.executor)
		promise.resolve(jsThenableOperand(p.thenableOf(jsThenableOperand(p.thenableOf(jsValue(5), "inner")), "outer")))
		p.after(promise, "after %d")
	},
	"resolve-with-promise-then-reject-ignored": func(p *jsProbe) {
		promise := newJSPromise[int](p.executor)
		promise.resolve(jsPromiseOperand(p.resolved(1)))
		promise.reject(errors.New("ignored"))
		promise.resolve(jsValue(2))
		p.settled(promise, "after %d", "rejected")
	},
	"resolve-with-pending-promise": func(p *jsProbe) {
		promise := newJSPromise[int](p.executor)
		promise.resolve(jsPromiseOperand(p.pendingAt(6, 2)))
		p.after(promise, "after %d")
	},
	"resolve-first-wins": func(p *jsProbe) {
		promise := newJSPromise[int](p.executor)
		promise.resolve(jsValue(1))
		promise.resolve(jsValue(2))
		p.after(promise, "after %d")
	},
	"resolve-self-cycle": func(p *jsProbe) {
		promise := newJSPromise[int](p.executor)
		promise.resolve(jsPromiseOperand(promise))
		jsThen(promise, func(int) (jsOp, error) {
			p.log("never")
			return jsValue(0), nil
		}, func(err error) (jsOp, error) {
			p.log("caught TypeError: %s", err)
			return jsValue(0), nil
		})
	},
	"reject-passthrough": func(p *jsProbe) {
		p.catchLog(p.never(p.never(p.rejected("boom"))), "h")
	},
	"reject-handled-then-continues": func(p *jsProbe) {
		handled := jsThen(p.rejected("boom"), func(int) (jsOp, error) {
			p.log("never")
			return jsValue(0), nil
		}, func(err error) (jsOp, error) {
			p.log("r %s", err)
			return jsValue(1), nil
		})
		p.after(handled, "after %d")
	},
	"handler-throws": func(p *jsProbe) {
		thrown := jsThen(p.resolved(1), func(int) (jsOp, error) { return jsValue(0), errors.New("thrown") }, nil)
		p.catchLog(p.never(thrown), "h")
	},
	"thenable-then-throws": func(p *jsProbe) {
		chain := jsThen(p.resolved(0), func(int) (jsOp, error) { return jsThenableOperand(p.throwingThenable()), nil }, nil)
		p.settled(chain, "never %d", "h")
	},
	"thenable-throws-after-resolve": func(p *jsProbe) {
		promise := newJSPromise[int](p.executor)
		promise.resolve(jsThenableOperand(jsFuncThenable[int](func(resolve func(jsOp), _ func(error)) error {
			resolve(jsValue(8))
			return errors.New("ignored")
		})))
		p.settled(promise, "after %d", "h")
	},
	"reactions-in-registration-order": func(p *jsProbe) {
		promise := p.pendingAt(1, 3)
		p.after(promise, "a %d")
		p.after(promise, "b %d")
		jsThen(p.resolved(0), func(int) (jsOp, error) {
			p.after(promise, "c %d")
			return jsValue(0), nil
		}, nil)
	},
	"then-on-settled-inside-reaction": func(p *jsProbe) {
		outer := jsThen(p.resolved(0), func(int) (jsOp, error) {
			p.log("outer")
			jsThen(p.resolved(0), func(int) (jsOp, error) {
				p.log("inner")
				return jsValue(0), nil
			}, nil)
			return jsValue(0), nil
		}, nil)
		jsThen(outer, func(int) (jsOp, error) {
			p.log("outer-next")
			return jsValue(0), nil
		}, nil)
	},

	// Primitive 2: await and async function return.
	"await-plain": func(p *jsProbe) {
		p.spawn(func(co *jsCoroutine) {
			p.log("start")
			_, _ = jsAwait(co, jsValue(1))
			p.log("after")
		})
		p.log("sync")
	},
	"await-two-functions-interleave": func(p *jsProbe) {
		p.spawn(func(co *jsCoroutine) {
			_, _ = jsAwait(co, jsValue(1))
			p.log("a1")
			_, _ = jsAwait(co, jsValue(1))
			p.log("a2")
			_, _ = jsAwait(co, jsValue(1))
			p.log("a3")
		})
		p.spawn(func(co *jsCoroutine) {
			_, _ = jsAwait(co, jsValue(1))
			p.log("b1")
			_, _ = jsAwait(co, jsPromiseOperand(p.resolved(0)))
			p.log("b2")
		})
	},
	"async-return-value": func(p *jsProbe) {
		p.after(runJSAsync(p.executor, func(*jsCoroutine) (jsOp, error) { return jsValue(1), nil }), "after %d")
	},
	"async-return-promise": func(p *jsProbe) {
		p.after(runJSAsync(p.executor, func(*jsCoroutine) (jsOp, error) { return jsPromiseOperand(p.resolved(1)), nil }), "after %d")
	},
	"async-return-await-promise": func(p *jsProbe) {
		p.after(runJSAsync(p.executor, func(co *jsCoroutine) (jsOp, error) {
			v, err := jsAwait(co, jsPromiseOperand(p.resolved(1)))
			return jsValue(v), err
		}), "after %d")
	},
	"async-return-pending-promise": func(p *jsProbe) {
		p.after(runJSAsync(p.executor, func(*jsCoroutine) (jsOp, error) { return jsPromiseOperand(p.pendingAt(1, 2)), nil }), "after %d")
	},
	"async-return-thenable": func(p *jsProbe) {
		p.after(runJSAsync(p.executor, func(*jsCoroutine) (jsOp, error) { return jsThenableOperand(p.thenable(1)), nil }), "after %d")
	},
	"async-throw": func(p *jsProbe) {
		p.catchLog(runJSAsync(p.executor, func(*jsCoroutine) (jsOp, error) { return jsValue(0), errors.New("sync throw") }), "h")
	},
	"async-throw-after-await": func(p *jsProbe) {
		p.catchLog(runJSAsync(p.executor, func(co *jsCoroutine) (jsOp, error) {
			_, _ = jsAwait(co, jsValue(1))
			return jsValue(0), errors.New("late throw")
		}), "h")
	},
	"async-await-async": func(p *jsProbe) {
		inner := func() *jsPromise[int] {
			return runJSAsync(p.executor, func(co *jsCoroutine) (jsOp, error) {
				_, _ = jsAwait(co, jsValue(1))
				return jsValue(5), nil
			})
		}
		p.spawn(func(co *jsCoroutine) {
			v, _ := jsAwait(co, jsPromiseOperand(inner()))
			p.log("outer %d", v)
		})
	},
	"async-return-async": func(p *jsProbe) {
		inner := func() *jsPromise[int] {
			return runJSAsync(p.executor, func(co *jsCoroutine) (jsOp, error) {
				_, _ = jsAwait(co, jsValue(1))
				return jsValue(5), nil
			})
		}
		p.after(runJSAsync(p.executor, func(*jsCoroutine) (jsOp, error) { return jsPromiseOperand(inner()), nil }), "after %d")
	},
	"async-await-rejected-async": func(p *jsProbe) {
		inner := func() *jsPromise[int] {
			return runJSAsync(p.executor, func(co *jsCoroutine) (jsOp, error) {
				_, _ = jsAwait(co, jsValue(1))
				return jsValue(0), errors.New("inner")
			})
		}
		p.spawn(func(co *jsCoroutine) {
			if _, err := jsAwait(co, jsPromiseOperand(inner())); err != nil {
				p.log("caught %s", err)
			}
		})
	},

	// Primitive 3: native async generators.
	"gen-next-sequential": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			p.log("body start")
			if err := b.yield(jsValue(1)); err != nil {
				return 0, err
			}
			p.log("body 2")
			if err := b.yield(jsValue(2)); err != nil {
				return 0, err
			}
			p.log("body end")
			return 0, nil
		})
		p.spawn(func(co *jsCoroutine) {
			for i := 1; i <= 4; i++ {
				result, _ := jsAwait(co, jsPromiseOperand(generator.next()))
				p.log("r%d %s", i, showJSResult(result))
			}
		})
		p.log("sync")
	},
	"gen-next-queued": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			p.log("body start")
			if err := b.yield(jsValue(1)); err != nil {
				return 0, err
			}
			p.log("body 2")
			if err := b.yield(jsValue(2)); err != nil {
				return 0, err
			}
			p.log("body end")
			return 0, nil
		})
		for i := 1; i <= 4; i++ {
			p.result(generator.next(), fmt.Sprintf("r%d", i))
		}
		p.log("sync")
	},
	"gen-yield-promise": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			if err := b.yield(jsPromiseOperand(p.resolved(5))); err != nil {
				return 0, err
			}
			p.log("sent undefined")
			return 0, nil
		})
		p.result(generator.next(), "r1")
		p.result(generator.next(), "r2")
	},
	"gen-yield-pending-promise": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			if err := b.yield(jsPromiseOperand(p.pendingAt(5, 3))); err != nil {
				return 0, err
			}
			p.log("after yield")
			return 0, nil
		})
		p.result(generator.next(), "r1")
		p.result(generator.next(), "r2")
	},
	"gen-yield-thenable": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			return 0, b.yield(jsThenableOperand(p.thenable(9)))
		})
		p.result(generator.next(), "r1")
		p.result(generator.next(), "r2")
	},
	"gen-yield-rejected-caught": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			if err := b.yield(jsPromiseOperand(p.rejected("bad"))); err != nil {
				p.log("body caught %s", err)
			} else {
				p.log("never")
			}
			return 0, b.yield(jsValue(2))
		})
		p.result(generator.next(), "r1")
		p.result(generator.next(), "r2")
		p.result(generator.next(), "r3")
	},
	"gen-yield-rejected-uncaught": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			if err := b.yield(jsPromiseOperand(p.rejected("bad"))); err != nil {
				return 0, err
			}
			p.log("never")
			return 0, nil
		})
		p.resultOrError(generator.next(), "r1")
		p.resultOrError(generator.next(), "r2")
	},
	"gen-return-value": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			if err := b.yield(jsValue(1)); err != nil {
				return 0, err
			}
			return b.returnValue(jsValue(7))
		})
		for i := 1; i <= 3; i++ {
			p.result(generator.next(), fmt.Sprintf("r%d", i))
		}
	},
	"gen-return-promise": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			return b.returnValue(jsPromiseOperand(p.resolved(7)))
		})
		p.result(generator.next(), "r1")
	},
	"gen-return-pending-promise": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			return b.returnValue(jsPromiseOperand(p.pendingAt(7, 3)))
		})
		p.result(generator.next(), "r1")
	},
	"gen-return-rejected": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			return b.returnValue(jsPromiseOperand(p.rejected("rej")))
		})
		p.resultOrError(generator.next(), "r1")
		p.result(generator.next(), "r2")
	},
	"gen-empty": func(p *jsProbe) {
		generator := p.generator(func(*jsGeneratorBody[int]) (int, error) { return 0, nil })
		p.result(generator.next(), "r1")
		p.result(generator.next(), "r2")
	},
	"gen-body-throws": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			if err := b.yield(jsValue(1)); err != nil {
				return 0, err
			}
			return 0, errors.New("body threw")
		})
		p.resultOrError(generator.next(), "r1")
		p.resultOrError(generator.next(), "r2")
		p.resultOrError(generator.next(), "r3")
	},
	"gen-return-at-start": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			p.log("never")
			return 0, b.yield(jsValue(1))
		})
		p.result(generator.returnWith(jsValue(9)), "r1")
		p.result(generator.next(), "r2")
	},
	"gen-return-at-start-promise": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) { return 0, b.yield(jsValue(1)) })
		p.result(generator.returnWith(jsPromiseOperand(p.resolved(3))), "r1")
	},
	"gen-return-at-start-pending-promise": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) { return 0, b.yield(jsValue(1)) })
		p.result(generator.returnWith(jsPromiseOperand(p.pendingAt(3, 3))), "r1")
	},
	"gen-return-at-start-rejected": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) { return 0, b.yield(jsValue(1)) })
		p.resultOrError(generator.returnWith(jsPromiseOperand(p.rejected("rej"))), "r1")
	},
	"gen-return-at-start-thenable": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) { return 0, b.yield(jsValue(1)) })
		p.result(generator.returnWith(jsThenableOperand(p.thenable(4))), "r1")
	},
	"gen-return-completed": func(p *jsProbe) {
		generator := p.generator(func(*jsGeneratorBody[int]) (int, error) { return 0, nil })
		p.result(generator.next(), "r1")
		p.result(generator.returnWith(jsValue(9)), "r2")
	},
	"gen-return-suspended-yield": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			err := b.yield(jsValue(1))
			if err == nil {
				p.log("never")
			}
			p.log("finally")
			return 0, err
		})
		p.result(generator.next(), "r1")
		p.result(generator.returnWith(jsValue(8)), "r2")
		p.result(generator.next(), "r3")
	},
	"gen-return-suspended-yield-promise": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) { return 0, b.yield(jsValue(1)) })
		p.result(generator.next(), "r1")
		p.result(generator.returnWith(jsPromiseOperand(p.resolved(8))), "r2")
	},
	"gen-return-suspended-yield-rejected": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			if err := b.yield(jsValue(1)); err != nil {
				p.log("body caught %s", err)
				return 0, b.yield(jsValue(4))
			}
			return 0, nil
		})
		p.result(generator.next(), "r1")
		p.resultOrError(generator.returnWith(jsPromiseOperand(p.rejected("rej"))), "r2")
		p.result(generator.next(), "r3")
	},
	"gen-return-finally-awaits": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			err := b.yield(jsValue(1))
			p.log("finally start")
			_, _ = jsAwait(b, jsValue(1))
			p.log("finally end")
			return 0, err
		})
		p.result(generator.next(), "r1")
		p.result(generator.returnWith(jsValue(8)), "r2")
	},
	"gen-return-finally-yields": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			err := b.yield(jsValue(1))
			p.log("finally start")
			if inner := b.yield(jsValue(1)); inner != nil {
				return 0, inner
			}
			p.log("finally end")
			return 0, err
		})
		p.result(generator.next(), "r1")
		p.result(generator.returnWith(jsValue(8)), "r2")
		p.result(generator.next(), "r3")
	},
	"gen-throw-at-start": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			p.log("never")
			return 0, b.yield(jsValue(1))
		})
		p.resultOrError(generator.throw(errors.New("t")), "r1")
		p.result(generator.next(), "r2")
	},
	"gen-throw-suspended-yield-caught": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			if err := b.yield(jsValue(1)); err != nil {
				p.log("body caught %s", err)
				return 0, b.yield(jsValue(2))
			}
			return 0, nil
		})
		p.result(generator.next(), "r1")
		p.resultOrError(generator.throw(errors.New("t")), "r2")
		p.result(generator.next(), "r3")
	},
	"gen-throw-suspended-yield-uncaught": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) { return 0, b.yield(jsValue(1)) })
		p.result(generator.next(), "r1")
		p.resultOrError(generator.throw(errors.New("t")), "r2")
		p.result(generator.next(), "r3")
	},
	"gen-throw-completed": func(p *jsProbe) {
		generator := p.generator(func(*jsGeneratorBody[int]) (int, error) { return 0, nil })
		p.result(generator.next(), "r1")
		p.resultOrError(generator.throw(errors.New("t")), "r2")
	},
	"gen-await-in-body-queued": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			p.log("body start")
			_, _ = jsAwait(b, jsValue(1))
			p.log("body after await")
			if err := b.yield(jsValue(1)); err != nil {
				return 0, err
			}
			_, _ = jsAwait(b, jsPromiseOperand(p.pendingAt(0, 2)))
			p.log("body after pending")
			return 0, b.yield(jsValue(2))
		})
		for i := 1; i <= 3; i++ {
			p.result(generator.next(), fmt.Sprintf("r%d", i))
		}
	},
	"gen-queued-return-while-executing": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			_, _ = jsAwait(b, jsValue(1))
			err := b.yield(jsValue(1))
			if err == nil {
				p.log("after yield")
			}
			p.log("finally")
			return 0, err
		})
		p.result(generator.next(), "r1")
		p.result(generator.next(), "r2")
		p.result(generator.returnWith(jsValue(4)), "r3")
		p.result(generator.next(), "r4")
	},
	"gen-queued-throw-while-executing": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			_, _ = jsAwait(b, jsValue(1))
			if err := b.yield(jsValue(1)); err != nil {
				return 0, err
			}
			return 0, b.yield(jsValue(2))
		})
		p.result(generator.next(), "r1")
		p.resultOrError(generator.throw(errors.New("t")), "r2")
		p.result(generator.next(), "r3")
	},
	"gen-next-during-awaiting-return": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) { return 0, b.yield(jsValue(1)) })
		p.result(generator.returnWith(jsPromiseOperand(p.pendingAt(3, 3))), "r1")
		p.result(generator.next(), "r2")
		p.result(generator.returnWith(jsValue(5)), "r3")
		p.resultOrError(generator.throw(errors.New("t")), "r4")
		p.result(generator.next(), "r5")
	},
	"gen-drain-queue-after-body-completes": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			_, _ = jsAwait(b, jsValue(1))
			return b.returnValue(jsValue(5))
		})
		p.result(generator.next(), "r1")
		p.result(generator.next(), "r2")
		p.resultOrError(generator.throw(errors.New("t")), "r3")
		p.result(generator.next(), "r4")
		p.result(generator.returnWith(jsValue(6)), "r5")
		p.result(generator.next(), "r6")
	},
	"gen-yields-continue-without-suspend": func(p *jsProbe) {
		generator := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			for i, name := range []string{"a", "b", "c"} {
				p.log("%s", name)
				if err := b.yield(jsValue(i + 1)); err != nil {
					return 0, err
				}
			}
			p.log("d")
			return 0, nil
		})
		for i := 1; i <= 4; i++ {
			p.result(generator.next(), fmt.Sprintf("r%d", i))
		}
	},
	"gen-chain": func(p *jsProbe) {
		inner := func() *jsAsyncGenerator[int] {
			return p.generator(func(b *jsGeneratorBody[int]) (int, error) {
				if err := b.yield(jsValue(1)); err != nil {
					return 0, err
				}
				return 0, b.yield(jsValue(2))
			})
		}
		outer := p.generator(func(b *jsGeneratorBody[int]) (int, error) {
			source := inner()
			for {
				result, err := jsAwait(b, jsPromiseOperand(source.next()))
				if err != nil {
					return 0, err
				}
				if result.done {
					return 0, nil
				}
				if err := b.yield(jsValue(result.value * 10)); err != nil {
					return 0, err
				}
			}
		})
		p.spawn(func(co *jsCoroutine) {
			for {
				result, _ := jsAwait(co, jsPromiseOperand(outer.next()))
				if result.done {
					break
				}
				p.log("item %d", result.value)
			}
			p.log("loop done")
		})
	},
}

func loadJSMicrotaskGolden(t *testing.T) map[string][]string {
	t.Helper()
	data, err := os.ReadFile("testdata/microtask/ticks.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string][]string
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

func runJSScenario(t *testing.T, run func(p *jsProbe, turn *continuationTurn)) []string {
	t.Helper()
	probe := &jsProbe{executor: &continuationExecutor{}}
	probe.executor.run(func(turn *continuationTurn) {
		probe.ticker()
		run(probe, turn)
	})
	assertJSExecutorDrained(t, probe.executor)
	return probe.logs
}

// The checked-in golden file is the probe's stdout. This test proves it is current for the Node on PATH, so the Go replays below compare against real Node output.
func TestJSMicrotaskGoldenMatchesNode(t *testing.T) {
	command := exec.CommandContext(t.Context(), "node", "ai/testdata/microtask/probe.mjs")
	command.Dir = ".."
	output, err := command.Output()
	if err != nil {
		t.Fatalf("node probe: %v", err)
	}
	want, err := os.ReadFile("testdata/microtask/ticks.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, want) {
		t.Fatalf("ticks.json is stale: rerun `node ai/testdata/microtask/probe.mjs > ai/testdata/microtask/ticks.json`")
	}
}

func TestJSMicrotaskScenariosMatchNode(t *testing.T) {
	golden := loadJSMicrotaskGolden(t)
	for name := range jsScenarios {
		if _, ok := golden[name]; !ok {
			t.Errorf("scenario %q has no probe entry", name)
		}
	}
	for name := range jsAwaitScenarios {
		if _, ok := golden[name]; !ok {
			t.Errorf("await scenario %q has no probe entry", name)
		}
	}
	for name, want := range golden {
		_, plain := jsScenarios[name]
		_, awaiting := jsAwaitScenarios[name]
		if !plain && !awaiting {
			t.Errorf("probe scenario %q has no Go replay", name)
			continue
		}
		if plain {
			t.Run(name, func(t *testing.T) {
				got := runJSScenario(t, func(p *jsProbe, _ *continuationTurn) { jsScenarios[name](p) })
				if !slices.Equal(got, want) {
					t.Fatalf("tick order differs from Node\n got: %v\nwant: %v", got, want)
				}
			})
		}
		if awaiting {
			t.Run(name+"/coroutine", func(t *testing.T) {
				got := runJSScenario(t, func(p *jsProbe, _ *continuationTurn) {
					p.spawn(func(co *jsCoroutine) { jsAwaitScenarios[name](p, co) })
				})
				if !slices.Equal(got, want) {
					t.Fatalf("tick order differs from Node\n got: %v\nwant: %v", got, want)
				}
			})
			t.Run(name+"/turn", func(t *testing.T) {
				got := runJSScenario(t, func(p *jsProbe, turn *continuationTurn) { jsAwaitScenarios[name](p, turn) })
				if !slices.Equal(got, want) {
					t.Fatalf("tick order differs from Node\n got: %v\nwant: %v", got, want)
				}
			})
		}
	}
}
