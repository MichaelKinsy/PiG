package ai

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptedReadiness is a transport whose buffer state is declared by the test: a ready step completes inside begin, a pending step hands deliver to the test, which completes it from its own goroutine.
type scriptedRead struct {
	data  []byte
	err   error
	ready bool
}

type scriptedReadiness struct {
	mu      sync.Mutex
	steps   []scriptedRead
	pending chan func(bodyReadSignal)
	closed  bool
}

func newScriptedReadiness(steps ...scriptedRead) *scriptedReadiness {
	return &scriptedReadiness{steps: steps, pending: make(chan func(bodyReadSignal), 1)}
}

func (readiness *scriptedReadiness) begin(deliver func(bodyReadSignal)) (bodyReadSignal, bool) {
	readiness.mu.Lock()
	step := readiness.steps[0]
	readiness.steps = readiness.steps[1:]
	readiness.mu.Unlock()
	if step.ready {
		return bodyReadSignal{data: step.data, err: step.err}, true
	}
	readiness.pending <- deliver
	return bodyReadSignal{}, false
}

func (readiness *scriptedReadiness) Close() error {
	readiness.mu.Lock()
	readiness.closed = true
	readiness.mu.Unlock()
	return nil
}

// completePending finishes the next pending read from a goroutine other than the executor's, as a transport worker does.
func (readiness *scriptedReadiness) completePending(t *testing.T, result bodyReadSignal) {
	t.Helper()
	deliver := <-readiness.pending
	deliver(result)
}

// externalQuiet models a macrotask without the probe's post-external ticker.
func (script *microtaskScript) externalQuiet(action func()) {
	script.executor.postExternal(action)
}

func bodyValuesScript(t *testing.T, readiness bodyReadiness, body func(script *microtaskScript, iterator *webStreamValues)) []string {
	t.Helper()
	return runMicrotaskScript(func(script *microtaskScript) {
		stream := newBodyStream(t.Context(), script.executor, readiness, nil)
		body(script, mustWebValues(t, stream))
	})
}

// A chunk the transport already holds settles the read in the current turn, at the position Node's buffered byte-stream read takes in the differential probe (values-buffered/bytes).
func TestBodyReadinessReadyChunkMatchesBufferedStream(t *testing.T) {
	got := bodyValuesScript(t, newScriptedReadiness(scriptedRead{data: []byte{1}, ready: true}, scriptedRead{data: []byte{2}, ready: true}), func(script *microtaskScript, iterator *webStreamValues) {
		script.ticker("t", 12)
		script.logf("next1 %s", showWebRead(scriptAwait(script, iterator.next())))
		script.logf("next2 %s", showWebRead(scriptAwait(script, iterator.next())))
	})
	want := runMicrotaskScript(func(script *microtaskScript) {
		stream := webTestStream(script, "bytes", nil)
		stream.enqueue([]byte{1})
		stream.enqueue([]byte{2})
		iterator := mustWebValues(t, stream)
		script.ticker("t", 12)
		script.logf("next1 %s", showWebRead(scriptAwait(script, iterator.next())))
		script.logf("next2 %s", showWebRead(scriptAwait(script, iterator.next())))
	})
	if !equalStrings(got, want) {
		t.Fatalf("ready read differs from a buffered stream\n got: %s\nwant: %s", strings.Join(got, " | "), strings.Join(want, " | "))
	}
	if got[3] != "t4" || got[4] != "next1 [1]" {
		t.Fatalf("first buffered read did not settle four reactions after the turn: %v", got)
	}
}

// The fetch body's first buffered chunk arrives firstReadRounds reactions later; later chunks keep the plain latency. A consumer that awaits read() sees the whole delay. values().next() adopts the read promise through its first-step chain, which registers on the promise while it is still pending and hides one reaction of the delay.
func TestBodyReadinessFirstBufferedReadRounds(t *testing.T) {
	steps := func() *scriptedReadiness {
		return newScriptedReadiness(scriptedRead{data: []byte{1}, ready: true}, scriptedRead{data: []byte{2}, ready: true})
	}
	positions := func(log []string) (first, second int) {
		for i, entry := range log {
			switch entry {
			case "next1 [1]":
				first = i
			case "next2 [2]":
				second = i
			}
		}
		return first, second
	}
	viaReader := func(rounds int) []string {
		return runMicrotaskScript(func(script *microtaskScript) {
			reader := mustWebReader(t, newBodyStreamWithFirstRead(t.Context(), script.executor, steps(), nil, rounds))
			script.ticker("t", 14)
			script.logf("next1 %s", showWebRead(scriptAwait(script, reader.read())))
			script.logf("next2 %s", showWebRead(scriptAwait(script, reader.read())))
		})
	}
	viaValues := func(rounds int) []string {
		return runMicrotaskScript(func(script *microtaskScript) {
			iterator := mustWebValues(t, newBodyStreamWithFirstRead(t.Context(), script.executor, steps(), nil, rounds))
			script.ticker("t", 14)
			script.logf("next1 %s", showWebRead(scriptAwait(script, iterator.next())))
			script.logf("next2 %s", showWebRead(scriptAwait(script, iterator.next())))
		})
	}
	for name, run := range map[string]func(int) []string{"read": viaReader, "values": viaValues} {
		t.Run(name, func(t *testing.T) {
			plainFirst, plainSecond := positions(run(0))
			delayedFirst, delayedSecond := positions(run(2))
			wantShift := 2
			if name == "values" {
				wantShift = 1
			}
			if delayedFirst-plainFirst != wantShift {
				t.Fatalf("first buffered chunk arrived %d reactions later, want %d", delayedFirst-plainFirst, wantShift)
			}
			if delayedSecond-delayedFirst != plainSecond-plainFirst {
				t.Fatalf("second chunk latency changed: %d after the first, plain %d", delayedSecond-delayedFirst, plainSecond-plainFirst)
			}
		})
	}
}

// A read the transport must wait for is an external completion: it runs only after the reaction queue drains.
func TestBodyReadinessPendingReadIsExternal(t *testing.T) {
	readiness := newScriptedReadiness(scriptedRead{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		readiness.completePending(t, bodyReadSignal{data: []byte{9}})
	}()
	got := bodyValuesScript(t, readiness, func(script *microtaskScript, iterator *webStreamValues) {
		script.ticker("t", 12)
		script.logf("next1 %s", showWebRead(scriptAwait(script, iterator.next())))
	})
	<-done
	want := runMicrotaskScript(func(script *microtaskScript) {
		stream := webTestStream(script, "bytes", nil)
		iterator := mustWebValues(t, stream)
		script.ticker("t", 12)
		first := iterator.next()
		script.externalQuiet(func() { stream.enqueue([]byte{9}) })
		script.logf("next1 %s", showWebRead(scriptAwait(script, first)))
	})
	if !equalStrings(got, want) {
		t.Fatalf("pending read differs from an external enqueue\n got: %s\nwant: %s", strings.Join(got, " | "), strings.Join(want, " | "))
	}
	if got[len(got)-2] != "t12" {
		t.Fatalf("pending read completed before the reaction queue drained: %v", got)
	}
}

// Node reports end-of-stream from a separate macrotask, even when the final bytes were already buffered with the last chunk: a ready EOF, and the EOF that accompanies ready data, are external.
func TestBodyReadinessEndOfStreamIsExternal(t *testing.T) {
	cases := map[string][]scriptedRead{
		"eof only":     {{data: []byte{1}, ready: true}, {err: io.EOF, ready: true}},
		"data and eof": {{data: []byte{1}, err: io.EOF, ready: true}},
	}
	want := runMicrotaskScript(func(script *microtaskScript) {
		stream := webTestStream(script, "bytes", nil)
		stream.enqueue([]byte{1})
		script.externalQuiet(stream.closeController)
		iterator := mustWebValues(t, stream)
		script.ticker("t", 12)
		script.logf("next1 %s", showWebRead(scriptAwait(script, iterator.next())))
		script.logf("next2 %s", showWebRead(scriptAwait(script, iterator.next())))
	})
	for name, steps := range cases {
		t.Run(name, func(t *testing.T) {
			got := bodyValuesScript(t, newScriptedReadiness(steps...), func(script *microtaskScript, iterator *webStreamValues) {
				script.ticker("t", 12)
				script.logf("next1 %s", showWebRead(scriptAwait(script, iterator.next())))
				script.logf("next2 %s", showWebRead(scriptAwait(script, iterator.next())))
			})
			if got[len(got)-1] != "next2 done" || got[len(got)-2] != "t12" {
				t.Fatalf("end of stream was not an external completion: %v", got)
			}
			if !equalStrings(got, want) {
				t.Fatalf("differs from a buffered chunk followed by an external close\n got: %s\nwant: %s", strings.Join(got, " | "), strings.Join(want, " | "))
			}
		})
	}
}

func TestBodyReadinessCancellation(t *testing.T) {
	t.Run("before the read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		readiness := newScriptedReadiness()
		var failure error
		runMicrotaskScript(func(script *microtaskScript) {
			iterator := mustWebValues(t, newBodyStream(ctx, script.executor, readiness, nil))
			_, failure = scriptSettle(script, iterator.next())
		})
		if !errors.Is(failure, context.Canceled) {
			t.Fatalf("read failed with %v, want context.Canceled", failure)
		}
	})
	t.Run("while waiting", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		readiness := newScriptedReadiness(scriptedRead{})
		go func() {
			deliver := <-readiness.pending
			cancel()
			deliver(bodyReadSignal{data: []byte("late")})
		}()
		var failure error
		runMicrotaskScript(func(script *microtaskScript) {
			iterator := mustWebValues(t, newBodyStream(ctx, script.executor, readiness, nil))
			_, failure = scriptSettle(script, iterator.next())
		})
		if !errors.Is(failure, context.Canceled) {
			t.Fatalf("a completion after cancellation must report the cancellation, got %v", failure)
		}
	})
}

func TestBodyReadinessErrorAfterDataIsSeparateEvent(t *testing.T) {
	boom := errors.New("connection reset")
	var first webReadResult
	var firstErr, secondErr error
	runMicrotaskScript(func(script *microtaskScript) {
		iterator := mustWebValues(t, newBodyStream(t.Context(), script.executor, newScriptedReadiness(scriptedRead{data: []byte{1}, err: boom, ready: true}), nil))
		first, firstErr = scriptSettle(script, iterator.next())
		_, secondErr = scriptSettle(script, iterator.next())
	})
	if len(first.value) != 1 || firstErr != nil || !errors.Is(secondErr, boom) {
		t.Fatalf("got %#v %v then %v, want the chunk and then the error", first, firstErr, secondErr)
	}
}

// The consumer leaving the iterator early cancels the stream and closes the source once.
func TestBodyReadinessReturnClosesSource(t *testing.T) {
	readiness := newScriptedReadiness(scriptedRead{data: []byte{1}, ready: true})
	bodyValuesScript(t, readiness, func(script *microtaskScript, iterator *webStreamValues) {
		scriptAwait(script, iterator.next())
		scriptAwait(script, iterator.returnSteps(nil))
	})
	if !readiness.closed {
		t.Fatal("return() did not close the readiness source")
	}
}

// pipeTransport serves responses over net.Pipe, whose Write delivers exactly what the client's bufio reader asks for, so the split between buffered and pending bytes is decided by the test rather than by the kernel.
func pipeResponseBody(t *testing.T, serve func(conn net.Conn)) *observedResponseBody {
	t.Helper()
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer func() { _ = server.Close() }()
			request, err := http.ReadRequest(bufio.NewReader(server))
			if err == nil {
				_ = request.Body.Close()
				serve(server)
			}
		}()
		return &idleTimeoutConn{Conn: client, timeout: func() time.Duration { return 0 }}, nil
	}}
	client := &http.Client{Transport: &nodeFetchTransport{base: transport, connectionIdle: true}}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://pipe.invalid/", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close(); transport.CloseIdleConnections() })
	body, ok := response.Body.(*observedResponseBody)
	if !ok {
		t.Fatalf("response body is %T, want the observed HTTP/1 body", response.Body)
	}
	return body
}

// Bytes that arrived with the response headers are ready: net/http's bufio reader holds them and the connection is not read again.
func TestObservedBodyReadinessBufferedBytesAreReady(t *testing.T) {
	body := pipeResponseBody(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello")
	})
	readiness := newObservedBodyReadiness(body)
	defer func() { _ = readiness.Close() }()
	result, ready := readiness.begin(func(bodyReadSignal) { t.Error("a ready read must not call deliver") })
	if !ready || string(result.data) != "hello" {
		t.Fatalf("begin = %q ready=%t, want the buffered body", result.data, ready)
	}
}

// A read that must wait for the network announces it before it blocks and completes through deliver.
func TestObservedBodyReadinessNetworkWaitIsPending(t *testing.T) {
	release := make(chan struct{})
	body := pipeResponseBody(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\n")
		<-release
		_, _ = io.WriteString(conn, "world")
	})
	readiness := newObservedBodyReadiness(body)
	defer func() { _ = readiness.Close() }()
	delivered := make(chan bodyReadSignal, 1)
	if _, ready := readiness.begin(func(result bodyReadSignal) { delivered <- result }); ready {
		t.Fatal("a read with no buffered bytes reported ready")
	}
	close(release)
	if result := <-delivered; string(result.data) != "world" {
		t.Fatalf("delivered %q, want world", result.data)
	}
}

// A body that cannot report its buffer is always pending, and Close joins the reader goroutine.
func TestOpaqueBodyReadinessIsAlwaysPending(t *testing.T) {
	readiness := newOpaqueBodyReadiness(io.NopCloser(strings.NewReader("abc")))
	delivered := make(chan bodyReadSignal, 1)
	if _, ready := readiness.begin(func(result bodyReadSignal) { delivered <- result }); ready {
		t.Fatal("an opaque body reported ready")
	}
	if result := <-delivered; string(result.data) != "abc" {
		t.Fatalf("delivered %q, want abc", result.data)
	}
	if err := readiness.Close(); err != nil {
		t.Fatal(err)
	}
}
