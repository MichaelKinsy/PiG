package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// deepNesting is value nested depth levels deep, in arrays or in one-key objects.
func deepNesting(shape string, depth int) any {
	var nested any = 1
	for range depth {
		if shape == "objects" {
			nested = map[string]any{"a": nested}
		} else {
			nested = []any{nested}
		}
	}
	return nested
}

// deepDeltaShapes are the nestings the delta rows send: past serde_json's 128 levels, and near the 10000 the host's marshal allows (the payload adds a few levels). The last is shallow: the extension still reads an ordinary update after the deep ones.
var deepDeltaShapes = []struct {
	shape string
	depth int
}{{"arrays", 200}, {"arrays", 3000}, {"objects", 3000}, {"arrays", 4000}, {"objects", 4000}, {"arrays", 9000}, {"objects", 9000}, {"arrays", 1}}

// A message_update whose partial gains a tool call with deeply nested arguments: the host sends the block in a delta's `set` operation, and the operation must be read whole.
func deepMessageUpdateEvents(shape string, depth int) []extension.MessageUpdateEvent {
	base := ai.AssistantMessage{API: "openai-completions", Provider: "faux", Model: "m", Timestamp: 1700000000000, StopReason: ai.StopReasonToolUse}
	call := ai.ToolCall{ID: "call_1", Name: "deep", Arguments: ai.JsonObject{"zeta": 1, "meta": map[string]any{"d": deepNesting(shape, depth)}}}
	var events []extension.MessageUpdateEvent
	answer := fmt.Sprintf("%s %d: ", shape, depth)
	for step := range 4 {
		answer += fmt.Sprintf("answer %d of the stream; ", step)
		message := base
		message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: answer}}
		if step >= 1 {
			message.Content = append(message.Content, call)
		}
		events = append(events, updateEvent(message, func(p *ai.AssistantMessage) any {
			return ai.TextDeltaEvent{ContentIndex: 0, Delta: "answer", Partial: p}
		}))
	}
	return events
}

// Tool updates of two calls: the first call's arguments are deep in every update (its keyframe carries them and each delta is rebuilt from them), the second's partial
// result gains a deep `details` in a delta's `set` operation and keeps it while its text grows.
func deepToolUpdateEvents(shape string, depth int) []extension.ToolExecutionUpdateEvent {
	var events []extension.ToolExecutionUpdateEvent
	texts := [2]string{fmt.Sprintf("%s %d: ", shape, depth), fmt.Sprintf("%s %d: ", shape, depth)}
	for round := range 4 {
		for call := range 2 {
			texts[call] += fmt.Sprintf("call %d round %d writes a line of output that is longer than the append framing;\n", call, round)
			partial := map[string]any{"content": []any{map[string]any{"type": "text", "text": texts[call]}}, "details": map[string]any{"progress": round}}
			args := map[string]any{"command": "make", "target": call}
			if call == 0 {
				args["meta"] = map[string]any{"d": deepNesting(shape, depth)}
			}
			if call == 1 && round >= 1 {
				partial["details"] = map[string]any{"progress": round, "d": deepNesting(shape, depth)}
			}
			events = append(events, extension.ToolExecutionUpdateEvent{Type: "tool_execution_update", ToolCallID: fmt.Sprintf("call-%d", call), ToolName: "make", Args: args, PartialResult: partial})
		}
	}
	return events
}

// TestDeepEventStreamDeltasReachEverySDK pins that a message_update or tool_execution_update delta whose operations carry a value nested past serde_json's 128 levels (up to
// what the host's marshal sends) reaches the Go, Python, Rust and Lisp SDKs, in every placement (isolated and in a shared runtime cell): the handler receives the payload the
// event encodes to (the Node SDK, Pi's own runtime, where the running Node's JSON.stringify has stack for it: to 4000 levels on Node 24 and every depth here on Node 26; where it has none, its handler's own JSON.stringify throws V8's RangeError, as in Pi: D268), and the same extension reads the ordinary update that follows (D268: no SDK ends its connection on deep input).
func TestDeepEventStreamDeltasReachEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.wire == nil {
				t.Fatal("the harness does not count the frames the host writes")
			}
			node := strings.Contains(tc.name, "node")
			var failures []string
			removeListener := h.runner.AddErrorListener(func(err *extension.ExtensionError) { failures = append(failures, err.Error) })
			defer removeListener()
			for _, name := range []string{"message_update", "tool_execution_update"} {
				var events []extension.ExtensionEvent
				var depths []int
				for _, c := range deepDeltaShapes {
					if name == "message_update" {
						for _, event := range deepMessageUpdateEvents(c.shape, c.depth) {
							events, depths = append(events, event), append(depths, c.depth)
						}
					} else {
						for _, event := range deepToolUpdateEvents(c.shape, c.depth) {
							events, depths = append(events, event), append(depths, c.depth)
						}
					}
				}
				prefix := "event_payload:" + name + ":"
				*h.notify = (*h.notify)[:0]
				failures = failures[:0]
				h.wire.frames.keepEventFrames(name)
				runConformanceCommandArgs(t, h, "event_payload_probe", name)
				for index, event := range events {
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					_, err := h.runner.Emit(ctx, event)
					cancel()
					if err != nil {
						t.Fatalf("%s: emit %d: %v", name, index, err)
					}
				}
				// probeFails and fixtureFails are where a handler of the Node SDK fails on the event: the probe's JSON.stringify of the payload, and for a tool update that holds the arguments the fixture's
				// own handler's JSON.stringify of them. Each fails where the running Node's JSON.stringify does on what the runtime handed the handler, which the probe replays from the frames the host
				// wrote (nodeEventStreamOutcomes, D268).
				probeFails, fixtureFails := make([]bool, len(events)), make([]bool, len(events))
				if node {
					frames := h.wire.frames.keptEventFrames(tc.extName, name)
					probeFails, fixtureFails = nodeEventStreamOutcomes(t, frames, len(events))
					// The replay runs the runtime's own event-stream code, so it cannot be its own oracle: the outcomes must be the ones the rule below gives, which uses none of it.
					wantProbe, wantFixture := nodeExpectedFailures(t, events, depths, frames)
					for index := range events {
						if probeFails[index] != wantProbe[index] || fixtureFails[index] != wantFixture[index] {
							t.Fatalf("%s: event %d (nesting %d): the runtime's copy makes the handlers fail (probe %v, fixture %v), the rule says (probe %v, fixture %v)", name, index, depths[index], probeFails[index], fixtureFails[index], wantProbe[index], wantFixture[index])
						}
					}
				}
				wantReports, wantFailures := 0, 0
				for index := range events {
					if probeFails[index] {
						wantFailures++
					} else {
						wantReports++
					}
					if fixtureFails[index] {
						wantFailures++
					}
				}
				// The wait ends when every handler has answered, or when one answered more than the row expects.
				var reported []string
				if !awaitCondition(testbudget.Context(t), func() bool {
					reported = reported[:0]
					for _, n := range *h.notify {
						if rest, ok := strings.CutPrefix(n, prefix); ok {
							reported = append(reported, strings.TrimSuffix(rest, ":info"))
						}
					}
					return (len(reported) >= wantReports && len(failures) >= wantFailures) || len(reported) > wantReports || len(failures) > wantFailures
				}) {
					t.Fatalf("not satisfied within the test's wait budget (%s): the handler did not report every %s: %d of %d reported, %d of %d handlers failed: %.600v", testbudget.Env, name, len(reported), wantReports, len(failures), wantFailures, failures)
				}
				if len(reported) != wantReports || len(failures) != wantFailures {
					t.Fatalf("%s: %d payloads reported and %d handlers failed, want %d and %d: %.600v", name, len(reported), len(failures), wantReports, wantFailures, failures)
				}
				for _, failure := range failures {
					if !strings.Contains(failure, "Maximum call stack size exceeded") {
						t.Fatalf("%s: a handler failed with %q, want V8's RangeError", name, failure)
					}
				}
				// The row means nothing if the host sent keyframes only: it must have sent deltas whose operations nest deep.
				if deep := h.wire.frames.deepDeltaFrames()[name]; deep == 0 {
					t.Fatalf("%s: the host sent no delta whose operations nest past 128 levels (%d event frames)", name, h.wire.frames.eventFrames()[name])
				}
				next := 0
				for index, event := range events {
					if probeFails[index] {
						continue
					}
					whole, err := json.Marshal(event)
					if err != nil {
						t.Fatal(err)
					}
					var got, want any
					if err := json.Unmarshal([]byte(reported[next]), &got); err != nil {
						t.Fatalf("%s: payload %d is not JSON: %v", name, index, err)
					}
					if err := json.Unmarshal(whole, &want); err != nil {
						t.Fatal(err)
					}
					next++
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("%s: payload %d = %.200s…, want %.200s…", name, index, reported[next-1], whole)
					}
				}
			}
		})
	}
}
