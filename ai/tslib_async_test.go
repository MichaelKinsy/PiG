package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// tslibTrace replays ai/testdata/tslib-async/probe.mjs. Its round counter is the same self-rescheduling reaction chain the probe uses, started before the scenario body, so `label@round` lines compare microtask generations.
type tslibTrace struct {
	executor *continuationExecutor
	round    int
	events   []string
	timers   map[int][]func()
}

func (trace *tslibTrace) loop() {
	trace.round++
	timers := trace.timers[trace.round]
	delete(trace.timers, trace.round)
	for _, timer := range timers {
		timer()
	}
	if trace.round < 60 {
		trace.executor.post(trace.loop)
	}
}

func (trace *tslibTrace) log(label string) {
	trace.events = append(trace.events, fmt.Sprintf("%s@%d", label, trace.round))
}

func (trace *tslibTrace) at(rounds int, run func()) {
	trace.timers[trace.round+rounds] = append(trace.timers[trace.round+rounds], run)
}

func (trace *tslibTrace) resolved(value any) *tslibPromise {
	return tslibPromiseResolve(trace.executor, value)
}

func (trace *tslibTrace) pendingAt(rounds int, value any) *tslibPromise {
	promise := newTslibPromise(trace.executor)
	trace.at(rounds, func() { promise.resolve(value) })
	return promise
}

func (trace *tslibTrace) rejectedAt(rounds int, message string) *tslibPromise {
	promise := newTslibPromise(trace.executor)
	trace.at(rounds, func() { promise.reject(errors.New(message)) })
	return promise
}

func tslibFmtv(value any) string {
	switch v := value.(type) {
	case nil:
		return "undefined"
	case string:
		return strconv.Quote(v)
	case int:
		return strconv.Itoa(v)
	}
	panic(fmt.Sprintf("unformattable %T", value))
}

func tslibFmtr(value any) string {
	result := value.(tslibIterResult)
	return fmt.Sprintf("%s/%t", tslibFmtv(result.value), result.done)
}

// logResult is `.then((r) => log(prefix + fmtr(r)), (e) => log(rejectPrefix + e.message))`.
func (trace *tslibTrace) logResult(promise *tslibPromise, prefix, rejectPrefix string) *tslibPromise {
	var onRejected func(error) (any, error)
	if rejectPrefix != "" {
		onRejected = func(err error) (any, error) {
			trace.log(rejectPrefix + err.Error())
			return nil, nil
		}
	}
	return promise.then(func(value any) (any, error) {
		trace.log(prefix + tslibFmtr(value))
		return nil, nil
	}, onRejected)
}

func (trace *tslibTrace) basic() *tslibAsyncIterator {
	return newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
		trace.log("b:start")
		a, _ := y.Await(trace.resolved(1))
		trace.log("b:a=" + tslibFmtv(a))
		v, _ := y.YieldAwaited(a.(int) + 1)
		trace.log("b:y=" + tslibFmtv(v))
		_, _ = y.Yield("plain")
		trace.log("b:end")
		return "ret", nil
	})
}

type tslibScheduledRead struct {
	at    int
	done  bool
	value string
}

type tslibReadResult struct {
	done  bool
	value string
}

func (trace *tslibTrace) reader(specs []tslibScheduledRead) func() *tslibPromise {
	return func() *tslibPromise {
		spec := specs[0]
		specs = specs[1:]
		result := tslibReadResult{done: spec.done, value: spec.value}
		if spec.at == 0 {
			return trace.resolved(result)
		}
		return trace.pendingAt(spec.at, result)
	}
}

type tslibChunk struct {
	trace *tslibTrace
	value string
}

func (chunk tslibChunk) json() *tslibPromise {
	return chunk.trace.resolved("j:"+chunk.value).then(func(v any) (any, error) { return v, nil }, nil)
}

// ApiClient.processStreamResponse (index.mjs:13780): reader.read() loop, `yield yield __await(x)`, finally releases.
func (trace *tslibTrace) reading(read func() *tslibPromise) *tslibAsyncIterator {
	return newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
		defer trace.log("reader:release")
		for {
			read, _ := y.Await(read())
			result := read.(tslibReadResult)
			if result.done {
				return nil, nil
			}
			if _, err := y.YieldAwaited(tslibChunk{trace: trace, value: result.value}); err != nil {
				return nil, err
			}
		}
	})
}

// Models.generateContentStreamInternal map step (index.mjs:15789).
func (trace *tslibTrace) mapping(source *tslibAsyncIterator, throwOn string) *tslibAsyncIterator {
	return newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
		return nil, y.ForAwait(tslibAsyncValues(source), func(value any) (bool, error) {
			resp, err := y.Await(value.(tslibChunk).json())
			if err != nil {
				return false, err
			}
			if resp == throwOn {
				return false, errors.New("map:" + resp.(string))
			}
			_, err = y.YieldAwaited(resp)
			return false, err
		})
	})
}

// Models.generateContentStream AFC loop (index.mjs:15626).
func (trace *tslibTrace) afc(makeResponse func(any) *tslibPromise) *tslibAsyncIterator {
	return newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
		params, _ := y.Await(trace.resolved("params"))
		response, _ := y.Await(makeResponse(params))
		return nil, y.ForAwait(tslibAsyncValues(response.(*tslibAsyncIterator)), func(chunk any) (bool, error) {
			_, err := y.YieldAwaited(chunk)
			return false, err
		})
	})
}

func (trace *tslibTrace) stack(specs []tslibScheduledRead, throwOn string) *tslibAsyncIterator {
	return trace.afc(func(any) *tslibPromise {
		return trace.resolved(trace.mapping(trace.reading(trace.reader(specs)), throwOn)).then(func(v any) (any, error) { return v, nil }, nil)
	})
}

func tslibStackSpecs() []tslibScheduledRead {
	return []tslibScheduledRead{{value: "c1"}, {at: 9, value: "c2"}, {value: "c3"}, {done: true}}
}

// consume is Pi's native `for await` (google-generative-ai.ts:106), optionally leaving early.
func (trace *tslibTrace) consume(turn *continuationTurn, iterator *tslibAsyncIterator, breakAt int) {
	n := 0
	err := tslibForAwait(turn, iterator, func(value any) (bool, error) {
		trace.log("consumer:" + tslibFmtv(value))
		n++
		return n == breakAt, nil
	})
	if err != nil {
		trace.log("consumer:caught " + err.Error())
		return
	}
	trace.log("consumer:done")
}

type tslibHand struct {
	returns       bool
	nextRejectsAt int
	returnRejects bool
}

func (trace *tslibTrace) hand(items []int, options tslibHand) *tslibAsyncIterator {
	position := 0
	iterator := &tslibAsyncIterator{next: func(any) *tslibPromise {
		trace.log("it:next")
		if position == options.nextRejectsAt {
			promise := newTslibPromise(trace.executor)
			promise.reject(errors.New("next-failed"))
			return promise
		}
		if position < len(items) {
			position++
			return trace.resolved(tslibIterResult{value: items[position-1]})
		}
		return trace.resolved(tslibIterResult{done: true})
	}}
	if options.returns {
		iterator.ret = func(any) *tslibPromise {
			trace.log("it:return")
			promise := newTslibPromise(trace.executor)
			if options.returnRejects {
				promise.reject(errors.New("return-failed"))
			} else {
				promise.resolve(tslibIterResult{done: true})
			}
			return promise
		}
	}
	return iterator
}

func handOptions(mutate func(*tslibHand)) tslibHand {
	options := tslibHand{returns: true, nextRejectsAt: -1}
	if mutate != nil {
		mutate(&options)
	}
	return options
}

func (trace *tslibTrace) nativeLoop(turn *continuationTurn, iterator *tslibAsyncIterator, action func(int) error) {
	err := tslibForAwait(turn, iterator, func(value any) (bool, error) {
		trace.log("v:" + tslibFmtv(value))
		if action != nil {
			return false, action(value.(int))
		}
		return false, nil
	})
	if err != nil {
		trace.log("loop:caught " + err.Error())
		return
	}
	trace.log("loop:done")
}

func (trace *tslibTrace) nativeBreak(turn *continuationTurn, iterator *tslibAsyncIterator, at int, catch bool) {
	err := tslibForAwait(turn, iterator, func(value any) (bool, error) {
		trace.log("v:" + tslibFmtv(value))
		return value.(int) == at, nil
	})
	if err != nil {
		if catch {
			// `.catch((e) => log(...))` on the async function's rejected promise.
			rejected := newTslibPromise(trace.executor)
			rejected.reject(err)
			rejected.then(nil, func(err error) (any, error) {
				trace.log("break rejected " + err.Error())
				return nil, nil
			})
		}
		return
	}
	trace.log("loop:after")
}

func tslibArrayIterator(values ...any) *tslibSyncIterator {
	position := 0
	return &tslibSyncIterator{next: func(any) tslibIterResult {
		if position >= len(values) {
			return tslibIterResult{done: true}
		}
		position++
		return tslibIterResult{value: values[position-1]}
	}}
}

func (trace *tslibTrace) syncLoop(source *tslibAsyncIterator, breakAfterFirst bool, breakAt any) *tslibAsyncIterator {
	return newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
		return nil, y.ForAwait(source, func(value any) (bool, error) {
			if breakAt != nil && value == breakAt {
				return true, nil
			}
			if _, err := y.YieldAwaited(value); err != nil {
				return false, err
			}
			return breakAfterFirst, nil
		})
	})
}

func (trace *tslibTrace) stepAll(iterator *tslibAsyncIterator) {
	var step func()
	step = func() {
		iterator.next(nil).then(func(value any) (any, error) {
			trace.log("next -> " + tslibFmtr(value))
			if !value.(tslibIterResult).done {
				step()
			}
			return nil, nil
		}, nil)
	}
	step()
}

// looping is a tslib generator looping over a hand-written async iterator, leaving at value 2 by throw or by break.
func (trace *tslibTrace) looping(source *tslibAsyncIterator, throwAtTwo bool) *tslibAsyncIterator {
	return newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
		return nil, y.ForAwait(tslibAsyncValues(source), func(value any) (bool, error) {
			trace.log("v:" + tslibFmtv(value))
			if value == 2 {
				if throwAtTwo {
					return false, errors.New("body-failed")
				}
				return true, nil
			}
			_, err := y.YieldAwaited(value)
			return false, err
		})
	})
}

// drainCapped calls next() until the generator reports done, at most 4 times.
func (trace *tslibTrace) drainCapped(iterator *tslibAsyncIterator) {
	calls := 0
	var step func()
	step = func() {
		calls++
		if calls > 4 {
			return
		}
		iterator.next(nil).then(func(value any) (any, error) {
			trace.log("next -> " + tslibFmtr(value))
			if !value.(tslibIterResult).done {
				step()
			}
			return nil, nil
		}, func(err error) (any, error) {
			trace.log("next rejected " + err.Error())
			step()
			return nil, nil
		})
	}
	step()
}

var tslibScenarios = map[string]func(*tslibTrace, *continuationTurn){
	"gen-sequential": func(trace *tslibTrace, _ *continuationTurn) {
		iterator := trace.basic()
		var step func(int)
		step = func(n int) {
			iterator.next(n).then(func(value any) (any, error) {
				trace.log(fmt.Sprintf("next(%d) -> %s", n, tslibFmtr(value)))
				if !value.(tslibIterResult).done {
					step(n + 1)
				}
				return nil, nil
			}, nil)
		}
		step(1)
		trace.log("sync-end")
	},
	"gen-queued": func(trace *tslibTrace, _ *continuationTurn) {
		iterator := trace.basic()
		for n := 1; n <= 5; n++ {
			trace.logResult(iterator.next(n), fmt.Sprintf("next(%d) -> ", n), "")
		}
		trace.log("sync-end")
	},
	"gen-await-pending": func(trace *tslibTrace, _ *continuationTurn) {
		iterator := newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
			a, _ := y.Await(trace.pendingAt(5, "late"))
			trace.log("b:a=" + a.(string))
			_, _ = y.Yield(a)
			return nil, nil
		})
		for range 3 {
			trace.logResult(iterator.next(nil), "next -> ", "")
		}
	},
	"gen-await-rejected-caught": func(trace *tslibTrace, _ *continuationTurn) {
		iterator := newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
			if _, err := y.Await(trace.rejectedAt(3, "boom")); err != nil {
				trace.log("b:caught " + err.Error())
			}
			_, _ = y.Yield("after")
			return nil, nil
		})
		trace.logResult(iterator.next(nil), "next -> ", "")
		trace.logResult(iterator.next(nil), "next -> ", "")
	},
	"gen-await-rejected-uncaught": func(trace *tslibTrace, _ *continuationTurn) {
		iterator := newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
			if _, err := y.Await(trace.rejectedAt(3, "boom")); err != nil {
				return nil, err
			}
			_, _ = y.Yield("never")
			return nil, nil
		})
		trace.logResult(iterator.next(nil), "next -> ", "next rejected ")
		trace.logResult(iterator.next(nil), "next -> ", "next rejected ")
	},
	"gen-body-throws": func(trace *tslibTrace, _ *continuationTurn) {
		iterator := newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
			_, _ = y.Yield(1)
			return nil, errors.New("bang")
		})
		for range 3 {
			trace.logResult(iterator.next(nil), "next -> ", "next rejected ")
		}
	},
	"gen-return-before-start": func(trace *tslibTrace, _ *continuationTurn) {
		iterator := trace.basic()
		trace.logResult(iterator.ret("early"), "return -> ", "")
		trace.logResult(iterator.next(nil), "next -> ", "")
	},
	"gen-throw-before-start": func(trace *tslibTrace, _ *continuationTurn) {
		iterator := trace.basic()
		trace.logResult(iterator.throw(errors.New("x")), "throw -> ", "throw rejected ")
		trace.logResult(iterator.next(nil), "next -> ", "")
	},
	"gen-return-after-done": func(trace *tslibTrace, _ *continuationTurn) {
		iterator := newTslibAsyncGenerator(trace.executor, func(*tslibYield) (any, error) { return "r", nil })
		iterator.next(nil).then(func(value any) (any, error) {
			trace.log("next -> " + tslibFmtr(value))
			return iterator.ret("again"), nil
		}, nil).then(func(value any) (any, error) {
			trace.log("return -> " + tslibFmtr(value))
			return nil, nil
		}, nil)
	},
	"gen-return-suspended-finally-await": func(trace *tslibTrace, _ *continuationTurn) {
		iterator := newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
			defer func() {
				trace.log("b:finally")
				_, _ = y.Await(trace.resolved("cleanup"))
				trace.log("b:finally-done")
			}()
			_, _ = y.Yield("one")
			_, _ = y.Yield("two")
			return nil, nil
		})
		iterator.next(nil).then(func(value any) (any, error) {
			trace.log("next -> " + tslibFmtr(value))
			return iterator.ret("bye"), nil
		}, nil).then(func(value any) (any, error) {
			trace.log("return -> " + tslibFmtr(value))
			return nil, nil
		}, nil)
	},
	"gen-return-while-awaiting-queues": func(trace *tslibTrace, _ *continuationTurn) {
		iterator := newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
			defer trace.log("b:finally")
			_, _ = y.Await(trace.pendingAt(4, "slow"))
			_, _ = y.Yield("one")
			return nil, nil
		})
		trace.logResult(iterator.next(nil), "next -> ", "")
		trace.logResult(iterator.ret("bye"), "return -> ", "")
	},
	"gen-throw-suspended": func(trace *tslibTrace, _ *continuationTurn) {
		iterator := newTslibAsyncGenerator(trace.executor, func(y *tslibYield) (any, error) {
			if _, err := y.Yield("one"); err != nil {
				trace.log("b:caught " + err.Error())
				_, _ = y.Yield("recovered")
			}
			return nil, nil
		})
		iterator.next(nil).then(func(value any) (any, error) {
			trace.log("next -> " + tslibFmtr(value))
			return iterator.throw(errors.New("injected")), nil
		}, nil).then(func(value any) (any, error) {
			trace.log("throw -> " + tslibFmtr(value))
			return nil, nil
		}, nil)
	},
	"values-sync-array": func(trace *tslibTrace, _ *continuationTurn) {
		source := tslibAsyncValuesFromSync(trace.executor, tslibArrayIterator(1, 2, 3))
		trace.stepAll(trace.syncLoop(source, false, 2))
	},
	"values-sync-generator-return": func(trace *tslibTrace, _ *continuationTurn) {
		state := 0
		sync := &tslibSyncIterator{
			next: func(any) tslibIterResult {
				state++
				if state > 2 {
					return tslibIterResult{done: true}
				}
				return tslibIterResult{value: state}
			},
			ret: func(value any) tslibIterResult {
				trace.log("sync:finally")
				return tslibIterResult{value: value, done: true}
			},
		}
		trace.stepAll(trace.syncLoop(tslibAsyncValuesFromSync(trace.executor, sync), true, nil))
	},
	"gen-loop-throw-return-rejects": func(trace *tslibTrace, _ *continuationTurn) {
		trace.drainCapped(trace.looping(trace.hand([]int{1, 2, 3}, handOptions(func(o *tslibHand) { o.returnRejects = true })), true))
	},
	"gen-loop-break-return-rejects": func(trace *tslibTrace, _ *continuationTurn) {
		trace.drainCapped(trace.looping(trace.hand([]int{1, 2, 3}, handOptions(func(o *tslibHand) { o.returnRejects = true })), false))
	},
	"gen-loop-throw-return-ok": func(trace *tslibTrace, _ *continuationTurn) {
		trace.drainCapped(trace.looping(trace.hand([]int{1, 2, 3}, handOptions(nil)), true))
	},
	"gen-loop-break-return-ok": func(trace *tslibTrace, _ *continuationTurn) {
		trace.drainCapped(trace.looping(trace.hand([]int{1, 2, 3}, handOptions(nil)), false))
	},
	"gen-loop-next-rejects": func(trace *tslibTrace, _ *continuationTurn) {
		trace.drainCapped(trace.looping(trace.hand([]int{1, 2, 3}, handOptions(func(o *tslibHand) { o.nextRejectsAt = 1 })), false))
	},
	"stack-full": func(trace *tslibTrace, turn *continuationTurn) {
		trace.consume(turn, trace.stack(tslibStackSpecs(), ""), 0)
	},
	"stack-break-after-2": func(trace *tslibTrace, turn *continuationTurn) {
		trace.consume(turn, trace.stack(tslibStackSpecs(), ""), 2)
	},
	"stack-map-throws": func(trace *tslibTrace, turn *continuationTurn) {
		trace.consume(turn, trace.stack(tslibStackSpecs(), "j:c2"), 0)
	},
	"stack-reader-immediate": func(trace *tslibTrace, turn *continuationTurn) {
		trace.consume(turn, trace.stack([]tslibScheduledRead{{value: "a"}, {value: "b"}, {done: true}}, ""), 0)
	},
	"native-full": func(trace *tslibTrace, turn *continuationTurn) {
		trace.nativeLoop(turn, trace.hand([]int{1, 2}, handOptions(nil)), nil)
	},
	"native-break-completion": func(trace *tslibTrace, turn *continuationTurn) {
		trace.nativeBreak(turn, trace.hand([]int{1, 2, 3}, handOptions(nil)), 2, false)
	},
	"native-break-no-return-method": func(trace *tslibTrace, turn *continuationTurn) {
		trace.nativeBreak(turn, trace.hand([]int{1, 2, 3}, handOptions(func(o *tslibHand) { o.returns = false })), 2, false)
	},
	"native-break-return-rejects": func(trace *tslibTrace, turn *continuationTurn) {
		trace.nativeBreak(turn, trace.hand([]int{1, 2, 3}, handOptions(func(o *tslibHand) { o.returnRejects = true })), 2, true)
	},
	"native-throw-return-rejects": func(trace *tslibTrace, turn *continuationTurn) {
		trace.nativeLoop(turn, trace.hand([]int{1, 2, 3}, handOptions(func(o *tslibHand) { o.returnRejects = true })), func(v int) error {
			if v == 2 {
				return errors.New("body-failed")
			}
			return nil
		})
	},
	"native-throw-in-body": func(trace *tslibTrace, turn *continuationTurn) {
		trace.nativeLoop(turn, trace.hand([]int{1, 2, 3}, handOptions(nil)), func(v int) error {
			if v == 2 {
				return errors.New("body-failed")
			}
			return nil
		})
	},
	"native-next-rejects": func(trace *tslibTrace, turn *continuationTurn) {
		trace.nativeLoop(turn, trace.hand([]int{1, 2, 3}, handOptions(func(o *tslibHand) { o.nextRejectsAt = 1 })), nil)
	},
}

func runTslibScenario(body func(*tslibTrace, *continuationTurn)) []string {
	executor := &continuationExecutor{}
	trace := &tslibTrace{executor: executor, timers: map[int][]func(){}}
	executor.run(func(turn *continuationTurn) {
		executor.post(trace.loop)
		body(trace, turn)
	})
	return trace.events
}

var (
	tslibOracleOnce   sync.Once
	tslibOracleTraces map[string][]string
	tslibOracleErr    error
)

// tslibOracle runs the Node probe once. Node prints one event list per scenario.
func tslibOracle(t *testing.T) map[string][]string {
	t.Helper()
	tslibOracleOnce.Do(func() {
		cmd := exec.Command("node", "testdata/tslib-async/probe.mjs")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			tslibOracleErr = fmt.Errorf("node probe: %w\n%s", err, stderr.String())
			return
		}
		tslibOracleErr = json.Unmarshal(output, &tslibOracleTraces)
	})
	if tslibOracleErr != nil {
		t.Fatal(tslibOracleErr)
	}
	return tslibOracleTraces
}

// Node is the oracle for every microtask count in the tslib generator, __asyncValues and for-await models. Each scenario replays a probe scenario and must reproduce Node's `label@round` list exactly.
func TestTslibAsyncMatchesNode(t *testing.T) {
	oracle := tslibOracle(t)
	for name, want := range oracle {
		if strings.HasPrefix(name, "oracle-only-") {
			continue
		}
		scenario, ok := tslibScenarios[name]
		if !ok {
			t.Errorf("probe scenario %q has no Go replay", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			got := runTslibScenario(scenario)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Go trace differs from Node\nGo:   %q\nNode: %q", got, want)
			}
		})
	}
	for _, name := range slices.Sorted(maps.Keys(tslibScenarios)) {
		if _, ok := oracle[name]; !ok {
			t.Errorf("Go scenario %q has no probe scenario", name)
		}
	}
}

// The reason tslib_async.go exists: the same generator written as a native async generator takes different microtask counts, so a native model cannot reproduce @google/genai's ordering.
func TestTslibAsyncCountsDifferFromNativeGenerators(t *testing.T) {
	oracle := tslibOracle(t)
	native, tslib := oracle["oracle-only-native-sequential"], oracle["gen-sequential"]
	if len(native) == 0 || reflect.DeepEqual(native, tslib) {
		t.Fatalf("native and tslib generators traced identically\nnative: %q\ntslib:  %q", native, tslib)
	}
}

// close releases the coroutine of a generator that a consumer abandons, and runs its finally blocks without suspending again.
func TestTslibAsyncCloseReleasesAbandonedGenerator(t *testing.T) {
	before := runtime.NumGoroutine()
	finalized := 0
	executor := &continuationExecutor{}
	executor.run(func(*continuationTurn) {
		for range 50 {
			iterator := newTslibAsyncGenerator(executor, func(y *tslibYield) (any, error) {
				defer func() {
					finalized++
					_, _ = y.Await("cleanup")
				}()
				_, _ = y.Yield(1)
				return nil, nil
			})
			iterator.next(nil)
			iterator.close()
			iterator.close()
		}
		newTslibAsyncGenerator(executor, func(*tslibYield) (any, error) { return nil, nil }).close()
	})
	if finalized != 50 {
		t.Fatalf("finally blocks ran %d times, want 50", finalized)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines grew from %d to %d", before, after)
	}
}

func tslibManyChunks(n int) []tslibScheduledRead {
	specs := make([]tslibScheduledRead, 0, n+1)
	for i := range n {
		specs = append(specs, tslibScheduledRead{value: strconv.Itoa(i)})
	}
	return append(specs, tslibScheduledRead{done: true})
}

// A long stream through the three-generator stack delivers every chunk in order, finishes the reader, and leaves no coroutine behind, whether the consumer reads to the end or leaves early.
func TestTslibAsyncStackStreamsManyChunksWithoutLeaks(t *testing.T) {
	const chunks = 5000
	for _, breakAt := range []int{0, chunks / 2} {
		before := runtime.NumGoroutine()
		delivered := 0
		released := 0
		executor := &continuationExecutor{}
		trace := &tslibTrace{executor: executor, timers: map[int][]func(){}}
		executor.run(func(turn *continuationTurn) {
			reading := newTslibAsyncGenerator(executor, func(y *tslibYield) (any, error) {
				defer func() { released++ }()
				read := trace.reader(tslibManyChunks(chunks))
				for {
					result, _ := y.Await(read())
					if result.(tslibReadResult).done {
						return nil, nil
					}
					if _, err := y.YieldAwaited(tslibChunk{trace: trace, value: result.(tslibReadResult).value}); err != nil {
						return nil, err
					}
				}
			})
			stream := trace.afc(func(any) *tslibPromise {
				return trace.resolved(trace.mapping(reading, ""))
			})
			err := tslibForAwait(turn, stream, func(value any) (bool, error) {
				if want := "j:" + strconv.Itoa(delivered); value != want {
					t.Fatalf("chunk %d = %v, want %s", delivered, value, want)
				}
				delivered++
				return delivered == breakAt, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
		want := chunks
		if breakAt != 0 {
			want = breakAt
		}
		if delivered != want || released != 1 {
			t.Fatalf("breakAt %d: delivered %d chunks and released %d readers, want %d and 1", breakAt, delivered, released, want)
		}
		deadline := time.Now().Add(2 * time.Second)
		for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if after := runtime.NumGoroutine(); after > before {
			t.Fatalf("breakAt %d: goroutines grew from %d to %d", breakAt, before, after)
		}
	}
}

func BenchmarkTslibAsyncStack(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		executor := &continuationExecutor{}
		trace := &tslibTrace{executor: executor, timers: map[int][]func(){}}
		executor.run(func(turn *continuationTurn) {
			stream := trace.stack(tslibManyChunks(1000), "")
			if err := tslibForAwait(turn, stream, func(any) (bool, error) { return false, nil }); err != nil {
				b.Fatal(err)
			}
		})
	}
}
