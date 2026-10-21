//go:build !race && unix

package cli

import (
	"bytes"
	"context"
	"io"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// BenchmarkPrintPipelineDeltas is coding's BenchmarkPrintPipelineDeltas through the real output modes: the faux provider streams one write tool call whose arguments arrive in 16-byte deltas, the write tool runs, and a second response ends the run. It reports ns/delta (wall), cpu-ns/delta (process CPU from getrusage), B/delta and allocs/delta over the whole run.
//
//	print/text, print/json  startPrintMode in process (the run's startup and session file are part of the run, a constant the 10k figures dilute)
//	print/json+extension    JSON mode with one in-process extension subscribed to message_update
//	rpc                     RPC mode's event forwarding (rpcHost.forward: rpcAgentEvent, the JSON line writer, the stdout backpressure listener) on a Session, without the command loop
func BenchmarkPrintPipelineDeltas(b *testing.B) {
	modes := []struct {
		name string
		run  func(b *testing.B, deltas int, meter *pipelineMeter)
	}{
		{"print/text", func(b *testing.B, deltas int, meter *pipelineMeter) { runPrintPipeline(b, "text", 0, deltas, meter) }},
		{"print/json", func(b *testing.B, deltas int, meter *pipelineMeter) { runPrintPipeline(b, "json", 0, deltas, meter) }},
		{"print/json+extension", func(b *testing.B, deltas int, meter *pipelineMeter) { runPrintPipeline(b, "json", 1, deltas, meter) }},
		{"rpc", runRPCPipeline},
	}
	for _, mode := range modes {
		for _, deltas := range []int{1_000, 10_000} {
			b.Run(mode.name+"/deltas="+strconv.Itoa(deltas), func(b *testing.B) {
				meter := pipelineMeter{deltas: deltas}
				b.ReportAllocs()
				for b.Loop() {
					b.StopTimer()
					mode.run(b, deltas, &meter)
					b.StartTimer()
				}
				meter.report(b)
			})
		}
	}
}

// pipelineFaux returns a faux provider that streams 16-byte deltas (TokenSize 4) with the two responses of one run queued: the write tool call of deltas deltas, then the closing text.
func pipelineFaux(deltas int) *ai.FauxProviderHandle {
	size := 4
	faux := ai.NewFauxProvider(ai.FauxConfig{TokenSize: &ai.FauxTokenSize{Min: &size, Max: &size}, Models: []ai.FauxModelDefinition{{ID: "faux-1", ContextWindow: 10_000_000, MaxTokens: 1_000_000}}})
	skeleton := len(ai.SafeJsonStringify(map[string]any{"path": "out.txt", "content": ""}))
	arguments := map[string]any{"path": "out.txt", "content": strings.Repeat("x", deltas*16-skeleton)}
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxAssistantMessage(ai.FauxToolCall("write", arguments, &ai.FauxToolCallOptions{ID: "call_pipeline"}), ai.FauxAssistantMessageOptions{StopReason: string(ai.StopReasonToolUse)}),
		ai.FauxAssistantMessage(ai.FauxContentString("done"), ai.FauxAssistantMessageOptions{}),
	})
	return faux
}

func pipelineServices(b *testing.B) *coding.AgentSessionServices {
	b.Setenv("PIG_HOME", b.TempDir())
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(services.Close)
	return services
}

func runPrintPipeline(b *testing.B, mode string, listeners, deltas int, meter *pipelineMeter) {
	services := pipelineServices(b)
	faux := pipelineFaux(deltas)
	configureFauxAuth(b, services, faux.ID())
	host := printModeRuntime{Services: services, Session: coding.SessionStartOptions{Model: faux.GetModel(), ResourceLoader: coding.NoResources, SessionDir: b.TempDir()}}
	if listeners > 0 {
		handlers := make([]extension.HandlerFn, listeners)
		for i := range handlers {
			handlers[i] = func(...any) (any, error) { return nil, nil }
		}
		host.Extensions = []extension.Extension{{Path: "/pipeline", Handlers: map[string][]extension.HandlerFn{"message_update": handlers}}}
	}
	var stderr bytes.Buffer
	runtime.GC()
	meter.begin(b)
	b.StartTimer()
	err := startPrintModeErr(context.Background(), host, printModeOptions{Mode: mode, InitialMessage: "write the file", stdout: io.Discard, stderr: &stderr})
	b.StopTimer()
	meter.end(b)
	if err != nil || stderr.Len() != 0 {
		b.Fatalf("print mode %s: %v, stderr %q", mode, err, stderr.String())
	}
	if calls := faux.CallCount(); calls != 2 {
		b.Fatalf("provider calls = %d, want the tool call and the closing text", calls)
	}
}

func runRPCPipeline(b *testing.B, deltas int, meter *pipelineMeter) {
	services := pipelineServices(b)
	faux := pipelineFaux(deltas)
	session, err := coding.NewSession(services, coding.SessionOptions{Model: faux.GetModel(), NoSession: true, ResourceLoader: coding.NoResources, SystemPrompt: "Test"})
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := newStdoutQueue(io.Discard, func(err error) { b.Error(err) })
	defer out.Close()
	h := &rpcHost{
		ctx: ctx, cancel: cancel,
		writeRPC:        func(v any) { writeJSONLine(out, v) },
		requestShutdown: func() {},
		conversionErr:   make(chan error, 1),
		stdoutWait:      func(ctx context.Context) { out.Wait(ctx) },
	}
	h.forward(session).Store(true)
	runtime.GC()
	meter.begin(b)
	b.StartTimer()
	_, err = session.Send(ctx, "write the file")
	if err == nil {
		err = session.FlushEvents(ctx)
	}
	out.Wait(ctx)
	b.StopTimer()
	meter.end(b)
	if err != nil {
		b.Fatal(err)
	}
	for _, unsubscribe := range h.unsubscribeEvents {
		unsubscribe()
	}
	if err := session.Close(); err != nil {
		b.Fatal(err)
	}
	h.forwarders.Wait()
	if calls := faux.CallCount(); calls != 2 {
		b.Fatalf("provider calls = %d, want the tool call and the closing text", calls)
	}
}

// pipelineMeter accumulates wall time, process CPU (getrusage RUSAGE_SELF: wall time hides the CPU that goroutine hand-offs burn on other threads), allocations and bytes over the measured runs only.
type pipelineMeter struct {
	wall, cpu      time.Duration
	allocs, bytes  uint64
	before         runtime.MemStats
	start          time.Time
	startCPU       time.Duration
	deltas, rounds int
}

func processCPU(tb testing.TB) time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		tb.Fatal(err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

func (m *pipelineMeter) begin(tb testing.TB) {
	runtime.ReadMemStats(&m.before)
	m.startCPU = processCPU(tb)
	m.start = time.Now()
}

func (m *pipelineMeter) end(tb testing.TB) {
	m.wall += time.Since(m.start)
	m.cpu += processCPU(tb) - m.startCPU
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	m.allocs += after.Mallocs - m.before.Mallocs
	m.bytes += after.TotalAlloc - m.before.TotalAlloc
	m.rounds++
}

func (m *pipelineMeter) report(b *testing.B) {
	total := float64(m.deltas * m.rounds)
	b.ReportMetric(float64(m.wall.Nanoseconds())/total, "ns/delta")
	b.ReportMetric(float64(m.cpu.Nanoseconds())/total, "cpu-ns/delta")
	b.ReportMetric(float64(m.bytes)/total, "B/delta")
	b.ReportMetric(float64(m.allocs)/total, "allocs/delta")
}
