package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// D82 W5: the mistral-conversations provider against Pi 0.87.1's own tick order. The oracle is the checked-in output of
// coding/testdata/rpc33-observation/providers/mistral-conversations/probe.mjs; its `direct` path is a for-await over pi-ai's
// mistral-conversations stream() and records the assistant message the consumer observes at each delivery. Every trace entry that
// separates one delivery from the next is a provider push, so the ordered delivered states pin the push-versus-delivery
// interleaving (read, decode, parse, push and deliver) that the probe's trace lists.

type mistralOracleFile struct {
	Bodies  map[string]string `json:"bodies"`
	Reply   string            `json:"reply"`
	Cancels []struct {
		Name     string `json:"name"`
		Fixture  string `json:"fixture"`
		Delivery string `json:"delivery"`
		Abort    string `json:"abort"`
		States   []struct {
			Event   string          `json:"event"`
			Message json.RawMessage `json:"message"`
		} `json:"states"`
	} `json:"cancels"`
	Cases []struct {
		Fixture  string `json:"fixture"`
		Delivery string `json:"delivery"`
		Paths    []struct {
			Path   string `json:"path"`
			States []struct {
				Event   string          `json:"event"`
				Message json.RawMessage `json:"message"`
			} `json:"states"`
		} `json:"paths"`
	} `json:"cases"`
}

func loadMistralOracleFile(t *testing.T) mistralOracleFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "coding", "testdata", "rpc33-observation", "providers", "mistral-conversations", "pi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle mistralOracleFile
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle
}

// canonicalMistralState drops wall-clock fields and re-encodes with sorted keys.
func canonicalMistralState(t *testing.T, raw []byte) string {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	var strip func(any) any
	strip = func(value any) any {
		switch value := value.(type) {
		case map[string]any:
			delete(value, "timestamp")
			for key, item := range value {
				value[key] = strip(item)
			}
		case []any:
			for i, item := range value {
				value[i] = strip(item)
			}
		}
		return value
	}
	encoded, err := json.Marshal(strip(value))
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// serveMistralFixture serves body under probe.mjs's three deliveries. buffered writes the whole response in one write; pending
// flushes headers and delivers the body after the first body read is pending; chunked writes one SSE record per flush.
func serveMistralFixture(t *testing.T, body, delivery string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if delivery == "buffered" {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = io.WriteString(w, body)
			return
		}
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		wait := func(d time.Duration) bool {
			select {
			case <-time.After(d):
				return true
			case <-r.Context().Done():
				return false
			}
		}
		if !wait(150 * time.Millisecond) {
			return
		}
		if delivery == "pending" {
			_, _ = io.WriteString(w, body)
			return
		}
		gap := 25 * time.Millisecond
		if delivery == "tail" {
			gap = 150 * time.Millisecond
		}
		for _, piece := range mistralPieces(body, delivery) {
			_, _ = io.WriteString(w, piece)
			flusher.Flush()
			if !wait(gap) {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// mistralPieces splits a body into the writes of probe.mjs's chunked (one SSE record per write), split (40-byte pieces) and tail
// (the first two records, then the rest) deliveries.
func mistralPieces(body, delivery string) []string {
	var records []string
	for record := range strings.SplitAfterSeq(body, "\n\n") {
		if record != "" {
			records = append(records, record)
		}
	}
	switch delivery {
	case "split":
		var pieces []string
		for len(body) > 0 {
			n := min(40, len(body))
			pieces, body = append(pieces, body[:n]), body[n:]
		}
		return pieces
	case "tail":
		return []string{strings.Join(records[:min(2, len(records))], ""), strings.Join(records[min(2, len(records)):], "")}
	}
	return records
}

// observedMistralDirect is the Go counterpart of the probe's `direct` path: one for-range over the provider's stream.
// abort is "" (none), "timer" (60ms after the request) or "start" (in the consumer, on the start event).
func observedMistralDirect(t *testing.T, baseURL, abort string) (events []string, states []string) {
	t.Helper()
	provider := NewMistralProvider(MistralConfig{BaseURL: baseURL, APIKey: "k", Model: "strict", ProviderID: "p"})
	defer func() { _ = provider.Close() }()
	ctx, stop := context.WithTimeout(t.Context(), 20*time.Second)
	defer stop()
	// The request's signal is separate from the consumer's iteration, as in Pi where options.signal is not the for-await.
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if abort == "timer" {
		defer time.AfterFunc(60*time.Millisecond, cancel).Stop()
	}
	// Pi's probe starts its for-await in the same job that called stream(), so no socket completion can run between them. The consumer holds its continuation across Stream and the first iterator read, as the Agent does (agent-loop.ts:402-411).
	err := RunStreamContinuation(WithStreamContinuations(ctx), func(observation *StreamObservation) error {
		stream, err := provider.Stream(observation.Context(requestCtx), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe"), Timestamp: 1}}}), StreamOptions{})
		if err != nil {
			return err
		}
		for event := range stream.Events(observation.Context(ctx)) {
			var message *AssistantMessage
			switch event := event.(type) {
			case DoneEvent:
				message = event.Message
			case ErrorEvent:
				message = event.Error
			case StartEvent:
				message = event.Partial
			default:
				message = eventPartial(event)
			}
			encoded, err := json.Marshal(message.Observe())
			if err != nil {
				t.Fatal(err)
			}
			if abort == "start" && event.EventType() == EventStart {
				cancel()
			}
			events = append(events, string(event.EventType()))
			states = append(states, canonicalMistralState(t, encoded))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return events, states
}

func mistralOracleRuns() int {
	if value := os.Getenv("PIG_D82_RUNS"); value != "" {
		if runs, err := strconv.Atoi(value); err == nil && runs > 0 {
			return runs
		}
	}
	return 3
}

// TestMistralDirectMatchesPiTickOrder replays every oracle fixture and delivery through the Go provider and requires each
// delivered event and the state the consumer observes at that delivery to equal Pi's, in every run.
func TestMistralDirectMatchesPiTickOrder(t *testing.T) {
	oracle := loadMistralOracleFile(t)
	runs := mistralOracleRuns()
	for _, c := range oracle.Cases {
		t.Run(c.Fixture+"/"+c.Delivery, func(t *testing.T) {
			var wantEvents, wantStates []string
			for _, path := range c.Paths {
				if path.Path != "direct" {
					continue
				}
				for _, state := range path.States {
					wantEvents = append(wantEvents, state.Event)
					wantStates = append(wantStates, canonicalMistralState(t, state.Message))
				}
			}
			if len(wantEvents) == 0 {
				t.Fatal("oracle has no direct path")
			}
			for run := range runs {
				gotEvents, gotStates := observedMistralDirect(t, serveMistralFixture(t, oracle.Bodies[c.Fixture], c.Delivery), "")
				if fmt.Sprint(gotEvents) != fmt.Sprint(wantEvents) {
					t.Fatalf("run %d events\n got: %v\nwant: %v", run, gotEvents, wantEvents)
				}
				for i := range wantStates {
					if gotStates[i] != wantStates[i] {
						t.Fatalf("run %d: %s #%d observed state differs\n got: %s\nwant: %s", run, wantEvents[i], i, gotStates[i], wantStates[i])
					}
				}
			}
		})
	}
}

// TestMistralCancelMatchesPi aborts the request while its first body read is pending and while a read is pending after the
// start event. Pi reports the abort as the terminal error message of an `aborted` result.
func TestMistralCancelMatchesPi(t *testing.T) {
	oracle := loadMistralOracleFile(t)
	if len(oracle.Cancels) == 0 {
		t.Fatal("oracle has no cancel cases")
	}
	for _, c := range oracle.Cancels {
		t.Run(c.Name, func(t *testing.T) {
			var wantEvents, wantStates []string
			for _, state := range c.States {
				wantEvents = append(wantEvents, state.Event)
				wantStates = append(wantStates, canonicalMistralState(t, state.Message))
			}
			for run := range mistralOracleRuns() {
				gotEvents, gotStates := observedMistralDirect(t, serveMistralFixture(t, oracle.Bodies[c.Fixture], c.Delivery), c.Abort)
				if fmt.Sprint(gotEvents) != fmt.Sprint(wantEvents) {
					t.Fatalf("run %d events\n got: %v\nwant: %v", run, gotEvents, wantEvents)
				}
				for i := range wantStates {
					if gotStates[i] != wantStates[i] {
						t.Fatalf("run %d: %s #%d observed state differs\n got: %s\nwant: %s", run, wantEvents[i], i, gotStates[i], wantStates[i])
					}
				}
			}
		})
	}
}
