package services

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

type transcriptTestWatch struct {
	snapshot                  agentharness.LaneSnapshot
	replacement               agentharness.LaneSnapshot
	listener                  agentharness.EventListener
	resnapshots, unsubscribes atomic.Int32
}

func (w *transcriptTestWatch) Watch(context.Context) (agentharness.WatchHandle[agentharness.LaneSnapshot], error) {
	return w, nil
}
func (w *transcriptTestWatch) Snapshot() agentharness.LaneSnapshot { return w.snapshot }
func (w *transcriptTestWatch) Start(listener agentharness.EventListener) error {
	w.listener = listener
	return nil
}
func (w *transcriptTestWatch) Resnapshot(context.Context) (agentharness.LaneSnapshot, error) {
	w.resnapshots.Add(1)
	return w.replacement, nil
}
func (w *transcriptTestWatch) Unsubscribe() { w.unsubscribes.Add(1) }

func TestPortWave08TranscriptProvider(t *testing.T) {
	// upstream: packages/coding-agent/test/experimental-transcript-provider.test.ts:36
	t.Run("publishes coherent state and rebases after navigation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			snapshot := func(tip *string) agentharness.LaneSnapshot {
				return agentharness.LaneSnapshot{Lane: "main", Transcript: []session.Entry{}, TipID: tip, Configuration: session.LaneConfiguration{Model: session.ModelRef{Provider: "test", ModelID: "model"}, ThinkingLevel: "off", ActiveToolNames: []string{}}, Queues: []agentharness.LaneQueuedItem{}}
			}
			watch := &transcriptTestWatch{snapshot: snapshot(nil), replacement: snapshot(new("replacement-tip"))}
			runtime, err := CreateTranscriptService(watch)
			if err != nil {
				t.Fatal(err)
			}
			disposed := false
			t.Cleanup(func() {
				if !disposed {
					if err := runtime.Dispose(); err != nil {
						t.Error(err)
					}
				}
			})
			var mu sync.Mutex
			var states []*TranscriptState
			unsubscribe, err := runtime.Service.State().Subscribe(func(value *TranscriptState, _ context.Context, _ pico3.ReplicatedStateDelivery) {
				if value.Snapshot != nil {
					mu.Lock()
					states = append(states, value)
					mu.Unlock()
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			defer unsubscribe()
			values := func() []*TranscriptState {
				mu.Lock()
				defer mu.Unlock()
				return append([]*TranscriptState(nil), states...)
			}
			event := func(value json.RawMessage) map[string]any {
				var fields map[string]any
				if len(value) != 0 {
					if err := json.Unmarshal(value, &fields); err != nil {
						t.Fatal(err)
					}
				}
				return fields
			}
			if err := runtime.Activate(); err != nil {
				t.Fatal(err)
			}
			got := values()
			if len(got) != 1 {
				t.Fatalf("initial publications = %d, want 1", len(got))
			}
			if got[0].Snapshot.Lane != "main" || got[0].Snapshot.Operation != nil || event(got[0].Event) != nil {
				t.Fatalf("initial state = %+v", got[0])
			}
			if watch.listener == nil {
				t.Fatal("watch did not start")
			}
			if err := watch.listener(context.Background(), agentharness.HarnessEvent{Lane: "main", Payload: agentharness.RunStartPayload{RunID: "run-1", StartedAt: 1}}); err != nil {
				t.Fatal(err)
			}
			got = values()
			if len(got) != 2 {
				t.Fatalf("run_start publications = %d, want 2", len(got))
			}
			if got[1].Snapshot.Operation == nil || got[1].Snapshot.Operation.ID != "run-1" || event(got[1].Event)["type"] != "run_start" || event(got[1].Event)["runId"] != "run-1" {
				t.Fatalf("run state = %+v", got[1])
			}
			var entry session.Entry
			if err := json.Unmarshal([]byte(`{"id":"entry-1","parentId":null,"seq":1,"timestamp":2,"type":"message","message":{"role":"user","content":"hello","timestamp":2}}`), &entry); err != nil {
				t.Fatal(err)
			}
			if err := watch.listener(context.Background(), agentharness.HarnessEvent{Lane: "main", Payload: agentharness.EntryAddedPayload{Entry: entry}}); err != nil {
				t.Fatal(err)
			}
			got = values()
			last := got[len(got)-1]
			if last.Snapshot.TipID == nil || *last.Snapshot.TipID != "entry-1" || len(last.Snapshot.Transcript) != 1 || last.Snapshot.Transcript[0].ID != "entry-1" || event(last.Event)["type"] != "entry_added" {
				t.Fatalf("entry state = %+v", last)
			}
			navigation := agentharness.HarnessEvent{Lane: "main", Payload: agentharness.NavigationEndPayload{RunID: "navigation-1", Status: "completed", FromTipID: new("entry-1"), TipID: new("replacement-tip"), EndedAt: 3}}
			if err := watch.listener(context.Background(), navigation); err != nil {
				t.Fatal(err)
			}
			// Await the owned rebase task rather than polling with the upstream vi.waitFor timer.
			synctest.Wait()
			got = values()
			if len(got) < 2 {
				t.Fatalf("missing navigation/rebase publications: %d", len(got))
			}
			before, last := got[len(got)-2], got[len(got)-1]
			if before.Snapshot.TipID == nil || *before.Snapshot.TipID != "entry-1" || event(before.Event)["type"] != "navigation_end" {
				t.Fatalf("navigation state = %+v", before)
			}
			if last.Snapshot.TipID == nil || *last.Snapshot.TipID != "replacement-tip" || event(last.Event) != nil {
				t.Fatalf("rebased state = %+v", last)
			}
			current := runtime.Service.State().Value()
			if current.Snapshot.TipID == nil || *current.Snapshot.TipID != "replacement-tip" || event(current.Event) != nil {
				t.Fatalf("retained state = %+v", current)
			}
			if got := watch.resnapshots.Load(); got != 1 {
				t.Fatalf("resnapshot calls = %d, want 1", got)
			}
			if err := runtime.Dispose(); err != nil {
				t.Fatal(err)
			}
			disposed = true
			if got := watch.unsubscribes.Load(); got != 1 {
				t.Fatalf("unsubscribe calls = %d, want 1", got)
			}
		})
	})
}

func TestPortWave08TranscriptStreamingRoundTrip(t *testing.T) {
	// Production JSON boundary guard for packages/agent/src/harness/agent-harness.ts:228-246.
	input := `{"id":"run-1","kind":"run","startedAt":1,"fromTipId":null,"status":"open","runningTools":[],"streamingMessage":{"role":"assistant","content":[{"type":"text","text":"hello"}],"timestamp":2,"stopReason":"pending"}}`
	var operation agentharness.LaneSnapshotOperation
	if err := json.Unmarshal([]byte(input), &operation); err != nil {
		t.Fatal(err)
	}
	state, err := chord.NewReplicatedState(&TranscriptState{Snapshot: &agentharness.LaneSnapshot{Operation: &operation}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(state.Value().Snapshot.Operation)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("streaming operation = %s, want %s", encoded, input)
	}
}

func TestPortWave08TranscriptQueueRoundTrip(t *testing.T) {
	// Production JSON boundary guard for packages/agent/src/harness/agent-harness.ts:219-246.
	for _, input := range []string{
		`{"entryId":"message-1","kind":"steer","type":"message","message":{"role":"user","content":[{"type":"text","text":"queued"}],"timestamp":2}}`,
		`{"entryId":"custom-1","kind":"write","type":"custom","customType":"data","data":null}`,
		`{"entryId":"custom-2","kind":"write","type":"custom","customType":"data"}`,
	} {
		t.Run(input, func(t *testing.T) {
			var item agentharness.LaneQueuedItem
			if err := json.Unmarshal([]byte(input), &item); err != nil {
				t.Fatal(err)
			}
			state, err := chord.NewReplicatedState(&TranscriptState{Snapshot: &agentharness.LaneSnapshot{Queues: []agentharness.LaneQueuedItem{item}}})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(state.Value().Snapshot.Queues[0])
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			if err := json.Unmarshal([]byte(input), &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("queue item = %s, want %s", encoded, input)
			}
		})
	}
}
