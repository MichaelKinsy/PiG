//go:build !race && unix

package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// The most objects a streamed argument delta may allocate in `-p` text mode, where nothing observes message_update, and
// what the base (b02c2ceabc) allocated. Delivering each update to the Agent and Session listeners, which do nothing with
// it, was most of the base's cost; on openai-completions the eager parse of the partial arguments was most of the rest.
//
//	faux    the faux provider: the publication of the delta, its event, the consumer's await of it (13.5 after lw-quiet)
//	openai  openai-completions over local HTTP, one SSE record per delta: the record's decoding too (21.7 after lw-quiet)
const (
	printTextFauxDeltaAllocsBase      = 45
	printTextFauxDeltaAllocsCeiling   = 20
	printTextOpenAIDeltaAllocsBase    = 71.5
	printTextOpenAIDeltaAllocsCeiling = 35
)

// In `-p` text mode nothing observes a message_update (the Session discards its events and has no listener or
// extension handler for it), so a streamed delta costs only its production and the consumer's turn: the slope of the
// run's allocations over the number of deltas stays under the ceiling.
func TestPrintTextModeUnobservedDeltasAllocateLittle(t *testing.T) {
	for _, tc := range []struct {
		name          string
		run           func(t *testing.T, deltas int) uint64
		base, ceiling float64
	}{
		{"faux", printTextFauxRunAllocs, printTextFauxDeltaAllocsBase, printTextFauxDeltaAllocsCeiling},
		{"openai", printTextOpenAIRunAllocs, printTextOpenAIDeltaAllocsBase, printTextOpenAIDeltaAllocsCeiling},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const small, large = 1_000, 5_000
			tc.run(t, small) // warm the process: lazily built tables and pools are not per delta
			perDelta := (float64(tc.run(t, large)) - float64(tc.run(t, small))) / float64(large-small)
			t.Logf("allocations per delta: %.1f (base %.1f, ceiling %.1f)", perDelta, tc.base, tc.ceiling)
			if perDelta > tc.ceiling {
				t.Fatalf("allocations per streamed delta = %.1f, want at most %.1f (base %.1f)", perDelta, tc.ceiling, tc.base)
			}
		})
	}
}

// BenchmarkPrintTextOpenAIDeltas is BenchmarkPrintPipelineDeltas's print/text on openai-completions over local HTTP,
// the large_write row's shape: ns/delta (wall), cpu-ns/delta (process CPU, the server's writes included), B/delta and
// allocs/delta over the whole run.
func BenchmarkPrintTextOpenAIDeltas(b *testing.B) {
	const deltas = 10_000
	meter := pipelineMeter{deltas: deltas}
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		services, host, server := printTextOpenAISetup(b, deltas)
		runtime.GC()
		meter.begin(b)
		b.StartTimer()
		runPrintText(b, host)
		b.StopTimer()
		meter.end(b)
		server.close()
		services.Close()
		b.StartTimer()
	}
	meter.report(b)
}

func printTextFauxRunAllocs(t *testing.T, deltas int) uint64 {
	services := quietPipelineServices(t)
	faux := pipelineFaux(deltas)
	configureFauxAuth(t, services, faux.ID())
	host := printModeRuntime{Services: services, Session: coding.SessionStartOptions{Model: faux.GetModel(), ResourceLoader: coding.NoResources, SessionDir: t.TempDir()}}
	allocs := measureAllocs(func() { runPrintText(t, host) })
	if calls := faux.CallCount(); calls != 2 {
		t.Fatalf("provider calls = %d, want the tool call and the closing text", calls)
	}
	return allocs
}

func printTextOpenAIRunAllocs(t *testing.T, deltas int) uint64 {
	services, host, server := printTextOpenAISetup(t, deltas)
	defer services.Close()
	defer server.close()
	allocs := measureAllocs(func() { runPrintText(t, host) })
	if requests := server.requests.Load(); requests != 2 {
		t.Fatalf("requests = %d, want the tool call and the closing text", requests)
	}
	return allocs
}

func measureAllocs(run func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	return after.Mallocs - before.Mallocs
}

func runPrintText(tb testing.TB, host printModeRuntime) {
	var stderr bytes.Buffer
	err := startPrintModeErr(context.Background(), host, printModeOptions{Mode: "text", InitialMessage: "write the file", stdout: io.Discard, stderr: &stderr})
	if err != nil || stderr.Len() != 0 {
		tb.Fatalf("print mode: %v, stderr %q", err, stderr.String())
	}
}

func quietPipelineServices(tb testing.TB) *coding.AgentSessionServices {
	tb.Setenv("PIG_HOME", tb.TempDir())
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: tb.TempDir(), AgentDir: tb.TempDir()})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(services.Close)
	return services
}

// printTextOpenAISetup serves a write tool call of deltas 16-byte argument deltas, source text with escaped newlines and
// quotes as a model writes a file, then a closing text, from openai-completions over local HTTP.
func printTextOpenAISetup(tb testing.TB, deltas int) (*coding.AgentSessionServices, printModeRuntime, *quietSSEServer) {
	services := quietPipelineServices(tb)
	server := newQuietSSEServer(deltas)
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "pipeline", Model: "gpt-pipeline", BaseURL: server.server.URL, APIKey: "key"})
	tb.Cleanup(func() { _ = provider.Close() })
	configureFauxAuth(tb, services, "pipeline")
	model := &ai.Model{ID: "gpt-pipeline", DisplayName: "gpt-pipeline", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 10_000_000, MaxOutputTokens: 1_000_000, SupportsToolUse: true}}
	return services, printModeRuntime{Services: services, Session: coding.SessionStartOptions{Model: model, ResourceLoader: coding.NoResources, SessionDir: tb.TempDir()}}, server
}

type quietSSEServer struct {
	server   *httptest.Server
	requests atomic.Int64
}

func (s *quietSSEServer) close() { s.server.Close() }

func newQuietSSEServer(deltas int) *quietSSEServer {
	record := func(delta string) []byte {
		return []byte(`data: {"id":"chatcmpl-pipeline","object":"chat.completion.chunk","created":0,"model":"gpt-pipeline","choices":[{"index":0,"delta":` + delta + "}]}\n\n")
	}
	content := strings.Repeat("export const value = \"x\";\n", deltas)
	arguments := ai.SafeJsonStringify(map[string]any{"path": "out.ts", "content": content})[:deltas*16]
	arguments += `"}`
	call := [][]byte{record(`{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_pipeline","type":"function","function":{"name":"write","arguments":""}}]},"finish_reason":null`)}
	for start := 0; start < len(arguments); start += 16 {
		chunk := ai.SafeJsonStringify(arguments[start:min(start+16, len(arguments))])
		call = append(call, record(`{"tool_calls":[{"index":0,"function":{"arguments":`+chunk+`}}]},"finish_reason":null`))
	}
	call = append(call, record(`{},"finish_reason":"tool_calls"`), []byte("data: [DONE]\n\n"))
	closing := [][]byte{record(`{"role":"assistant","content":"done"},"finish_reason":null`), record(`{},"finish_reason":"stop"`), []byte("data: [DONE]\n\n")}
	s := &quietSSEServer{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		records := closing
		if s.requests.Add(1) == 1 {
			records = call
		}
		flusher, _ := w.(http.Flusher)
		for _, record := range records {
			_, _ = w.Write(record)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	return s
}
