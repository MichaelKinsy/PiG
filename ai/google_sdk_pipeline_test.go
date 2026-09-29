package ai

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// googleTickOracle is testdata/google-tick-order/pi.json: Pi 0.87.1's real provider over the real @google/genai SDK with only fetch replaced, stamped with (macrotask segment, microtask round).
type googleTickOracle struct {
	Cap   int `json:"cap"`
	Cases []struct {
		Name       string `json:"name"`
		Deliveries []struct {
			At    int    `json:"at"`
			Bytes string `json:"bytes"`
		} `json:"deliveries"`
		SDK      []googleTickStamp `json:"sdk"`
		Provider []googleTickStamp `json:"provider"`
		Consumer []googleTickStamp `json:"consumer"`
	} `json:"cases"`
}

type googleTickStamp struct {
	Seg             int            `json:"seg"`
	Tick            int            `json:"tick"`
	Event           string         `json:"ev"`
	Index           *int           `json:"index,omitempty"`
	Text            string         `json:"text,omitempty"`
	HasFunctionCall bool           `json:"hasFunctionCall,omitempty"`
	ResponseID      string         `json:"responseId,omitempty"`
	Type            string         `json:"type,omitempty"`
	ContentIndex    *int           `json:"contentIndex,omitempty"`
	StopReason      string         `json:"stopReason,omitempty"`
	Snapshot        map[string]any `json:"snapshot,omitempty"`
}

// googleRoundClock is the oracle's stamping chain: a self-rescheduling reaction that occupies one queue slot per round and starts first in every macrotask segment.
type googleRoundClock struct {
	executor   *continuationExecutor
	cap        int
	segment    int
	tick       int
	generation int
}

func (clock *googleRoundClock) startSegment() {
	clock.segment++
	clock.tick = 0
	clock.generation++
	mine, steps := clock.generation, 0
	var step func()
	step = func() {
		if mine != clock.generation || steps >= clock.cap {
			return
		}
		steps++
		clock.tick = steps
		clock.executor.post(step)
	}
	clock.executor.post(step)
}

func (clock *googleRoundClock) stamp(event string) googleTickStamp {
	return googleTickStamp{Seg: clock.segment, Tick: clock.tick, Event: event}
}

// TestGoogleSDKPipelineMatchesNodeTickOrder replays every delivery case of the oracle through the reaction model of the SDK (fetch settling through the two tslib generators and the Web Streams reader) and requires the same (segment, round) for the call, the resolution of generateContentStream, each chunk and the end.
// upstream: @google/genai 2.21.0 dist/node/index.mjs:13765-13860, 15309-15315, 15768-15855
func TestGoogleSDKPipelineMatchesNodeTickOrder(t *testing.T) {
	var oracle googleTickOracle
	data, err := os.ReadFile("testdata/google-tick-order/pi.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			executor := &continuationExecutor{}
			clock := &googleRoundClock{executor: executor, cap: oracle.Cap, segment: -1}
			var got []googleTickStamp
			finished := make(chan struct{})
			executor.run(func(turn *continuationTurn) {
				defer close(finished)
				clock.startSegment() // segment 0: the synchronous call
				got = append(got, clock.stamp("call"))
				stream := newWebReadableStream(executor, true)
				fetched := newTslibPromise(executor)
				session := &googleSDKStream{}
				defer session.close()
				sdk := googleGenerateContentStream(executor, fetched, googleUndiciJSONRounds, session)
				lastAt := 0
				for _, delivery := range c.Deliveries {
					lastAt = max(lastAt, delivery.At)
				}
				flush := func(at int) {
					for _, delivery := range c.Deliveries {
						if delivery.At == at {
							stream.enqueue([]byte(delivery.Bytes))
						}
					}
					if at == lastAt {
						stream.closeController()
					}
				}
				// fetch() resolves from a timer callback; later deliveries arrive in later macrotasks.
				executor.postExternal(func() {
					clock.startSegment()
					flush(0)
					var next func(n int)
					next = func(n int) {
						if n > lastAt {
							return
						}
						executor.postExternal(func() {
							clock.startSegment()
							flush(n)
							next(n + 1)
						})
					}
					next(1)
					fetched.resolve(&googleFetchResponse{body: stream})
				})
				settled := awaitContinuation(turn, sdk.settled)
				if settled.err != nil {
					t.Error(settled.err)
					return
				}
				generator := settled.value.(*tslibAsyncIterator)
				got = append(got, clock.stamp("sdk-resolved"))
				for index := 0; ; index++ {
					next := awaitContinuation(turn, generator.next(nil).settled)
					if next.err != nil {
						t.Error(next.err)
						return
					}
					result := next.value.(tslibIterResult)
					if result.done {
						break
					}
					chunk := result.value.(*geminiStreamChunk)
					stamp := clock.stamp("chunk")
					stamp.Index = &index
					if len(chunk.Candidates) > 0 && chunk.Candidates[0].Content != nil && len(chunk.Candidates[0].Content.Parts) > 0 {
						part := chunk.Candidates[0].Content.Parts[0]
						if part.Text != nil {
							stamp.Text = *part.Text
						}
						stamp.HasFunctionCall = part.FunctionCall != nil
					}
					stamp.ResponseID = chunk.ResponseID
					got = append(got, stamp)
				}
				got = append(got, clock.stamp("sdk-end"))
			})
			<-finished
			var want []googleTickStamp
			for _, stamp := range c.SDK {
				if stamp.Event != "segment" {
					want = append(want, stamp)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("SDK timeline differs from Node\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

// TestGoogleProviderConsumerMatchesNodeTickOrder runs the provider body over the same deliveries and requires that a plain consumer of the event stream sees each event in the same (segment, round) and with the same partial message as in Pi: the start event before the body is read, and the state of the shared message when each later event is delivered.
// upstream: packages/ai/src/api/google-generative-ai.ts:59-268; packages/ai/src/utils/event-stream.ts:44-91
func TestGoogleProviderConsumerMatchesNodeTickOrder(t *testing.T) {
	var oracle googleTickOracle
	data, err := os.ReadFile("testdata/google-tick-order/pi.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			ctx, executor := withContinuationExecutor(context.Background())
			clock := &googleRoundClock{executor: executor, cap: oracle.Cap, segment: -1}
			builder := newObservedProviderBuilder(ctx, APIGoogleGenerativeAI, "google", "gemini-2.5-flash")
			builder.managed = true
			var got, pushes []googleTickStamp
			finished := make(chan struct{})
			snapshot := func(message *AssistantMessage) map[string]any {
				encoded, err := json.Marshal(message)
				if err != nil {
					t.Error(err)
					return nil
				}
				var value map[string]any
				if err := json.Unmarshal(encoded, &value); err != nil {
					t.Error(err)
				}
				value["timestamp"] = 0.0
				return value
			}
			// The provider timeline: every push, with the message as it is when the provider pushes (Pi pushes a reference to `output`).
			executor.trace = &executorTrace{push: func(stream *AssistantMessageEventStream, event AssistantMessageEvent) {
				if stream != builder.stream {
					return
				}
				stamp := clock.stamp("push")
				stamp.Type = string(event.EventType())
				if index, ok := eventContentIndex(event); ok {
					stamp.ContentIndex = &index
				}
				stamp.Snapshot = snapshot(builder.partial)
				pushes = append(pushes, stamp)
			}}
			executor.run(func(turn *continuationTurn) {
				defer close(finished)
				clock.startSegment() // segment 0: piGoogleStream(...) returns
				got = append(got, clock.stamp("returned"))
				stream := builder.stream
				stream.resultContinuation(executor).onResolved(func(message *AssistantMessage) {
					stamp := clock.stamp("result")
					stamp.StopReason = string(message.StopReason)
					got = append(got, stamp)
				})
				body := newWebReadableStream(executor, true)
				lastAt := 0
				for _, delivery := range c.Deliveries {
					lastAt = max(lastAt, delivery.At)
				}
				flush := func(at int) {
					for _, delivery := range c.Deliveries {
						if delivery.At == at {
							body.enqueue([]byte(delivery.Bytes))
						}
					}
					if at == lastAt {
						body.closeController()
					}
				}
				go builder.runResponse(func() error {
					return builder.responseTurn(func() error {
						// fetch() resolves from a timer callback, in the first macrotask after the call.
						clock.startSegment()
						flush(0)
						var next func(n int)
						next = func(n int) {
							if n > lastAt {
								return
							}
							executor.postExternal(func() {
								clock.startSegment()
								flush(n)
								next(n + 1)
							})
						}
						next(1)
						runGoogleTurn(ctx, builder, &googleFetchResponse{body: body}, 0)
						return nil
					})
				})
				events := context.WithValue(ctx, continuationTurnKey{}, turn)
				for event := range stream.Events(events) {
					stamp := clock.stamp("event")
					stamp.Type = string(event.EventType())
					var message *AssistantMessage
					switch event := event.(type) {
					case StartEvent:
						message = event.Partial
					case DoneEvent:
						message = event.Message
					case ErrorEvent:
						message = event.Error
					default:
						message = eventPartial(event)
						if index, ok := eventContentIndex(event); ok {
							stamp.ContentIndex = &index
						}
					}
					stamp.Snapshot = snapshot(message)
					got = append(got, stamp)
				}
				got = append(got, clock.stamp("iterator-end"))
			})
			<-finished
			var wantPushes []googleTickStamp
			for _, stamp := range c.Provider {
				if stamp.Event == "push" {
					stamp.Snapshot["timestamp"] = 0.0
					wantPushes = append(wantPushes, stamp)
				}
			}
			if len(pushes) != len(wantPushes) {
				t.Fatalf("provider pushed %d events, Pi %d", len(pushes), len(wantPushes))
			}
			for i := range wantPushes {
				if !reflect.DeepEqual(pushes[i], wantPushes[i]) {
					encodedGot, _ := json.Marshal(pushes[i])
					encodedWant, _ := json.Marshal(wantPushes[i])
					t.Errorf("push %d differs from Pi\n got: %s\nwant: %s", i, encodedGot, encodedWant)
				}
			}
			var want []googleTickStamp
			for _, stamp := range c.Consumer {
				if stamp.Event == "event" && stamp.Snapshot != nil {
					stamp.Snapshot["timestamp"] = 0.0
				}
				want = append(want, stamp)
			}
			if len(got) != len(want) {
				t.Fatalf("consumer timeline has %d stamps, Node has %d", len(got), len(want))
			}
			for i := range want {
				if !reflect.DeepEqual(got[i], want[i]) {
					encodedGot, _ := json.Marshal(got[i])
					encodedWant, _ := json.Marshal(want[i])
					t.Errorf("stamp %d differs from Node\n got: %s\nwant: %s", i, encodedGot, encodedWant)
				}
			}
		})
	}
}

func eventContentIndex(event AssistantMessageEvent) (int, bool) {
	switch value := event.(type) {
	case TextStartEvent:
		return value.ContentIndex, true
	case TextDeltaEvent:
		return value.ContentIndex, true
	case TextEndEvent:
		return value.ContentIndex, true
	case ThinkingStartEvent:
		return value.ContentIndex, true
	case ThinkingDeltaEvent:
		return value.ContentIndex, true
	case ThinkingEndEvent:
		return value.ContentIndex, true
	case ToolCallStartEvent:
		return value.ContentIndex, true
	case ToolCallDeltaEvent:
		return value.ContentIndex, true
	case ToolCallEndEvent:
		return value.ContentIndex, true
	}
	return 0, false
}

// TestGoogleSSEDelimiterMatchesTheSDKScan compares the single-pass delimiter search with the SDK's per-delimiter indexOf (index.mjs:13820-13834) on every string over {CR, LF, x} up to length 9.
func TestGoogleSSEDelimiterMatchesTheSDKScan(t *testing.T) {
	naive := func(buffer string) (index, length int) {
		index = -1
		for _, delimiter := range [...]string{"\n\n", "\r\r", "\r\n\r\n"} {
			if found := strings.Index(buffer, delimiter); found != -1 && (index == -1 || found < index) {
				index, length = found, len(delimiter)
			}
		}
		return index, length
	}
	alphabet := []byte{'\r', '\n', 'x'}
	var walk func(prefix []byte)
	walk = func(prefix []byte) {
		gotIndex, gotLength := googleSSEDelimiter(string(prefix))
		wantIndex, wantLength := naive(string(prefix))
		if gotIndex != wantIndex || gotLength != wantLength {
			t.Fatalf("%q: got (%d, %d), SDK scan (%d, %d)", prefix, gotIndex, gotLength, wantIndex, wantLength)
		}
		if len(prefix) == 9 {
			return
		}
		for _, next := range alphabet {
			walk(append(prefix[:len(prefix):len(prefix)], next))
		}
	}
	walk(nil)
}
