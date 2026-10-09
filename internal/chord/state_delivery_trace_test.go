package chord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

// stateTraceAttachment hands its listener to the test and replays one buffered frame on Activate.
type stateTraceAttachment struct {
	log      *[]string
	listener func(ReplicatedStateSourceFrame)
}

func (attachment *stateTraceAttachment) Snapshot() ReplicatedStateSourceSnapshot {
	return ReplicatedStateSourceSnapshot{Value: map[string]any{"n": 0.0}, Cursor: 5}
}

func (attachment *stateTraceAttachment) Activate(listener func(ReplicatedStateSourceFrame)) {
	attachment.listener = listener
	listener(stateTraceFrame(1, 6))
}

func (attachment *stateTraceAttachment) Dispose() {
	*attachment.log = append(*attachment.log, "dispose")
}

type stateTraceSource struct{ attachment *stateTraceAttachment }

func (source stateTraceSource) Attach() ReplicatedStateSourceAttachment { return source.attachment }

func stateTraceFrame(n float64, cursor int) ReplicatedStateSourceFrame {
	return ReplicatedStateSourceFrame{Value: map[string]any{"n": n}, Ops: []Op{{"s", []any{"n"}, n}}, Cursor: cursor, Context: context.Background()}
}

// Pi packages/chord/src/services/state.ts:28-107 (StateSubscriber), 109-180 (ReplicatedStatePublisher.publish), 226-235 (replace) and
// 247-321 (AttachedReplicatedStateImpl): a replace or subscribe made from inside a listener is queued behind the running publication, a
// subscriber added mid-delivery hydrates at the committed sequence and skips publications up to it, an unsubscribed subscriber gets nothing
// more, a replace that changes nothing publishes nothing, subscriber failures go to the error reporter in isolation, and an attached state
// reports a cursor gap after disposing its attachment and ignores later frames. The trace is the Pi oracle's (/tmp/state2.mts, node over
// .upstream/current state.ts); Pi reports a mutable state's subscriber failures through queueMicrotask, so its uncaught errors come last.
func TestReplicatedStateDeliveryTraceMatchesPi(t *testing.T) {
	var log, uncaught []string
	var uncaughtMu sync.Mutex
	previous := SetUncaughtErrorReporter(func(err error) {
		uncaughtMu.Lock()
		uncaught = append(uncaught, "uncaught "+err.Error())
		uncaughtMu.Unlock()
	})
	t.Cleanup(func() { SetUncaughtErrorReporter(previous) })
	entry := func(name string, value docState, delivery ReplicatedStateDelivery) {
		log = append(log, fmt.Sprintf("%s %s %d n=%v", name, delivery.Kind, delivery.Sequence, value["n"]))
	}
	ctx := context.Background()

	state, err := NewReplicatedState(docState{"n": 0.0})
	if err != nil {
		t.Fatal(err)
	}
	var unsubscribeB func()
	mustSubscribe := func(listener func(docState, context.Context, ReplicatedStateDelivery)) func() {
		unsubscribe, err := state.Subscribe(listener)
		if err != nil {
			t.Fatal(err)
		}
		return unsubscribe
	}
	mustSubscribe(func(value docState, _ context.Context, delivery ReplicatedStateDelivery) {
		entry("A", value, delivery)
		switch value["n"] {
		case 1.0:
			if err := state.Replace(ctx, docState{"n": 2.0}); err != nil {
				t.Error(err)
			}
			log = append(log, "A after replace 2")
			mustSubscribe(func(value docState, _ context.Context, delivery ReplicatedStateDelivery) {
				entry("C", value, delivery)
				if value["n"] == 3.0 {
					panic(errors.New("C fails"))
				}
			})
			log = append(log, "A after subscribe C")
		case 2.0:
			unsubscribeB()
			log = append(log, "A unsubscribed B")
			if err := state.Replace(ctx, docState{"n": 3.0}); err != nil {
				t.Error(err)
			}
		}
	})
	unsubscribeB = mustSubscribe(func(value docState, _ context.Context, delivery ReplicatedStateDelivery) {
		entry("B", value, delivery)
		if value["n"] == 1.0 {
			panic(errors.New("B fails"))
		}
	})
	if err := state.Replace(ctx, docState{"n": 1.0}); err != nil {
		log = append(log, "replace 1 threw "+err.Error())
	} else {
		log = append(log, "replace 1 returned")
	}
	if err := state.Replace(ctx, docState{"n": 3.0}); err != nil {
		log = append(log, "threw "+err.Error())
	} else {
		log = append(log, "replace same returned")
	}
	encoded, _ := json.Marshal(state.Value())
	log = append(log, "value "+string(encoded))

	attachment := &stateTraceAttachment{log: &log}
	attached, err := AttachReplicatedState[docState](stateTraceSource{attachment}, ReplicatedStateSourceOptions{OnError: func(err error) {
		message := "onError " + err.Error()
		if aggregate, ok := errors.AsType[*AggregateError](err); ok {
			causes := make([]string, len(aggregate.Errors))
			for i, cause := range aggregate.Errors {
				causes[i] = cause.(error).Error()
			}
			message += " [" + strings.Join(causes, ", ") + "]"
		}
		log = append(log, message)
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"X", "Y"} {
		if _, err := attached.Subscribe(func(value docState, _ context.Context, delivery ReplicatedStateDelivery) {
			entry(name, value, delivery)
			if value["n"] == 2.0 {
				panic(errors.New(name + " fails"))
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	attachment.listener(stateTraceFrame(2, 7))
	attachment.listener(stateTraceFrame(3, 9))
	attachment.listener(stateTraceFrame(4, 10))
	attached.Dispose()
	encoded, _ = json.Marshal(attached.Value())
	log = append(log, "attached value "+string(encoded))
	uncaughtMu.Lock()
	log = append(log, uncaught...)
	uncaughtMu.Unlock()

	want := []string{
		"A hydrate 0 n=0",
		"B hydrate 0 n=0",
		"A update 1 n=1",
		"A after replace 2",
		"C hydrate 2 n=2",
		"A after subscribe C",
		"B update 1 n=1",
		"A update 2 n=2",
		"A unsubscribed B",
		"A update 3 n=3",
		"C update 3 n=3",
		"replace 1 returned",
		"replace same returned",
		`value {"n":3}`,
		"X hydrate 1 n=1",
		"Y hydrate 1 n=1",
		"X update 2 n=2",
		"onError X fails",
		"Y update 2 n=2",
		"onError Y fails",
		"dispose",
		"onError Replicated state source cursor has a gap: expected 8, received 9",
		`attached value {"n":2}`,
		"uncaught B fails",
		"uncaught C fails",
	}
	if !slices.Equal(log, want) {
		t.Errorf("trace differs from Pi:\n got %q\nwant %q", log, want)
	}
}
