package ai

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func showWebRead(result webReadResult) string {
	if result.done {
		return "done"
	}
	parts := make([]string, len(result.value))
	for i, value := range result.value {
		parts[i] = fmt.Sprint(value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func webTestStream(script *microtaskScript, kind string, cancel func(error) *jsPromise[struct{}]) *webReadableStream {
	stream := newWebReadableStream(script.executor, kind == "default")
	stream.cancelSource = cancel
	return stream
}

func mustWebValues(t *testing.T, stream *webReadableStream) *webStreamValues {
	t.Helper()
	iterator, err := stream.values()
	if err != nil {
		t.Fatal(err)
	}
	return iterator
}

func mustWebReader(t *testing.T, stream *webReadableStream) *webStreamReader {
	t.Helper()
	reader, err := stream.getReader()
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

// The probe builds streams with type "bytes" (undici's response body) and default controllers; each scenario name carries the kind.
func webStreamReplays(t *testing.T) map[string]func() []string {
	replays := map[string]func() []string{}
	add := func(name string, body func(script *microtaskScript, kind string)) {
		for _, kind := range []string{"bytes", "default"} {
			replays[strings.Replace(name, "KIND", kind, 1)] = func() []string {
				return runMicrotaskScript(func(script *microtaskScript) { body(script, kind) })
			}
		}
	}

	add("read-buffered-then-pending/KIND", func(script *microtaskScript, kind string) {
		stream := webTestStream(script, kind, nil)
		stream.enqueue([]byte{1, 2})
		reader := mustWebReader(t, stream)
		script.ticker("t", 8)
		first := reader.read()
		script.logf("read1 called")
		script.logf("read1 %s", showWebRead(scriptAwait(script, first)))
		second := reader.read()
		script.logf("read2 called")
		script.external("enqueue", func() { stream.enqueue([]byte{3}) })
		script.logf("read2 %s", showWebRead(scriptAwait(script, second)))
		third := reader.read()
		script.logf("read3 called")
		script.external("close", stream.closeController)
		script.logf("read3 %s", showWebRead(scriptAwait(script, third)))
		script.logf("read4 %s", showWebRead(scriptAwait(script, reader.read())))
	})

	add("read-buffered-close-requested/KIND", func(script *microtaskScript, kind string) {
		stream := webTestStream(script, kind, nil)
		stream.enqueue([]byte{1})
		stream.closeController()
		reader := mustWebReader(t, stream)
		script.ticker("t", 8)
		script.logf("read1 %s", showWebRead(scriptAwait(script, reader.read())))
		script.logf("read2 %s", showWebRead(scriptAwait(script, reader.read())))
	})

	add("read-error/KIND", func(script *microtaskScript, kind string) {
		stream := webTestStream(script, kind, nil)
		reader := mustWebReader(t, stream)
		script.ticker("t", 8)
		pending := reader.read()
		script.external("error", func() { stream.errorController(errProbeBoom) })
		logWebSettled(script, "read", pending)
		logWebSettled(script, "read2", reader.read())
	})

	add("reader-cancel-pending-read/KIND", func(script *microtaskScript, kind string) {
		stream := webTestStream(script, kind, func(error) *jsPromise[struct{}] { return nil })
		reader := mustWebReader(t, stream)
		script.ticker("t", 8)
		pending := reader.read()
		cancelled := reader.cancel(errors.New("stop"))
		pending.react(func(result webReadResult) { script.logf("pending read %s", showWebRead(result)) }, func(error) {})
		cancelled.react(func(struct{}) { script.logf("cancel settled") }, func(error) {})
		script.logf("await pending %s", showWebRead(scriptAwait(script, pending)))
		_ = scriptAwait(script, cancelled)
		script.logf("awaited cancel")
	})

	add("values-buffered/KIND", func(script *microtaskScript, kind string) {
		stream := webTestStream(script, kind, nil)
		stream.enqueue([]byte{1})
		stream.enqueue([]byte{2})
		iterator := mustWebValues(t, stream)
		script.ticker("t", 12)
		script.logf("next1 %s", showWebRead(scriptAwait(script, iterator.next())))
		script.logf("next2 %s", showWebRead(scriptAwait(script, iterator.next())))
		third := iterator.next()
		script.logf("next3 called")
		script.external("enqueue", func() { stream.enqueue([]byte{3}) })
		script.logf("next3 %s", showWebRead(scriptAwait(script, third)))
		fourth := iterator.next()
		script.logf("next4 called")
		script.external("close", stream.closeController)
		script.logf("next4 %s", showWebRead(scriptAwait(script, fourth)))
		script.logf("next5 %s", showWebRead(scriptAwait(script, iterator.next())))
	})

	add("values-pending-first/KIND", func(script *microtaskScript, kind string) {
		stream := webTestStream(script, kind, nil)
		iterator := mustWebValues(t, stream)
		script.ticker("t", 12)
		first := iterator.next()
		script.logf("next1 called")
		script.external("enqueue", func() { stream.enqueue([]byte{9}) })
		script.logf("next1 %s", showWebRead(scriptAwait(script, first)))
		second := iterator.next()
		script.logf("next2 called")
		script.external("close", stream.closeController)
		script.logf("next2 %s", showWebRead(scriptAwait(script, second)))
	})

	add("values-close-requested/KIND", func(script *microtaskScript, kind string) {
		stream := webTestStream(script, kind, nil)
		stream.enqueue([]byte{1})
		stream.closeController()
		iterator := mustWebValues(t, stream)
		script.ticker("t", 12)
		script.logf("next1 %s", showWebRead(scriptAwait(script, iterator.next())))
		script.logf("next2 %s", showWebRead(scriptAwait(script, iterator.next())))
		script.logf("next3 %s", showWebRead(scriptAwait(script, iterator.next())))
	})

	add("values-error/KIND", func(script *microtaskScript, kind string) {
		stream := webTestStream(script, kind, nil)
		iterator := mustWebValues(t, stream)
		script.ticker("t", 12)
		pending := iterator.next()
		script.external("error", func() { stream.errorController(errProbeBoom) })
		logWebSettled(script, "next", pending)
		script.logf("next2 %s", showWebRead(scriptAwait(script, iterator.next())))
	})

	add("values-concurrent-next/KIND", func(script *microtaskScript, kind string) {
		stream := webTestStream(script, kind, nil)
		stream.enqueue([]byte{1})
		stream.enqueue([]byte{2})
		iterator := mustWebValues(t, stream)
		script.ticker("t", 14)
		a := iterator.next()
		b := iterator.next()
		a.react(func(result webReadResult) { script.logf("a %s", showWebRead(result)) }, func(error) {})
		b.react(func(result webReadResult) { script.logf("b %s", showWebRead(result)) }, func(error) {})
		scriptAwait(script, b)
		script.logf("awaited b")
	})

	// cancel variants: the source's cancel method as createPromiseCallback1Param wraps it.
	cancels := map[string]func(script *microtaskScript) func(error) *jsPromise[struct{}]{
		"none": func(*microtaskScript) func(error) *jsPromise[struct{}] { return nil },
		"sync": func(*microtaskScript) func(error) *jsPromise[struct{}] {
			return func(error) *jsPromise[struct{}] { return nil }
		},
		"async": func(script *microtaskScript) func(error) *jsPromise[struct{}] {
			return func(error) *jsPromise[struct{}] { return jsResolved(script.executor, struct{}{}) }
		},
		"await-null": func(script *microtaskScript) func(error) *jsPromise[struct{}] {
			return func(error) *jsPromise[struct{}] {
				inner := newJSPromise[struct{}](script.executor)
				script.executor.post(func() { inner.resolve(jsValue(struct{}{})) })
				return inner
			}
		},
	}
	for variant, source := range cancels {
		add("values-return/KIND/"+variant, func(script *microtaskScript, kind string) {
			stream := webTestStream(script, kind, source(script))
			stream.enqueue([]byte{1})
			stream.enqueue([]byte{2})
			iterator := mustWebValues(t, stream)
			script.ticker("t", 14)
			script.logf("next1 %s", showWebRead(scriptAwait(script, iterator.next())))
			script.logf("return called")
			returned := iterator.returnSteps(nil)
			returned.react(func(webReadResult) { script.logf("return settled") }, func(error) {})
			scriptAwait(script, returned)
			script.logf("awaited return")
			script.logf("next2 %s", showWebRead(scriptAwait(script, iterator.next())))
		})
	}

	add("values-return-first/KIND", func(script *microtaskScript, kind string) {
		stream := webTestStream(script, kind, func(error) *jsPromise[struct{}] { return nil })
		iterator := mustWebValues(t, stream)
		script.ticker("t", 10)
		returned := iterator.returnSteps(nil)
		returned.react(func(webReadResult) { script.logf("return settled") }, func(error) {})
		scriptAwait(script, returned)
		script.logf("awaited return")
	})

	add("values-return-after-close/KIND", func(script *microtaskScript, kind string) {
		stream := webTestStream(script, kind, func(error) *jsPromise[struct{}] { return nil })
		stream.enqueue([]byte{1})
		stream.closeController()
		iterator := mustWebValues(t, stream)
		script.ticker("t", 14)
		script.logf("next1 %s", showWebRead(scriptAwait(script, iterator.next())))
		script.logf("next2 %s", showWebRead(scriptAwait(script, iterator.next())))
		returned := iterator.returnSteps(nil)
		returned.react(func(webReadResult) { script.logf("return settled") }, func(error) {})
		scriptAwait(script, returned)
		script.logf("awaited return")
	})
	return replays
}

func logWebSettled(script *microtaskScript, label string, promise *jsPromise[webReadResult]) {
	if _, err := scriptSettle(script, promise); err != nil {
		script.logf("%s rejected %s", label, err)
		return
	}
	script.logf("%s resolved", label)
}

// Node 24.19.0 internal/webstreams/readablestream.js: read(), values().next/return and readableStreamCancel, replayed against the probe's microtask log.
func TestNodeWebStreamMicrotaskOrder(t *testing.T) {
	verifyNodeProbe(t, "webstreams", webStreamReplays(t))
}
