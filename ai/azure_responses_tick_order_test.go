package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/bodyreadhook/readbarrier"
)

const azureTicksOracle = "../coding/testdata/rpc33-observation/providers/azure-openai-responses/ticks.json"

type azureTicksFile struct {
	PiVersion string              `json:"piVersion"`
	GapCap    int                 `json:"gapCap"`
	Bodies    map[string][]string `json:"bodies"`
	Results   []struct {
		Layers  json.RawMessage `json:"layers"`
		Shape   string          `json:"shape"`
		Fixture string          `json:"fixture"`
		Events  []struct {
			Type string `json:"type"`
			Gap  int    `json:"gap"`
			IO   bool   `json:"io"`
		} `json:"events"`
	} `json:"results"`
}

// TestAzureResponsesTickOrder replays the direct-provider rows of Pi's Azure OpenAI Responses tick oracle. A FIFO chain that restarts at each delivery counts
// the microtask generations between deliveries; the first gap starts at the synchronous onResponse hook.
// upstream: packages/ai/src/api/azure-openai-responses.ts:127-131 (`await options?.onResponse?.(...)`, then push start, then `await processResponsesStream`);
// upstream: packages/ai/src/api/openai-responses-shared.ts:432 (`for await` over the SDK Stream); openai 6.40.0 core/streaming.mjs:28-48,191-245 and
// internal/shims.mjs:41-43 (native ReadableStream iterator over undici's body).
func TestAzureResponsesTickOrder(t *testing.T) {
	data, err := os.ReadFile(azureTicksOracle)
	if err != nil {
		t.Fatal(err)
	}
	var oracle azureTicksFile
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.PiVersion != UpstreamVersionString() {
		t.Fatalf("oracle pins Pi %s; current Pi %s requires a fresh oracle", oracle.PiVersion, UpstreamVersionString())
	}
	direct := 0
	barrier := readbarrier.New(t)
	for _, c := range oracle.Results {
		if string(c.Layers) != "0" {
			continue
		}
		direct++
		t.Run(c.Shape+"/"+c.Fixture, func(t *testing.T) {
			t.Parallel()
			type gap struct {
				Type string
				Gap  int
				IO   bool
			}
			var want []gap
			for _, e := range c.Events {
				want = append(want, gap{e.Type, e.Gap, e.IO})
			}
			got := azureTickGaps(t, barrier, c.Fixture, oracle.Bodies[c.Shape], oracle.GapCap)
			var have []gap
			for _, g := range got {
				have = append(have, gap{g.typ, g.gap, g.io})
			}
			if !reflect.DeepEqual(want, have) {
				t.Fatalf("delivery gaps differ from Pi\nPi: %v\nGo: %v", want, have)
			}
		})
	}
	if direct != 9 {
		t.Fatalf("oracle has %d direct cases; 3 shapes and 3 fixtures require 9", direct)
	}
}

type azureTickGap struct {
	typ string
	gap int
	io  bool
}

func azureTickGaps(t *testing.T, barrier *readbarrier.Barrier, fixture string, records []string, gapCap int) []azureTickGap {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	release := sync.OnceFunc(func() { close(started) })
	defer release()
	server := barrier.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if fixture == "buffered" {
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
		if fixture == "pending" {
			_, _ = io.WriteString(w, strings.Join(records, ""))
			return
		}
		// The probe's timer between records always outlasts the client's microtask drain: write only once the client is reading the socket again.
		for _, record := range records {
			if !barrier.WaitReading(r.Context(), r.RemoteAddr) {
				return
			}
			_, _ = io.WriteString(w, record)
			w.(http.Flusher).Flush()
		}
	}))

	ctx, executor := withContinuationExecutor(ctx)
	var generation, ticks atomic.Int64
	var spin func(mine int64) func()
	spin = func(mine int64) func() {
		return func() {
			if generation.Load() != mine || ticks.Load() >= int64(gapCap) {
				return
			}
			ticks.Add(1)
			executor.post(spin(mine))
		}
	}
	mark := func() int {
		previous := int(ticks.Swap(0))
		executor.post(spin(generation.Add(1)))
		return previous
	}
	opts := StreamOptions{APIKey: "test", MaxRetries: new(0)}
	opts.OnResponse = func(context.Context, ProviderResponse, *Model) error {
		mark()
		return nil
	}
	provider := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{BaseURL: server.URL + "/v1", APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
	defer func() { _ = provider.Close() }()
	var out []azureTickGap
	// Pi's probe starts its for-await in the same job that called stream(), so no socket completion can run between them. The consumer holds its continuation across Stream and the first iterator read, as the Agent does (agent-loop.ts:402-411).
	err := RunStreamContinuation(ctx, func(observation *StreamObservation) error {
		stream, err := provider.Stream(observation.Context(ctx), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe"), Timestamp: 1}}}), opts)
		if err != nil {
			return err
		}
		for _, event := range stream.ObserveEvents(observation.Context(context.WithoutCancel(ctx))) {
			gap := mark()
			name := strings.TrimSuffix(fmt.Sprintf("%T", event), "Event")
			out = append(out, azureTickGap{azureTickName(name), min(gap, gapCap), gap >= gapCap-1})
			if _, ok := event.(StartEvent); ok {
				release()
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	generation.Add(1)
	return out
}

func azureTickName(goName string) string {
	name := strings.TrimPrefix(goName, "ai.")
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	name = strings.ToLower(b.String())
	if after, ok := strings.CutPrefix(name, "tool_call"); ok {
		name = "toolcall" + after
	}
	return name
}
