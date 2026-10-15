//go:build !race && unix

package coding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/trace"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	xtrace "golang.org/x/exp/trace"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// The delta pipeline of a large write: a provider streams one write tool call whose arguments arrive in 16-byte deltas, the Session runs it, and a second response ends the run with a short text. The mode drives the Session as -p text mode does (coding/cli/print_mode.go): no Session listener, and a consumer that drains Events and acknowledges delivery barriers. coding/cli's BenchmarkPrintPipelineDeltas runs JSON and RPC mode on the faux script.
//
// Two providers stream the same deltas:
//
//	faux    the in-process faux provider (TokenSize 4): the executor turn, no transport
//	openai  openai-completions over a local HTTP server that writes and flushes one SSE record per delta, as a live stream arrives: the HTTP body, the readiness bridge and the SSE decoder too

// pipelineDeltaBytes is the size of one tool-call arguments delta: 4 tokens of 4 bytes, the faux provider's chunking at TokenSize 4.
const pipelineDeltaBytes = 16

// pipelineWriteArguments returns the arguments of a write tool call whose JSON text is exactly deltas*16 bytes, so the faux provider streams it in exactly deltas deltas.
func pipelineWriteArguments(deltas int) map[string]any {
	skeleton := len(ai.SafeJsonStringify(map[string]any{"path": "out.txt", "content": ""}))
	return map[string]any{"path": "out.txt", "content": strings.Repeat("x", deltas*pipelineDeltaBytes-skeleton)}
}

// pipelineProvider selects the provider of a pipeline run.
type pipelineProvider int

const (
	pipelineFaux pipelineProvider = iota
	pipelineOpenAI
)

type pipelineHarness struct {
	session *Session
	faux    *ai.FauxProviderHandle
	sse     *pipelineSSEServer
	drained chan struct{}
	// updates counts the extension's message_update handler calls.
	updates atomic.Int64
}

// pipelineSSEServer serves openai-completions chat completions: the first request of a run gets the write tool call, one flushed SSE record per arguments delta, and the second the closing text. The records are built before the run, so the server only writes during it.
type pipelineSSEServer struct {
	server   *httptest.Server
	requests atomic.Int64
	call     [][]byte
	closing  [][]byte
}

func newPipelineSSEServer(tb testing.TB) *pipelineSSEServer {
	s := &pipelineSSEServer{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		records := s.closing
		if s.requests.Add(1) == 1 {
			records = s.call
		}
		flusher, _ := w.(http.Flusher)
		for _, record := range records {
			_, _ = w.Write(record)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	tb.Cleanup(s.server.Close)
	return s
}

func pipelineSSERecord(delta string) []byte {
	return []byte(`data: {"id":"chatcmpl-pipeline","object":"chat.completion.chunk","created":0,"model":"gpt-pipeline","choices":[{"index":0,"delta":` + delta + "}]}\n\n")
}

func (s *pipelineSSEServer) script(deltas int) {
	s.requests.Store(0)
	arguments := ai.SafeJsonStringify(pipelineWriteArguments(deltas))
	s.call = [][]byte{pipelineSSERecord(`{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_pipeline","type":"function","function":{"name":"write","arguments":""}}]},"finish_reason":null`)}
	for start := 0; start < len(arguments); start += pipelineDeltaBytes {
		chunk := ai.SafeJsonStringify(arguments[start:min(start+pipelineDeltaBytes, len(arguments))])
		s.call = append(s.call, pipelineSSERecord(`{"tool_calls":[{"index":0,"function":{"arguments":`+chunk+`}}]},"finish_reason":null`))
	}
	s.call = append(s.call, pipelineSSERecord(`{},"finish_reason":"tool_calls"`), []byte("data: [DONE]\n\n"))
	s.closing = [][]byte{pipelineSSERecord(`{"role":"assistant","content":"done"},"finish_reason":null`), pipelineSSERecord(`{},"finish_reason":"stop"`), []byte("data: [DONE]\n\n")}
}

// newPipelineSession builds a Session on the provider's model with the built-in tools in a fresh working directory. extensionListeners adds an in-process extension with that many message_update handlers.
func newPipelineSession(tb testing.TB, provider pipelineProvider, extensionListeners int) *pipelineHarness {
	tb.Helper()
	tb.Setenv("PIG_HOME", tb.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: tb.TempDir()})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(services.Close)
	h := &pipelineHarness{drained: make(chan struct{})}
	var model *ai.Model
	switch provider {
	case pipelineFaux:
		size := 4
		h.faux = ai.NewFauxProvider(ai.FauxConfig{TokenSize: &ai.FauxTokenSize{Min: &size, Max: &size}, Models: []ai.FauxModelDefinition{{ID: "faux-1", ContextWindow: 10_000_000, MaxTokens: 1_000_000}}})
		model = h.faux.GetModel()
	case pipelineOpenAI:
		h.sse = newPipelineSSEServer(tb)
		openai := ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "pipeline", Model: "gpt-pipeline", BaseURL: h.sse.server.URL, APIKey: "key"})
		tb.Cleanup(func() { _ = openai.Close() })
		model = &ai.Model{ID: "gpt-pipeline", DisplayName: "gpt-pipeline", Provider: openai, Capabilities: ai.ModelCapabilities{ContextWindow: 10_000_000, MaxOutputTokens: 1_000_000, SupportsToolUse: true}}
	}
	options := SessionOptions{Model: model, NoSession: true, ResourceLoader: NoResources, SystemPrompt: "Test"}
	if extensionListeners > 0 {
		handlers := make([]extension.HandlerFn, extensionListeners)
		for i := range handlers {
			handlers[i] = func(...any) (any, error) { h.updates.Add(1); return nil, nil }
		}
		options.Runner = inproc.NewRunner([]extension.Extension{{Path: "/pipeline", Handlers: map[string][]extension.HandlerFn{"message_update": handlers}}}, services.CWD())
	}
	session, err := NewSession(services, options)
	if err != nil {
		tb.Fatal(err)
	}
	h.session = session
	go func() {
		defer close(h.drained)
		for event := range session.Events() {
			AcknowledgeEvent(event)
		}
	}()
	tb.Cleanup(h.close)
	return h
}

func (h *pipelineHarness) close() {
	if h.session == nil {
		return
	}
	_ = h.session.Close()
	<-h.drained
	h.session = nil
}

// script queues the two responses of one run: the write tool call of deltas deltas, then the closing text.
func (h *pipelineHarness) script(deltas int) {
	if h.sse != nil {
		h.sse.script(deltas)
		return
	}
	call := ai.FauxToolCall("write", pipelineWriteArguments(deltas), &ai.FauxToolCallOptions{ID: "call_pipeline"})
	h.faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxAssistantMessage(call, ai.FauxAssistantMessageOptions{StopReason: string(ai.StopReasonToolUse)}),
		ai.FauxAssistantMessage(ai.FauxContentString("done"), ai.FauxAssistantMessageOptions{}),
	})
}

// run sends one prompt and waits, as print mode does, until every event of the run was delivered.
func (h *pipelineHarness) run(tb testing.TB) {
	ctx := context.Background()
	messages, err := h.session.Send(ctx, "write the file")
	if err == nil {
		err = h.session.FlushEvents(ctx)
	}
	if err != nil {
		tb.Fatal(err)
	}
	if last := messages[len(messages)-1]; last.Assistant == nil || last.Assistant.StopReason != ai.StopReasonStop {
		tb.Fatalf("run did not end with the closing text: %+v", last)
	}
}

// processCPU is the user plus system CPU time of the whole process (getrusage RUSAGE_SELF): wall time hides the CPU that goroutine hand-offs burn on other threads.
func processCPU(tb testing.TB) time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		tb.Fatal(err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

// deltaMeter accumulates wall time, process CPU, allocations and bytes over the measured runs only.
type deltaMeter struct {
	wall, cpu      time.Duration
	allocs, bytes  uint64
	before         runtime.MemStats
	start          time.Time
	startCPU       time.Duration
	deltas, rounds int
}

func (m *deltaMeter) begin(tb testing.TB) {
	runtime.ReadMemStats(&m.before)
	m.startCPU = processCPU(tb)
	m.start = time.Now()
}

func (m *deltaMeter) end(tb testing.TB) {
	m.wall += time.Since(m.start)
	m.cpu += processCPU(tb) - m.startCPU
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	m.allocs += after.Mallocs - m.before.Mallocs
	m.bytes += after.TotalAlloc - m.before.TotalAlloc
	m.rounds++
}

func (m *deltaMeter) report(b *testing.B) {
	total := float64(m.deltas * m.rounds)
	b.ReportMetric(float64(m.wall.Nanoseconds())/total, "ns/delta")
	b.ReportMetric(float64(m.cpu.Nanoseconds())/total, "cpu-ns/delta")
	b.ReportMetric(float64(m.bytes)/total, "B/delta")
	b.ReportMetric(float64(m.allocs)/total, "allocs/delta")
}

// BenchmarkPrintPipelineDeltas reports the per-delta cost of the Session pipeline at 1k and 10k deltas: ns/delta (wall), cpu-ns/delta (process CPU from getrusage), B/delta and allocs/delta. Each delta carries 16 bytes; the whole run (tool execution, the closing turn) is divided by the deltas, so the constant per-run cost shrinks from 1k to 10k.
//
//	text         print mode's Session drive on the faux provider: no listener, Events drained
//	extension    the same with one in-process extension subscribed to message_update
//	openai-text  text on openai-completions over local HTTP, one flushed SSE record per delta (the server's writes are in the process CPU)
func BenchmarkPrintPipelineDeltas(b *testing.B) {
	for _, mode := range []struct {
		name      string
		provider  pipelineProvider
		listeners int
	}{{"text", pipelineFaux, 0}, {"extension", pipelineFaux, 1}, {"openai-text", pipelineOpenAI, 0}} {
		for _, deltas := range []int{1_000, 10_000} {
			b.Run(mode.name+"/deltas="+strconv.Itoa(deltas), func(b *testing.B) {
				meter := deltaMeter{deltas: deltas}
				b.ReportAllocs()
				for b.Loop() {
					b.StopTimer()
					h := newPipelineSession(b, mode.provider, mode.listeners)
					h.script(deltas)
					runtime.GC()
					meter.begin(b)
					b.StartTimer()
					h.run(b)
					b.StopTimer()
					meter.end(b)
					h.close()
					b.StartTimer()
				}
				meter.report(b)
			})
		}
	}
}

// pipelineWakes runs the text-mode pipeline once under runtime/trace and counts its goroutine wake-ups and P starts.
func pipelineWakes(t *testing.T, provider pipelineProvider, deltas, listeners int) traceWakes {
	t.Helper()
	h := newPipelineSession(t, provider, listeners)
	h.script(deltas)
	var buf bytes.Buffer
	if err := trace.Start(&buf); err != nil {
		t.Skipf("runtime/trace unavailable: %v", err)
	}
	h.run(t)
	trace.Stop()
	h.close()
	if updates := h.updates.Load(); listeners > 0 && updates < int64(deltas*listeners) {
		t.Fatalf("the extension saw %d message_update events, want at least %d", updates, deltas*listeners)
	}
	return countTraceWakes(t, &buf)
}

// traceWakes is what a runtime trace shows of the scheduler's hand-offs.
type traceWakes struct {
	// wakes are goroutines made runnable from waiting through the scheduler: a channel send or close, a mutex, a timer, the network poller.
	wakes int
	// switches are coroutine switches (iter.Pull): the trace parses each as an unblock too, but the next goroutine runs on the switching one's thread at once, with no run queue and no P woken (about 30 ns).
	switches int
	// procStarts are Ps that go from idle to running: a thread woken to run Go code, what a hand-off costs beyond the switch.
	procStarts int
	// waitSwitches are the coroutine switches of a body read that waited for the socket: the one goroutine the body source owns (observedBodyReadiness.waitBody) resumes the read coroutine and the coroutine yields back to it. Not in switches: a read the buffers serve has none, so they depend on how often the machine's socket was empty, four per wait: the caller's resume of the read, its pending yield, waitBody's resume and the yield back.
	waitSwitches int
	// pollerWakes are the wakes made from no goroutine (the network poller, a timer): a body read that found the socket empty and parked until the peer's bytes arrived. Part of wakes.
	pollerWakes int
}

// switchSpread is how far apart the reader puts the three events of one coroutine switch: they share the switch's timestamp, and the reader bumps each by 1 ns to keep them strictly ordered.
const switchSpread = 2

// countTraceWakes reads a runtime trace. A coroutine switch reaches the parser (x/exp/trace, order.go advanceGoSwitch) as three events within switchSpread of the switch's timestamp: the unblock of the next goroutine, a block of the switching goroutine with no reason and no stack (or its destruction, for GoSwitchDestroy), and the start of the next goroutine. A real block always has a reason ("chan receive", "select", "sync"), so a channel hand-off whose sender then blocks is still a wake. The ai package's goroutineWakes (continuation_wakes_test.go) uses the same definition.
func countTraceWakes(t testing.TB, r io.Reader) traceWakes {
	t.Helper()
	reader, err := xtrace.NewReader(r)
	if err != nil {
		t.Fatal(err)
	}
	type unblock struct {
		by, next xtrace.GoID
		at       xtrace.Time
		blocked  bool // the unblocking goroutine has stopped the way a switch stops it
	}
	byUnblocker := map[xtrace.GoID]*unblock{}
	byNext := map[xtrace.GoID]*unblock{}
	var c traceWakes
	waiters := map[xtrace.GoID]bool{}
	// recent counts, per goroutine, its switches since it last switched with a waiter.
	recent := map[xtrace.GoID]int{}
	settle := func(u *unblock, switched bool) {
		delete(byUnblocker, u.by)
		delete(byNext, u.next)
		switch {
		case switched && (waiters[u.by] || waiters[u.next]):
			c.waitSwitches++
			if waiters[u.by] {
				// The read coroutine was resumed for its wait: the two switches before it, the caller's resume and the yield of pending, were the same wait's.
				k := min(recent[u.next], 2)
				c.switches -= k
				c.waitSwitches += k
				recent[u.next] = 0
			}
		case switched:
			c.switches++
			recent[u.by]++
			recent[u.next]++
		default:
			c.wakes++
		}
	}
	for {
		event, err := reader.ReadEvent()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind() != xtrace.EventStateTransition {
			continue
		}
		transition := event.StateTransition()
		switch transition.Resource.Kind {
		case xtrace.ResourceProc:
			if from, to := transition.Proc(); to == xtrace.ProcRunning && (from == xtrace.ProcIdle || from == xtrace.ProcNotExist) {
				c.procStarts++
			}
			continue
		case xtrace.ResourceGoroutine:
		default:
			continue
		}
		from, to := transition.Goroutine()
		id := transition.Resource.Goroutine()
		if from == xtrace.GoNotExist && to == xtrace.GoRunnable {
			for frame := range transition.Stack.Frames() {
				if strings.Contains(frame.Func, "observedBodyReadiness).waitBody") {
					waiters[id] = true
					break
				}
			}
		}
		if u := byUnblocker[id]; u != nil && !u.blocked {
			if event.Time()-u.at <= switchSpread && from == xtrace.GoRunning && (to == xtrace.GoWaiting && transition.Reason == "" || to == xtrace.GoNotExist) {
				u.blocked = true
				continue
			}
			settle(u, false)
		}
		if u := byNext[id]; u != nil {
			settle(u, u.blocked && event.Time()-u.at <= switchSpread && from == xtrace.GoRunnable && to == xtrace.GoRunning)
			continue
		}
		if from != xtrace.GoWaiting || to != xtrace.GoRunnable {
			continue
		}
		by := event.Goroutine()
		if by == xtrace.NoGoroutine {
			c.wakes++ // the network poller or another context without a goroutine
			c.pollerWakes++
			continue
		}
		if u := byUnblocker[by]; u != nil {
			settle(u, false)
		}
		u := &unblock{by: by, next: id, at: event.Time()}
		byUnblocker[by] = u
		byNext[id] = u
	}
	c.wakes += len(byNext)
	return c
}

// A coroutine switch is not a wake: iter.Pull hand-offs count as switches, and a channel ping-pong, where each side blocks right after it wakes the other, counts one wake per hop.
func TestCountTraceWakesSkipsCoroutineSwitches(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const n = 1_000
	traced := func(run func()) traceWakes {
		var buf bytes.Buffer
		if err := trace.Start(&buf); err != nil {
			t.Skipf("runtime/trace unavailable: %v", err)
		}
		run()
		trace.Stop()
		return countTraceWakes(t, &buf)
	}
	switched := traced(func() {
		next, stop := iter.Pull(func(yield func(int) bool) {
			for i := range n {
				if !yield(i) {
					return
				}
			}
		})
		defer stop()
		for {
			if _, ok := next(); !ok {
				return
			}
		}
	})
	if switched.switches < 2*n || switched.wakes > n/10 {
		t.Fatalf("iter.Pull over %d values: %+v, want about %d switches and no wakes", n, switched, 2*n)
	}
	pingPong := traced(func() {
		ping, pong := make(chan int), make(chan int)
		go func() {
			for v := range ping {
				pong <- v
			}
			close(pong)
		}()
		for i := range n {
			ping <- i
			<-pong
		}
		close(ping)
		<-pong
	})
	if pingPong.wakes < 2*n || pingPong.switches > n/10 {
		t.Fatalf("channel ping-pong of %d values: %+v, want about %d wakes and no switches", n, pingPong, 2*n)
	}
	t.Logf("iter.Pull: %+v; ping-pong: %+v", switched, pingPong)
}

// Every streamed delta should be one owner's work, as Pi's single event loop does it: crossing a goroutine boundary per delta (a channel baton, a done wait, an AfterFunc, a body-read round trip) costs a wake-up, and on a multi-core host each wake-up spins an M. A coroutine switch (iter.Pull) is not counted (countTraceWakes). The count runs at GOMAXPROCS=1, where a hand-off finds its receiver parked, so it counts hops and does not depend on timing: at 4cfaf24153 it is 5.36 on the faux provider and 11.9 on openai-completions over HTTP (13-14 at the Mac's default GOMAXPROCS; the E1 trace of the real large-write row read 17.6). The marginal wakes per delta between 1k and 2k deltas are ratcheted; S1, S2 and S3 of the LW roadmap take them to at most 1.
//
// P starts (an idle P made to run: a thread woken, the cost a hand-off adds beyond the switch) do not happen with one P, so each mode runs once more at the process's GOMAXPROCS and logs its wakes and P starts per delta over a whole 10k-delta run (a 1k-to-2k difference is noise there); those depend on timing and are not ratcheted.
func TestStreamWakesPerDelta(t *testing.T) {
	procs := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(procs) })
	const n = 1_000
	marginal := func(small, large traceWakes) string {
		return fmt.Sprintf("%.2f wakes, %.2f coroutine switches, %.2f P starts per delta", float64(large.wakes-small.wakes)/n, float64(large.switches-small.switches)/n, float64(large.procStarts-small.procStarts)/n)
	}
	for _, mode := range []struct {
		id        string
		provider  pipelineProvider
		listeners int
	}{{"coding.pipeline.text", pipelineFaux, 0}, {"coding.pipeline.extension", pipelineFaux, 1}, {"coding.pipeline.openai_text", pipelineOpenAI, 0}} {
		t.Run(mode.id, func(t *testing.T) {
			runtime.GOMAXPROCS(1)
			small := pipelineWakes(t, mode.provider, n, mode.listeners)
			large := pipelineWakes(t, mode.provider, 2*n, mode.listeners)
			runtime.GOMAXPROCS(procs)
			t.Logf("GOMAXPROCS=1: %s", marginal(small, large))
			perfgate.Check(t, mode.id+".wakes_per_delta", float64(large.wakes-small.wakes)/n)
			whole := pipelineWakes(t, mode.provider, 10*n, mode.listeners)
			t.Logf("GOMAXPROCS=%d, whole run: %.2f wakes, %.2f coroutine switches, %.2f P starts per delta", procs, float64(whole.wakes)/(10*n), float64(whole.switches)/(10*n), float64(whole.procStarts)/(10*n))
		})
	}
}

// The scripted run has exactly the deltas the per-delta figures divide by.
func TestPipelineWriteArgumentsSize(t *testing.T) {
	for _, deltas := range []int{1_000, 10_000} {
		if got := len(ai.SafeJsonStringify(pipelineWriteArguments(deltas))); got != deltas*pipelineDeltaBytes {
			t.Fatalf("%d deltas: arguments are %d bytes, want %d", deltas, got, deltas*pipelineDeltaBytes)
		}
	}
}
