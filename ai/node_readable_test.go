package ai

import (
	"strings"
	"testing"
)

func readableTestStream(script *microtaskScript, flavor string) *nodeReadable {
	readable := newNodeReadable(script.executor, nodeReadableOptions{incomingMessage: flavor == "incoming", noAutoDestroy: flavor == "manual"})
	readable.readSource = func() { script.logf("_read") }
	return readable
}

// scriptStep is the probe's `async function step`: the caller's `await step()` resumes one reaction after the body logs.
func scriptStep(script *microtaskScript, label string, iterator *jsAsyncGenerator[[]byte]) {
	script.logf("%s %s", label, awaitReadable(script, iterator.next()))
	scriptAwait(script, jsResolved(script.executor, struct{}{}))
}

func showReadableResult(result jsIterResult[[]byte], err error) string {
	if err != nil {
		return "rejected " + err.Error()
	}
	return showWebRead(webReadResult(result))
}

func awaitReadable(script *microtaskScript, promise *jsPromise[jsIterResult[[]byte]]) string {
	result, err := scriptSettle(script, promise)
	return showReadableResult(result, err)
}

func readableReplays() map[string]func() []string {
	replays := map[string]func() []string{}
	add := func(name string, body func(script *microtaskScript, flavor string)) {
		for _, flavor := range []string{"plain", "incoming", "manual"} {
			replays[strings.Replace(name, "FLAVOR", flavor, 1)] = func() []string {
				return runMicrotaskScript(func(script *microtaskScript) { body(script, flavor) })
			}
		}
	}
	pushEOF := func(readable *nodeReadable) { readable.push(nil, true) }

	add("buffered-ended/FLAVOR", func(script *microtaskScript, flavor string) {
		readable := readableTestStream(script, flavor)
		readable.push([]byte{1, 2}, false)
		pushEOF(readable)
		iterator := readable.asyncIterator()
		script.ticker("t", 10)
		scriptStep(script, "next1", iterator)
		scriptStep(script, "next2", iterator)
		scriptStep(script, "next3", iterator)
		script.logf("listeners readable=%d end=%d close=%d destroyed=%t", len(readable.events["readable"]), len(readable.events["end"]), len(readable.events["close"]), readable.destroyed)
	})

	add("buffered-two-chunks-ended/FLAVOR", func(script *microtaskScript, flavor string) {
		readable := readableTestStream(script, flavor)
		readable.push([]byte{1}, false)
		readable.push([]byte{2, 3}, false)
		pushEOF(readable)
		iterator := readable.asyncIterator()
		script.ticker("t", 10)
		scriptStep(script, "next1", iterator)
		scriptStep(script, "next2", iterator)
	})

	add("buffered-open-then-end/FLAVOR", func(script *microtaskScript, flavor string) {
		readable := readableTestStream(script, flavor)
		readable.push([]byte{1}, false)
		iterator := readable.asyncIterator()
		script.ticker("t", 10)
		scriptStep(script, "next1", iterator)
		second := iterator.next()
		script.logf("next2 called")
		script.external("end", func() { pushEOF(readable) })
		script.logf("next2 %s", awaitReadable(script, second))
		scriptStep(script, "next3", iterator)
	})

	add("pending-data-end/FLAVOR", func(script *microtaskScript, flavor string) {
		readable := readableTestStream(script, flavor)
		iterator := readable.asyncIterator()
		script.ticker("t", 10)
		first := iterator.next()
		script.logf("next1 called")
		script.external("data", func() { readable.push([]byte{7}, false) })
		script.logf("next1 %s", awaitReadable(script, first))
		second := iterator.next()
		script.logf("next2 called")
		script.external("end", func() { pushEOF(readable) })
		script.logf("next2 %s", awaitReadable(script, second))
	})

	add("pending-two-chunks/FLAVOR", func(script *microtaskScript, flavor string) {
		readable := readableTestStream(script, flavor)
		iterator := readable.asyncIterator()
		script.ticker("t", 10)
		first := iterator.next()
		script.logf("next1 called")
		script.external("data1", func() { readable.push([]byte{1}, false) })
		script.logf("next1 %s", awaitReadable(script, first))
		second := iterator.next()
		script.logf("next2 called")
		script.external("data2", func() { readable.push([]byte{2}, false) })
		script.logf("next2 %s", awaitReadable(script, second))
	})

	add("pending-coalesced-pushes/FLAVOR", func(script *microtaskScript, flavor string) {
		readable := readableTestStream(script, flavor)
		iterator := readable.asyncIterator()
		script.ticker("t", 10)
		first := iterator.next()
		script.logf("next1 called")
		script.external("burst", func() {
			readable.push([]byte{1}, false)
			readable.push([]byte{2}, false)
			pushEOF(readable)
		})
		script.logf("next1 %s", awaitReadable(script, first))
		scriptStep(script, "next2", iterator)
	})

	add("concurrent-next/FLAVOR", func(script *microtaskScript, flavor string) {
		readable := readableTestStream(script, flavor)
		readable.push([]byte{1}, false)
		iterator := readable.asyncIterator()
		script.ticker("t", 14)
		a := iterator.next()
		b := iterator.next()
		a.react(func(result jsIterResult[[]byte]) { script.logf("a %s", showReadableResult(result, nil)) }, func(error) {})
		b.react(func(result jsIterResult[[]byte]) { script.logf("b %s", showReadableResult(result, nil)) }, func(error) {})
		script.logf("called")
		script.external("data", func() { readable.push([]byte{2}, false) })
		scriptAwait(script, b)
		script.logf("awaited b")
	})

	add("return-after-first/FLAVOR", func(script *microtaskScript, flavor string) {
		readable := readableTestStream(script, flavor)
		readable.push([]byte{1}, false)
		iterator := readable.asyncIterator()
		script.ticker("t", 14)
		scriptStep(script, "next1", iterator)
		script.logf("return called")
		returned := iterator.returnWith(jsValue[[]byte](nil))
		returned.react(func(jsIterResult[[]byte]) { script.logf("return settled") }, func(error) {})
		scriptAwait(script, returned)
		script.logf("awaited return")
		scriptStep(script, "next2", iterator)
		script.logf("destroyed %t", readable.destroyed)
	})

	add("return-before-next/FLAVOR", func(script *microtaskScript, flavor string) {
		readable := readableTestStream(script, flavor)
		readable.push([]byte{1}, false)
		iterator := readable.asyncIterator()
		script.ticker("t", 10)
		returned := iterator.returnWith(jsValue[[]byte](nil))
		returned.react(func(jsIterResult[[]byte]) { script.logf("return settled") }, func(error) {})
		scriptAwait(script, returned)
		script.logf("awaited return")
		script.logf("destroyed %t", readable.destroyed)
	})

	add("return-while-pending/FLAVOR", func(script *microtaskScript, flavor string) {
		readable := readableTestStream(script, flavor)
		iterator := readable.asyncIterator()
		script.ticker("t", 10)
		pending := iterator.next()
		returned := iterator.returnWith(jsValue[[]byte](nil))
		pending.react(func(result jsIterResult[[]byte]) { script.logf("pending %s", showReadableResult(result, nil)) }, func(error) {})
		returned.react(func(result jsIterResult[[]byte]) { script.logf("return settled %s", showReadableResult(result, nil)) }, func(error) {})
		script.logf("called")
		script.external("data", func() { readable.push([]byte{5}, false) })
		scriptAwait(script, returned)
		script.logf("awaited return")
		script.logf("destroyed %t", readable.destroyed)
	})

	add("error-while-pending/FLAVOR", func(script *microtaskScript, flavor string) {
		readable := readableTestStream(script, flavor)
		iterator := readable.asyncIterator()
		script.ticker("t", 10)
		pending := iterator.next()
		script.logf("next1 called")
		script.external("error", func() { readable.destroy(errProbeBoom) })
		script.logf("next1 %s", awaitReadable(script, pending))
		scriptStep(script, "next2", iterator)
	})

	add("destroyed-before-next/FLAVOR", func(script *microtaskScript, flavor string) {
		readable := readableTestStream(script, flavor)
		readable.push([]byte{1}, false)
		readable.destroy(nil)
		iterator := readable.asyncIterator()
		script.ticker("t", 10)
		scriptStep(script, "next1", iterator)
		scriptStep(script, "next2", iterator)
	})

	// The probe reads a real http.IncomingMessage whose response arrived in one socket read: the body and EOF are buffered before the consumer starts.
	replays["real-incoming/buffered-ended"] = func() []string {
		return runMicrotaskScript(func(script *microtaskScript) {
			readable := newNodeReadable(script.executor, nodeReadableOptions{incomingMessage: true})
			readable.push([]byte{1, 2}, false)
			readable.push(nil, true)
			iterator := readable.asyncIterator()
			script.ticker("t", 10)
			scriptStep(script, "next1", iterator)
			scriptStep(script, "next2", iterator)
			scriptStep(script, "next3", iterator)
		})
	}
	return replays
}

// Node 24.19.0 internal/streams/readable.js createAsyncIterator, replayed against the probe's microtask, nextTick and macrotask log.
func TestNodeReadableAsyncIteratorOrder(t *testing.T) {
	verifyNodeProbe(t, "readable", readableReplays())
}
