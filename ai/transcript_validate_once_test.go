package ai

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync/atomic"
	"testing"
)

// marshalCounted is a tool-result details value that is not plain JSON, so normalizing it takes the marshal-and-decode
// path, and counts each MarshalJSON call. packages/agent never copies or revalidates a tool result between turns, so a
// turn must pay for its details once.
type marshalCounted struct {
	Content string `json:"content"`
	calls   *atomic.Int64
}

func (m *marshalCounted) MarshalJSON() ([]byte, error) {
	m.calls.Add(1)
	type plain struct {
		Content string `json:"content"`
		Lines   int    `json:"lines"`
	}
	return json.Marshal(plain{m.Content, 2000})
}

func countedTranscript(calls *atomic.Int64, results int) []Message {
	messages := []Message{UserMessage{Content: UserText("q")}}
	for i := range results {
		id := string(rune('a' + i))
		messages = append(messages,
			AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: id, Name: "read", Arguments: JsonObject{"path": "f"}}}, StopReason: StopReasonToolUse},
			ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}, Details: &marshalCounted{Content: "x", calls: calls}})
	}
	return messages
}

// TestATranscriptIsValidatedOnceAcrossTheStreamPath mirrors one agent turn: the loop normalizes the history, the session
// re-wraps the transcript for the model runtime, and the provider checks its request. Normalization leaves each details
// value as decoded JSON, which is not "plain", so a later validation or copy of it marshals and decodes the value again.
// The later steps hold a transcript that is already validated and immutable, so they cost the same with or without
// details: the independent denominator is the identical transcript without them.
func TestATranscriptIsValidatedOnceAcrossTheStreamPath(t *testing.T) {
	var calls atomic.Int64
	steps := func(messages []Message) (provider, rewrap float64) {
		transcript := NormalizeContext(Context{Messages: messages})
		if transcript.err != nil {
			t.Fatal(transcript.err)
		}
		provider = testing.AllocsPerRun(20, func() {
			if err := validateProviderRequest(context.Background(), transcript); err != nil {
				t.Fatal(err)
			}
		})
		rewrap = testing.AllocsPerRun(20, func() {
			if again := NormalizeContext(transcript.FrozenContext()); again.err != nil || again.Len() != transcript.Len() {
				t.Fatalf("re-wrapped transcript: len %d err %v", again.Len(), again.err)
			}
		})
		return provider, rewrap
	}
	withProvider, withRewrap := steps(countedTranscript(&calls, 3))
	bare := countedTranscript(&calls, 3)
	for i, m := range bare {
		if r, ok := m.(ToolResultMessage); ok {
			r.Details = nil
			bare[i] = r
		}
	}
	bareProvider, bareRewrap := steps(bare)
	if withProvider > bareProvider {
		t.Errorf("the provider's request check costs %.0f allocations with details and %.0f without: it validates the history again", withProvider, bareProvider)
	}
	if withRewrap > bareRewrap {
		t.Errorf("re-wrapping a transcript costs %.0f allocations with details and %.0f without: it copies and validates the history again", withRewrap, bareRewrap)
	}
}

// TestValidatingAValueDoesNotDecodeACopyOfIt: validation answers whether the value marshals to valid JSON; it needs no
// decoded tree. normalizeJSONValue, which builds the tree, is the independent denominator.
func TestValidatingAValueDoesNotDecodeACopyOfIt(t *testing.T) {
	var calls atomic.Int64
	value := &marshalCounted{Content: strings.Repeat("x", 4096), calls: &calls}
	validate := testing.AllocsPerRun(20, func() {
		if err := validateJsonValue(value); err != nil {
			t.Fatal(err)
		}
	})
	normalize := testing.AllocsPerRun(20, func() {
		if _, err := normalizeJSONValue(value); err != nil {
			t.Fatal(err)
		}
	})
	if validate >= normalize {
		t.Fatalf("validating costs %.0f allocations, decoding a copy %.0f: validation decodes the value", validate, normalize)
	}
}

// A transcript that grows by one message per turn validates each details value once with a memo: the work of a turn is
// the work of its new messages, not of the history. The marshal count is the independent denominator: one call per value
// actually validated.
func TestAFrozenTranscriptWithAMemoValidatesOnlyItsNewMessages(t *testing.T) {
	var calls atomic.Int64
	memo := NewValidationMemo()
	messages := countedTranscript(&calls, 1)
	for turn := range 8 {
		before := calls.Load()
		transcript := NormalizeContext(Context{Messages: messages, Frozen: true, Validated: memo})
		if transcript.err != nil || transcript.Len() != len(messages) {
			t.Fatalf("turn %d: len %d err %v", turn, transcript.Len(), transcript.err)
		}
		if got := calls.Load() - before; got != 1 {
			t.Fatalf("turn %d validated %d details values, want only the 1 new one (history of %d)", turn, got, turn)
		}
		next := countedTranscript(&calls, 1)
		messages = append(messages, next[1], next[2])
	}
	// Each exchange holds two references: the call's arguments and the result's details.
	if len(memo.seen) != 2*8 {
		t.Fatalf("memo holds %d values for 8 exchanges, want %d", len(memo.seen), 2*8)
	}
}

// The memo forgets values the normalized transcript no longer references and never serves an unfrozen context.
func TestAValidationMemoDropsReplacedHistoryAndIsIgnoredWithoutFrozen(t *testing.T) {
	var calls atomic.Int64
	memo := NewValidationMemo()
	first := countedTranscript(&calls, 3)
	NormalizeContext(Context{Messages: first, Frozen: true, Validated: memo})
	second := countedTranscript(&calls, 1)
	NormalizeContext(Context{Messages: second, Frozen: true, Validated: memo})
	if len(memo.seen) != 2 {
		t.Fatalf("memo holds %d values after the history was replaced by 1 exchange, want 2", len(memo.seen))
	}
	before := calls.Load()
	NormalizeContext(Context{Messages: second})
	plain := calls.Load() - before
	before = calls.Load()
	NormalizeContext(Context{Messages: second, Validated: memo})
	if got := calls.Load() - before; got != plain {
		t.Fatalf("an unfrozen context with a memo marshaled %d times, without one %d: the memo served a context that copies its messages", got, plain)
	}
}

// A value that fails validation is never remembered as valid.
func TestAValidationMemoDoesNotRememberAFailure(t *testing.T) {
	memo := NewValidationMemo()
	bad := &struct{ F func() }{}
	messages := []Message{UserMessage{Content: UserText("q")}, ToolResultMessage{ToolCallID: "a", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}, Details: bad}}
	for range 2 {
		if got := NormalizeContext(Context{Messages: messages, Frozen: true, Validated: memo}); got.err == nil {
			t.Fatal("an unmarshalable details value was accepted")
		}
	}
	if len(memo.seen) != 0 {
		t.Fatalf("memo holds %d values after failures", len(memo.seen))
	}
}

// TestARewrappedTranscriptStillValidatesWhatTheContextAdds: a re-wrapped transcript skips validating its own messages,
// but a system prompt or tools on the Context add a system message nothing has validated. Tools whose parameters do not
// marshal fail normalization with the same error as on a fresh context; skipping their validation would instead panic
// when the frozen path clones that system message.
func TestARewrappedTranscriptStillValidatesWhatTheContextAdds(t *testing.T) {
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("q")}}})
	if transcript.err != nil {
		t.Fatal(transcript.err)
	}
	tools := []ToolSchema{{Name: "t", Parameters: map[string]any{"type": "object", "minimum": math.NaN()}}}
	want := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("q")}}, Tools: tools, Frozen: true}).err
	if want == nil {
		t.Fatal("a fresh context with unmarshalable tool parameters normalized without an error")
	}
	for name, add := range map[string]func(*Context){
		"tools":         func(c *Context) { c.Tools = tools },
		"system prompt": func(c *Context) { c.SystemPrompt = "s"; c.Tools = tools },
	} {
		rewrapped := transcript.FrozenContext()
		add(&rewrapped)
		var got TranscriptContext
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: re-wrapping panicked instead of failing validation: %v", name, r)
				}
			}()
			got = NormalizeContext(rewrapped)
		}()
		if got.err == nil || got.err.Error() != want.Error() {
			t.Errorf("%s: re-wrapped error %v, want %v", name, got.err, want)
		}
	}
}

// A slice is known by its first element and its length: a longer slice of the same array holds values the memo has not
// validated, so it is validated again and its unmarshalable element is rejected.
func TestAValidationMemoTellsSlicesOfOneArrayApart(t *testing.T) {
	backing := []any{1, 2, math.NaN()}
	memo := NewValidationMemo()
	transcript := func(details []any) []Message {
		return []Message{UserMessage{Content: UserText("q")}, ToolResultMessage{ToolCallID: "a", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}, Details: details}}
	}
	if got := NormalizeContext(Context{Messages: transcript(backing[:2]), Frozen: true, Validated: memo}); got.err != nil {
		t.Fatal(got.err)
	}
	if got := NormalizeContext(Context{Messages: transcript(backing[:3]), Frozen: true, Validated: memo}); got.err == nil {
		t.Fatal("the longer slice of a validated slice's array was accepted with an unmarshalable element")
	}
}

// The zero ValidationMemo is an empty memo, as the zero value of an exported Go type should be.
func TestTheZeroValidationMemoIsUsable(t *testing.T) {
	var calls atomic.Int64
	var memo ValidationMemo
	messages := countedTranscript(&calls, 1)
	for range 2 {
		if got := NormalizeContext(Context{Messages: messages, Frozen: true, Validated: &memo}); got.err != nil {
			t.Fatal(got.err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the zero memo validated the details %d times over two requests, want 1", got)
	}
}
