package ai

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// A snapshot the parser calls valid is what cloneArgumentsValue returns for it, so a holder may keep it as its own immutable copy: for
// every snapshot of random, often malformed, often non-UTF-8 text, valid implies that the copy equals the value. The parser is allowed to
// call a valid value not valid (a string it cannot place counts against the stream), never the other way round.
func TestStreamingSnapshotValidImpliesTheArgumentCopyEqualsIt(t *testing.T) {
	alphabet := []string{`{`, `}`, `[`, `]`, `"`, `:`, `,`, ` `, `\u00e9`, `\ud83d`, `\ude00`, `\ud800`, `\n`, `\q`, "é", "\xff", "\xe2\x82", "\xac", "\xed\xa0\x80", `1`, `-`, `e`, `1e999`, `true`, `nul`, `null`, `a`, `"k":`, "\"a\xff\":"}
	sawValid, sawInvalid := 0, 0
	for seed := range uint64(400) {
		random := rand.New(rand.NewPCG(seed, 1))
		var parser StreamingJSONParser
		parser.quiet = seed%2 == 0
		for range 1 + random.IntN(40) {
			var piece strings.Builder
			for range 1 + random.IntN(4) {
				piece.WriteString(alphabet[random.IntN(len(alphabet))])
			}
			parser.Append(piece.String())
			snapshot := parser.Snapshot()
			if !snapshot.Defined {
				continue
			}
			if !snapshot.valid {
				sawInvalid++
				continue
			}
			sawValid++
			copied := cloneArgumentsValue(snapshot.Value)
			if !reflect.DeepEqual(copied, snapshot.Value) {
				t.Fatalf("seed %d: a valid snapshot %#v is not what the argument copy returns (%#v)", seed, snapshot.Value, copied)
			}
		}
	}
	if sawValid == 0 || sawInvalid == 0 {
		t.Fatalf("the generator reached %d valid and %d invalid snapshots", sawValid, sawInvalid)
	}
}

// Validity follows the clone's rules: a lone surrogate in a value is valid (WTF-8) but not in a key, a split character is not valid until
// it is complete, and an invalid byte in a finished string spoils the stream for good.
func TestStreamingSnapshotValidityRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pieces []string
		valid  []bool
	}{
		{"plain", []string{`{"a":"x`, `y","b":1}`}, []bool{true, true}},
		{"surrogate escapes are valid text", []string{`{"a":"\ud83d`, `\ude00 \ud800 "}`}, []bool{true, true}},
		{"a split character is not valid until it is whole", []string{"{\"a\":\"\xe2\x82", "\xac\"}"}, []bool{false, true}},
		{"an invalid byte in a finished string is permanent", []string{"{\"a\":\"\xff\",", `"b":"ok"}`}, []bool{false, false}},
		{"an invalid byte in the string being read spoils the stream once the string ends", []string{"{\"a\":\"ok\xff", `","b":1}`}, []bool{false, false}},
		{"an invalid key is permanent", []string{"{\"a\xff\":1,", `"b":2}`}, []bool{false, false}},
		{"a lone surrogate key is not valid", []string{`{"\ud800":1,`, `"b":2}`}, []bool{false, false}},
		{"a number that is not finite is copied as it is", []string{`{"n":1e999,`, `"b":2}`}, []bool{true, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var parser StreamingJSONParser
			for i, piece := range tc.pieces {
				parser.Append(piece)
				if got := parser.Snapshot().valid; got != tc.valid[i] {
					t.Fatalf("after %q: Valid = %v, want %v", piece, got, tc.valid[i])
				}
			}
		})
	}
}

type streamedArgumentsRun struct {
	pieces []string
	// shared is whether each piece's published snapshot must be the producer's own map (aliased) rather than a copy.
	shared []bool
}

var streamedArgumentsRuns = map[string]streamedArgumentsRun{
	"valid":                {[]string{`{"path":"a.ts",`, `"content":"line \u00e9`, `\n two \ud83d`, `\ude00 end"}`}, []bool{true, true, true, true}},
	"invalid byte":         {[]string{`{"path":"a.ts",`, "\"content\":\"a\xffb", `c"}`}, []bool{true, false, false}},
	"invalid key":          {[]string{"{\"a\xff\":1,", `"b":2}`}, []bool{false, false}},
	"split character":      {[]string{"{\"c\":\"\xe2\x82", "\xac and more\"}"}, []bool{false, true}},
	"nested and lone high": {[]string{`{"list":["a",{"b":"\ud83d`, `\ude00"}],"c":[1,2`, `]}`}, []bool{true, true, true}},
	// A number that is not finite is copied as it is (a tool call's arguments write it as null), so it does not stop the stream sharing.
	"number not finite": {[]string{`{"n":1e999,`, `"m":2}`}, []bool{true, true}},
}

// publishedArguments is the arguments of the first tool call of what the view's cell published last, as the cell holds them.
func publishedArguments(view *AssistantMessage) JsonObject {
	current, _, _ := view.observation.capture(view)
	return current.Content[0].(ToolCall).Arguments
}

func sameObject(a, b JsonObject) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// A streamed tool call's published arguments are the producer's own immutable snapshot exactly when the parser says it is valid, and
// they equal what cloneArgumentsValue returns for the producer's arguments at every step, an invalid string rewritten as before.
func TestPublishedStreamedArgumentsShareTheSnapshotOnlyWhenValidAndEqualACloneAlways(t *testing.T) {
	for name, run := range streamedArgumentsRuns {
		t.Run(name, func(t *testing.T) {
			builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "test", "model")
			runManagedBuilder(builder, func() {
				builder.start()
				builder.toolCallDelta(streamToolCallDelta{index: 0, id: "call_0", name: "write"})
				for i, piece := range run.pieces {
					builder.toolCallDelta(streamToolCallDelta{index: 0, argumentsDelta: piece})
					view := builder.publish()
					produced := builder.partial.Content[0].(ToolCall).Arguments
					published := publishedArguments(view)
					want := JsonObject(cloneArgumentsValue(produced).(map[string]any))
					if !reflect.DeepEqual(published, want) {
						t.Fatalf("piece %d: published %#v, want the clone %#v", i, published, want)
					}
					if got := sameObject(published, produced); got != run.shared[i] {
						t.Fatalf("piece %d: published arguments shared with the producer = %v, want %v", i, got, run.shared[i])
					}
				}
			})
		})
	}
}

// deepMutate changes everything reachable from a value a subscriber received: members and elements are replaced, added and deleted at
// every level.
func deepMutate(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			deepMutate(child)
			typed[key] = "mutated"
		}
		typed["injected"] = "mutated"
		for key := range typed {
			if key != "injected" {
				delete(typed, key)
				break
			}
		}
	case JsonObject:
		deepMutate(map[string]any(typed))
	case []any:
		for i, child := range typed {
			deepMutate(child)
			typed[i] = "mutated"
		}
	}
}

func mutateHandout(message *AssistantMessage) {
	for _, block := range message.Content {
		if call, ok := block.(ToolCall); ok {
			deepMutate(call.Arguments)
		}
	}
}

// streamedRunObservations runs a streamed tool call and returns, for each piece, the arguments a subscriber observes and, when mutate is
// set, changes everything it received (the stable view, Observe, ShallowCopy and delivered) before the next piece arrives. It also
// returns the producer's final arguments and the cell's.
func streamedRunObservations(t *testing.T, pieces []string, mutate bool) (observed []JsonObject, produced, published JsonObject) {
	t.Helper()
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "test", "model")
	runManagedBuilder(builder, func() {
		builder.start()
		builder.toolCallDelta(streamToolCallDelta{index: 0, id: "call_0", name: "write"})
		for _, piece := range pieces {
			builder.toolCallDelta(streamToolCallDelta{index: 0, argumentsDelta: piece})
			view := builder.publish()
			fresh := view.Observe()
			observed = append(observed, JsonObject(cloneArgumentsValue(fresh.Content[0].(ToolCall).Arguments).(map[string]any)))
			if mutate {
				for _, handout := range []*AssistantMessage{fresh, view.Observe(), view.ShallowCopy(), view.delivered(), view} {
					mutateHandout(handout)
				}
			}
		}
		produced = builder.partial.Content[0].(ToolCall).Arguments
		published = publishedArguments(builder.cell.view())
	})
	return observed, produced, published
}

// Nothing a subscriber changes in what it received reaches the cell, the producer or a later snapshot: a run in which every subscriber
// mutates everything it is handed after every piece observes exactly what a run with no mutation observes, and the producer's and
// the cell's final arguments are the unmutated parse of the text.
func TestMutatingWhatASubscriberReceivedChangesNoSnapshotOfAStreamedCall(t *testing.T) {
	for name, run := range streamedArgumentsRuns {
		t.Run(name, func(t *testing.T) {
			untouched, _, _ := streamedRunObservations(t, run.pieces, false)
			mutated, produced, published := streamedRunObservations(t, run.pieces, true)
			if !reflect.DeepEqual(mutated, untouched) {
				t.Fatalf("observations after mutation:\n got %#v\nwant %#v", mutated, untouched)
			}
			want := JsonObject(cloneArgumentsValue(untouched[len(untouched)-1]).(map[string]any))
			if !reflect.DeepEqual(JsonObject(cloneArgumentsValue(produced).(map[string]any)), want) || !reflect.DeepEqual(published, want) {
				t.Fatalf("producer %#v and cell %#v must both equal %#v", produced, published, want)
			}
		})
	}
}

// Subscribers on other goroutines read and change what they receive while the producer keeps streaming, as the race detector checks:
// no map a subscriber can write is one the producer, the parser or another subscriber reads.
func TestSubscribersMutatingConcurrentlyDoNotRaceWithAStreamingProducer(t *testing.T) {
	pieces := make([]string, 0, 200)
	pieces = append(pieces, `{"path":"a.ts","list":[1,{"k":"v"}],"content":"`)
	for i := range 197 {
		pieces = append(pieces, strings.Repeat("é", 1+i%3)+`\n`)
	}
	pieces = append(pieces, `"}`)
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "test", "model")
	views := make(chan *AssistantMessage, len(pieces))
	var subscribers sync.WaitGroup
	for range 4 {
		subscribers.Go(func() {
			for view := range views {
				for _, handout := range []*AssistantMessage{view.Observe(), view.ShallowCopy(), view.Observe().ShallowCopy()} {
					mutateHandout(handout)
				}
				publishedArguments(view) // the cell's own, read while the producer publishes the next one
			}
		})
	}
	runManagedBuilder(builder, func() {
		builder.start()
		builder.toolCallDelta(streamToolCallDelta{index: 0, id: "call_0", name: "write"})
		for _, piece := range pieces {
			builder.toolCallDelta(streamToolCallDelta{index: 0, argumentsDelta: piece})
			views <- builder.publish()
		}
	})
	close(views)
	subscribers.Wait()
	want, _, _ := parseToolCallArguments(strings.Join(pieces, ""))
	if got := builder.partial.Content[0].(ToolCall).Arguments; !reflect.DeepEqual(got, want) {
		t.Fatalf("producer arguments %#v, want %#v", got, want)
	}
	if got := publishedArguments(builder.cell.view()); !reflect.DeepEqual(got, want) {
		t.Fatalf("published arguments %#v, want %#v", got, want)
	}
}
