package extensionconformance

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/boundarywire"
)

// frameCheck holds the frames a Host wrote that are not json.Marshal of their own envelope.
type frameCheck struct {
	mu     sync.Mutex
	frames int
	events map[string]int
	// deltas and deepDeltas count the event requests that carry the operations of an event stream (no payload of their own), and those whose operations nest past
	// serde_json's 128 levels, by event.
	deltas     map[string]int
	deepDeltas map[string]int
	// signalCancels counts the signal-only cancel frames (CancelPayload.SignalOnly) the host wrote.
	signalCancels int
	// toolCalls counts the tool_call requests the host wrote.
	toolCalls int
	failures      []string
}

// checkOutboundFrames fails t when a frame the host writes to an extension, in any load mode, is not the bytes json.Marshal writes for the envelope it carries: every member in the encoder's order, every raw member compact and escaped as the encoder escapes it (<, >, &, U+2028, U+2029). Each realization of an extension then receives the bytes it received before the host shared or spliced encodings.
func checkOutboundFrames(t *testing.T, host *subprocess.Host) *frameCheck {
	t.Helper()
	check := &frameCheck{}
	host.SetOutboundObserver(check.observe)
	t.Cleanup(func() {
		check.mu.Lock()
		defer check.mu.Unlock()
		for _, failure := range check.failures {
			t.Error(failure)
		}
	})
	return check
}

func (c *frameCheck) observe(extName string, frame []byte) {
	var env subprocess.Envelope
	failure := ""
	if err := sdkjson.Unmarshal(frame, &env); err != nil {
		failure = fmt.Sprintf("frame to %s does not decode: %v: %s", extName, err, clip(frame))
	} else if want, err := sdkjson.Marshal(&env); err != nil {
		failure = fmt.Sprintf("frame to %s does not encode again: %v: %s", extName, err, clip(frame))
	} else if !bytes.Equal(frame, want) {
		failure = fmt.Sprintf("frame to %s is not json.Marshal of its envelope:\n got %s\nwant %s", extName, clip(frame), clip(want))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frames++
	if env.Type == subprocess.MsgCancel && env.Cancel != nil && env.Cancel.SignalOnly {
		c.signalCancels++
	}
	if env.Request != nil && env.Request.Method == subprocess.MethodToolCall {
		c.toolCalls++
	}
	if env.Request != nil && env.Request.Method == "event" {
		if c.events == nil {
			c.events = map[string]int{}
		}
		c.events[env.Request.Event]++
		if stream := env.Request.Stream; stream != nil && len(stream.Ops) > 0 && len(env.Request.Args) == 0 {
			if c.deltas == nil {
				c.deltas, c.deepDeltas = map[string]int{}, map[string]int{}
			}
			c.deltas[env.Request.Event]++
			if bytes.Contains(stream.Ops, bytes.Repeat([]byte("["), 150)) || bytes.Contains(stream.Ops, bytes.Repeat([]byte(`{"a":`), 150)) {
				c.deepDeltas[env.Request.Event]++
			}
		}
	}
	if failure != "" && len(c.failures) < 5 {
		c.failures = append(c.failures, failure)
	}
}

// signalCancelFrames returns how many signal-only cancel frames the host wrote, counted by the write loop before it writes each, so a frame the loop writes after the count follows that cancel on the wire.
func (c *frameCheck) signalCancelFrames() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.signalCancels
}

// toolCallRequests returns how many tool_call requests the host wrote: a nested tool call is a request of its own.
func (c *frameCheck) toolCallRequests() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.toolCalls
}

// eventFrames returns how many event requests of each kind the host wrote.
func (c *frameCheck) eventFrames() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.events)
}

// deepDeltaFrames returns how many delta frames of each event the host wrote whose operations nest past serde_json's 128 levels.
func (c *frameCheck) deepDeltaFrames() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.deepDeltas)
}

func clip(b []byte) string {
	const keep = 600
	if len(b) <= keep {
		return string(b)
	}
	return string(b[:keep]) + fmt.Sprintf("... (%d bytes)", len(b))
}

// outboundCorpus is the text the corpus events carry: the characters json.Marshal escapes (<, >, &, U+2028, U+2029), a lone surrogate in the WTF-8 form a session keeps, and an astral character.
const outboundCorpus = `a<b>&c\u2028d\u2029e\ud800f\ud83d\ude00`

// Every realization of an extension receives, for every event, the bytes the host's encoder writes for the frame's envelope (FD-9). The corpus covers a message_update keyframe and the delta after it, a tool_call whose input is the model's non-compact JSON with numbers JSON.stringify spells its own way (1e21, -0, 0.1), and a turn_end whose context preview carries the corpus text.
func TestOutboundFramesAreTheEncodersBytes(t *testing.T) {
	assistant := func(text string) extension.AgentMessage {
		var message extension.AgentMessage
		raw := `{"role":"assistant","content":[{"type":"text","text":"` + text + `"}],"api":"openai-responses","provider":"p","model":"m","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0.1,"output":0,"cacheRead":0,"cacheWrite":0,"total":0.1}},"stopReason":"stop","timestamp":1}`
		if err := sdkjson.Unmarshal([]byte(raw), &message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	user := func(text string) extension.AgentMessage {
		var message extension.AgentMessage
		if err := sdkjson.Unmarshal([]byte(`{"role":"user","content":"`+text+`","timestamp":2}`), &message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	wireInput := sdkjson.RawMessage("{ \"path\" : \"<a&b>\u2028\" ,\n \"n\": 1e21, \"z\": -0, \"f\": 0.1, \"s\": \"\\ud800\", \"nested\": { \"list\" : [ 1 , 2 ] } }")
	cases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			h := test.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			before := h.wire.frames.eventFrames()
			keyframe := extension.MessageUpdateEvent{Type: "message_update", Message: assistant(outboundCorpus), AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 0, Delta: "\u2028<"}}
			delta := extension.MessageUpdateEvent{Type: "message_update", Message: assistant(outboundCorpus + "<more>"), AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 0, Delta: "<more>"}}
			for _, event := range []extension.ExtensionEvent{keyframe, delta} {
				if _, err := h.runner.Emit(ctx, event); err != nil {
					t.Fatalf("emit message_update: %v", err)
				}
			}
			var input map[string]any
			if err := sdkjson.Unmarshal(wireInput, &input); err != nil {
				t.Fatal(err)
			}
			raw := slices.Clone(wireInput)
			call := extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "corpus-call", WireInput: &raw, ToolName: "corpus_tool", Input: input}
			if _, err := h.runner.EmitToolCall(ctx, call); err != nil {
				t.Fatalf("emit tool_call: %v", err)
			}
			// The turn_end preview as a session builds it: projected from the session log, with each entry's record (boundarywire), so the host takes its encoding path for session previews.
			var encodings boundarywire.Encodings
			for revision := range uint64(2) {
				boundaryCtx, wire := boundarywire.WithSessionCache(ctx, &encodings)
				preview := func([]extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
					messages := []extension.AgentMessage{user(outboundCorpus), assistant(outboundCorpus)}
					preview := extension.BoundaryContextPreview{
						ContextEntries:  []extension.ProjectedSessionEntry{{SourceEntry: map[string]any{"type": "message", "id": "corpus", "text": "<&>\u2028"}, Messages: messages}},
						ContextMessages: messages,
						LLMMessages:     []ai.Message{},
						PendingMessages: []extension.AgentMessage{},
					}
					wire.Built(preview, boundarywire.Key{Session: &encodings, Revision: revision}, true, []sdkjson.RawMessage{sdkjson.RawMessage(`{"type":"message","id":"corpus","text":"<&>\u2028 \ud800"}`)})
					wire.Shared()
					return preview, nil
				}
				turnEnd := extension.TurnEndEvent{Type: "turn_end", Message: assistant(outboundCorpus), ToolResults: []extension.ToolResultMessage{}, ToolResultEntryIds: []string{}, MessageEntryID: "corpus-entry", BoundaryState: &extension.BoundaryState{Entries: []extension.SessionBoundaryDraft{{Type: "custom", CustomType: "<draft>"}}, Outcome: extension.AgentActivityCompleted}}
				if _, err := h.runner.EmitBoundary(boundaryCtx, turnEnd, preview); err != nil {
					t.Fatalf("emit turn_end: %v", err)
				}
			}
			// Each event reached the extension, so its frame was checked.
			after := h.wire.frames.eventFrames()
			for event, want := range map[string]int{"message_update": 2, "tool_call": 1, "turn_end": 2} {
				if got := after[event] - before[event]; got < want {
					t.Errorf("the host wrote %d %s requests, want at least %d", got, event, want)
				}
			}
		})
	}
}
