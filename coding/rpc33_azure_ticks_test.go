package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/bodyreadhook/readbarrier"
)

const azureTicksOracle = "testdata/rpc33-observation/providers/azure-openai-responses/ticks.json"

type azureTickCase struct {
	API     ai.API            `json:"api"`
	Layers  json.RawMessage   `json:"layers"`
	Shape   string            `json:"shape"`
	Fixture string            `json:"fixture"`
	Events  []json.RawMessage `json:"events"`
}

type azureTickOracle struct {
	PiVersion string              `json:"piVersion"`
	GapCap    int                 `json:"gapCap"`
	Bodies    map[string][]string `json:"bodies"`
	Results   []azureTickCase     `json:"results"`
}

// TestRPC33AzureTickOrder replays ticks.json: the microtask generations between successive Azure OpenAI Responses deliveries,
// counted by a FIFO chain that restarts at every delivery (see providers/azure-openai-responses/ticks.mjs).
// upstream: packages/ai/src/api/azure-openai-responses.ts:127-131 (await onResponse, then push start);
// upstream: packages/ai/src/api/openai-responses-shared.ts:432 (processResponsesStream); openai 6.40.0 core/streaming.mjs:28-48,191-245.
func TestRPC33AzureTickOrder(t *testing.T) {
	for _, key := range []string{"HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
		t.Setenv(key, t.TempDir())
	}
	var oracle azureTickOracle
	readObservationJSON(t, azureTicksOracle, &oracle)
	if oracle.PiVersion != UpstreamVersion {
		t.Fatalf("oracle pins Pi %s; current Pi %s requires a fresh oracle", oracle.PiVersion, UpstreamVersion)
	}
	if len(oracle.Results) != 2*3*3 {
		t.Fatalf("oracle has %d cases; axes require 18", len(oracle.Results))
	}
	barrier := readbarrier.New(t)
	for _, c := range oracle.Results {
		t.Run(fmt.Sprintf("%s/%s/%s", strings.Trim(string(c.Layers), `"`), c.Shape, c.Fixture), func(t *testing.T) {
			t.Parallel()
			got := runAzureTickCase(t, barrier, c, oracle.GapCap, oracle.Bodies[c.Shape])
			want := observationComparable(t, maskStartGap(t, c.Events))
			have := observationComparable(t, got)
			if !bytes.Equal(want, have) {
				diff := gotextdiff.ToUnified("Pi", "Go", string(want), myers.ComputeEdits(span.URIFromPath("ticks"), string(want), string(have)))
				t.Fatalf("delivery gaps or delivered partials differ from Pi\nPi:\n%s\nGo:\n%s\n%v", tickSummary(t, maskStartGap(t, c.Events)), tickSummary(t, got), diff)
			}
		})
	}
}

func tickSummary(t *testing.T, events any) string {
	t.Helper()
	var decoded []map[string]any
	if err := json.Unmarshal(observationCopy(t, events), &decoded); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range decoded {
		fmt.Fprintf(&b, "  %v gap=%v io=%v\n", e["type"], e["gap"], e["io"])
	}
	return b.String()
}

func runAzureTickCase(t *testing.T, barrier *readbarrier.Barrier, c azureTickCase, gapCap int, records []string) []any {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	release := sync.OnceFunc(func() { close(started) })
	defer release()
	var requests atomic.Int32
	server := barrier.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if c.Fixture == "buffered" {
			_, _ = io.WriteString(w, strings.Join(records, ""))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-started:
		case <-r.Context().Done():
			return
		}
		if c.Fixture == "pending" {
			_, _ = io.WriteString(w, strings.Join(records, ""))
			return
		}
		// ticks.mjs waits on a timer between records; the client has drained and is reading the socket again before each write.
		for _, record := range records {
			if !barrier.WaitReading(r.Context(), r.RemoteAddr) {
				return
			}
			_, _ = io.WriteString(w, record)
			w.(http.Flusher).Flush()
		}
	}))
	model := &ai.Model{ID: "probe", DisplayName: "probe", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: "probe-provider", API: c.API, BaseURL: server.URL + "/v1"}, Capabilities: ai.ModelCapabilities{ContextWindow: 4096, MaxOutputTokens: 256}}
	transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("probe"), Timestamp: 1}}})

	// The chain is a queued continuation that yields once per FIFO generation (queueMicrotask in ticks.mjs).
	var generation, ticks atomic.Int64
	mark := func(observation *ai.StreamObservation) int {
		previous := int(ticks.Swap(0))
		mine := generation.Add(1)
		chain := observation.PrepareContinuation()
		go func() {
			_ = chain.Run(func(o *ai.StreamObservation) error {
				for generation.Load() == mine && ticks.Load() < int64(gapCap) {
					ticks.Add(1)
					o.Yield()
				}
				return nil
			})
		}()
		return previous
	}
	opts := ai.StreamOptions{MaxRetries: new(0), APIKey: "test"}
	var stream *ai.AssistantMessageEventStream
	var runtime *ModelRuntime
	if string(c.Layers) == `"runtime"` {
		config := fmt.Sprintf(`{"providers":{"probe-provider":{"apiKey":"test","baseUrl":%q,"api":%q,"models":[{"id":"probe","name":"probe","reasoning":false,"input":["text"],"contextWindow":4096,"maxTokens":256,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, model.ProviderMeta.BaseURL, c.API)
		services, _ := nativeCompatServices(t, config, nil)
		runtime = services.ModelRuntime()
		model = runtime.GetModel("probe-provider", "probe")
		if model == nil {
			t.Fatal("configured runtime model absent")
		}
	}
	var out []any
	// ticks.mjs starts its for-await in the same job that called stream(), so no socket completion can run between them. The consumer holds its continuation across Stream and the first iterator read, as the Agent does (agent-loop.ts:402-411).
	err := ai.RunStreamContinuation(ai.WithStreamContinuations(ctx), func(consumer *ai.StreamObservation) error {
		if runtime != nil {
			stream = runtime.StreamSimple(consumer.Context(ctx), model, ai.Context{Messages: transcript.Messages()}, opts)
		} else {
			provider := ai.NewAzureOpenAIResponsesProvider(ai.AzureOpenAIResponsesConfig{BaseURL: server.URL + "/v1", APIKey: "test", Model: "probe", ProviderID: "probe-provider", ModelMetadata: model})
			defer func() { _ = provider.Close() }()
			var err error
			stream, err = provider.Stream(consumer.Context(ctx), transcript, opts)
			if err != nil {
				return err
			}
		}
		for observation, event := range stream.ObserveEvents(consumer.Context(context.WithoutCancel(ctx))) {
			gap := mark(observation)
			raw := observationCopy(t, event)
			var record map[string]any
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			entry := map[string]any{"type": record["type"], "gap": min(gap, gapCap), "io": gap >= gapCap-1}
			if _, ok := event.(ai.StartEvent); ok {
				// The start gap counts from the synchronous onResponse hook, which no consumer can observe. The provider-level ai test
				// TestAzureResponsesTickOrder owns it for the direct provider; ModelRuntime's forwarding prefix is W7.
				entry["gap"], entry["io"] = 0, false
			}
			for _, key := range []string{"contentIndex", "delta"} {
				if v, ok := record[key]; ok {
					entry[key] = v
				}
			}
			for _, key := range []string{"partial", "message", "error"} {
				if v, ok := record[key]; ok {
					entry["partial"] = v
					break
				}
			}
			out = append(out, entry)
			if _, ok := event.(ai.StartEvent); ok {
				release()
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	generation.Add(1)
	if requests.Load() != 1 {
		t.Errorf("requests = %d", requests.Load())
	}
	return out
}

func maskStartGap(t *testing.T, events []json.RawMessage) []any {
	t.Helper()
	out := make([]any, len(events))
	for i, raw := range events {
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		if record["type"] == "start" {
			record["gap"], record["io"] = float64(0), false
		}
		out[i] = record
	}
	return out
}
