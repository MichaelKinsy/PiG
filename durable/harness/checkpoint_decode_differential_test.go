package harness

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// referenceCheckpoint is what each built-in document's checkpoint predicate was before CheckpointWhenRaw: decode the
// whole prepared value as the document's type (failing the commit when that fails), then ask the typed predicate.
func referenceCheckpoint[T any](predicate func(T) bool) func(durable.JsonObject) (bool, error) {
	return func(value durable.JsonObject) (bool, error) {
		typed, err := durable.FromJsonValue[T](value)
		if err != nil {
			return false, err
		}
		return predicate(typed), nil
	}
}

func referenceLive(value LiveState) bool {
	if value.Generation != nil {
		return false
	}
	for _, slot := range value.Tools {
		if slot.Status == ToolSlotRunning {
			return false
		}
	}
	for _, slot := range value.NestedTools {
		if slot.Status == ToolSlotRunning {
			return false
		}
	}
	return true
}

type checkpointCase struct {
	name      string
	definable interface {
		AnyDefinition() *durable.AnyDocDefinition
	}
	base      any
	reference func(durable.JsonObject) (bool, error)
	// depth is how many members down a random operation reaches at most; 0 is 4.
	depth int
}

func checkpointCases() []checkpointCase {
	taskID := durable.TaskId(7)
	entryID := durable.EntryId(9)
	text, count := "out", 3
	slot := func(i int, status ToolSlotStatus) ToolSlot {
		return ToolSlot{CallId: fmt.Sprintf("c%d", i), Name: "echo", TaskId: &taskID, Status: status, Output: &text, DroppedBytes: &count, Entry: &entryID}
	}
	nested := func(i int, status ToolSlotStatus) NestedToolSlot {
		return NestedToolSlot{CallId: fmt.Sprintf("c1/%d", i), ParentCallId: "c1", ParentTaskId: 4, Name: "echo", TaskId: durable.TaskId(10 + i), Arguments: map[string]any{"text": "x"}, Status: status, Output: &text}
	}
	live := LiveState{
		Tools:       []ToolSlot{slot(0, ToolSlotDone), slot(1, ToolSlotRunning), slot(2, ToolSlotDone)},
		NestedTools: []NestedToolSlot{nested(0, ToolSlotDone), nested(1, ToolSlotRunning), nested(2, ToolSlotPending), nested(3, ToolSlotDone)},
	}
	idle := LiveState{Tools: []ToolSlot{slot(0, ToolSlotDone)}, NestedTools: []NestedToolSlot{nested(0, ToolSlotDone)}}
	_ = idle
	usage := UsageState{Models: map[string]ai.Usage{"p/m": {Input: 1, Output: 2}, "p/n": {Input: 3}}, Tools: map[string]ai.Usage{"echo": {Input: 5}}}
	partial := LiveState{Generation: &LiveGeneration{Attempt: 1, Message: &ai.AssistantMessage{
		Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: "streamed text", TextSignature: "sig"},
			ai.ThinkingContent{Thinking: "thought", ThinkingSignature: "tsig"},
			ai.ToolCall{ID: "c1", Name: "write", Arguments: ai.JsonObject{"path": "a.ts", "content": "body", "nested": map[string]any{"list": []any{"x", "y"}}}},
			ai.TextContent{Text: "second"},
		},
		API: "faux", Provider: "faux", Model: "faux-1", ResponseID: "r1", Usage: ai.Usage{Input: 1, Output: 2, TotalTokens: 3}, StopReason: ai.StopReasonToolUse, Timestamp: 5,
	}}, Tools: []ToolSlot{slot(0, ToolSlotDone)}}
	return []checkpointCase{
		{"pi.live", LiveDoc, live, referenceCheckpoint(referenceLive), 0},
		{"pi.live partial", LiveDoc, partial, referenceCheckpoint(referenceLive), 8},
		{"pi.live idle", LiveDoc, idle, referenceCheckpoint(referenceLive), 0},
		{"pi.inbox", InboxDoc, InboxState{Items: []InboxItem{{Id: 1, Mode: InboxFollowUp, Content: "hi"}, {Id: 2, Mode: InboxWrite}}}, referenceCheckpoint(func(value InboxState) bool { return len(value.Items) == 0 }), 0},
		{"pi.usage", UsageDoc, usage, referenceCheckpoint(func(UsageState) bool { return true }), 0},
		{"pi.agent", AgentDoc, AgentState{ThinkingLevel: "high"}, referenceCheckpoint(func(AgentState) bool { return true }), 0},
		{"pi.provider", ProviderDoc, ProviderState{SessionId: "s"}, referenceCheckpoint(func(ProviderState) bool { return true }), 0},
	}
}

// scalarPool holds values a mutation writes: members of the right kinds and the wrong ones.
func scalarPool(rng *rand.Rand) any {
	pool := []any{nil, true, false, float64(3), float64(-1.5), float64(1 << 40), "running", "done", "pending", "x", "", []any{}, delta.JsonObjectOf(),
		delta.JsonObjectOf("status", "running"), delta.JsonObjectOf("status", float64(4)), []any{"a", float64(1)}, []any{delta.JsonObjectOf("status", "done")},
		delta.JsonObjectOf("callId", "z", "name", "n", "status", "running"), delta.JsonObjectOf("sessionId", float64(5))}
	return pool[rng.IntN(len(pool))]
}

// randomPath walks value to a random node at most depth members down and returns the path to it and the node.
func randomPath(rng *rand.Rand, value any, depth int) ([]any, any) {
	var path []any
	node := value
	for range depth {
		switch container := node.(type) {
		case *delta.JsonObject:
			keys := container.Keys()
			if len(keys) == 0 {
				return path, node
			}
			key := keys[rng.IntN(len(keys))]
			path = append(path, key)
			node, _ = container.Get(key)
		case []any:
			if len(container) == 0 {
				return path, node
			}
			index := rng.IntN(len(container))
			if rng.IntN(2) == 0 {
				path = append(path, float64(index))
			} else {
				path = append(path, index)
			}
			node = container[index]
		default:
			return path, node
		}
		if rng.IntN(3) == 0 {
			break
		}
	}
	return path, node
}

func randomOp(rng *rand.Rand, current any, depthLimit int) delta.Op {
	depth := 1 + rng.IntN(depthLimit)
	path, node := randomPath(rng, current, depth)
	if len(path) == 0 {
		return nil
	}
	switch rng.IntN(7) {
	case 0, 1, 2:
		return delta.Op{"s", path, scalarPool(rng)}
	case 3:
		return delta.Op{"d", path}
	case 4:
		if list, ok := node.([]any); ok {
			index := rng.IntN(len(list) + 1)
			remove := 0
			if index < len(list) {
				remove = rng.IntN(2)
			}
			items := []any{scalarPool(rng)}
			if rng.IntN(2) == 0 && len(list) > 0 {
				copied, _ := delta.CloneJSON(list[rng.IntN(len(list))])
				items = append(items, copied)
			}
			return delta.Op{"p", path, float64(index), float64(remove), items}
		}
		return delta.Op{"s", path, scalarPool(rng)}
	case 5:
		if list, ok := node.([]any); ok && len(list) > 1 {
			permutation := make([]any, len(list))
			for i, p := range rng.Perm(len(list)) {
				permutation[i] = float64(p)
			}
			return delta.Op{"m", path, permutation}
		}
		return delta.Op{"s", path, scalarPool(rng)}
	default:
		if text, ok := node.(string); ok {
			_ = text
			return delta.Op{"a", path, "!"}
		}
		return delta.Op{"s", path, scalarPool(rng)}
	}
}

// The decode CheckpointWhenRaw skips must still fail the commit where the typed predicate's decode failed: for a value
// that decodes before a batch of operations, the erased predicate returns the same result or the same error as
// decoding the whole value after it.
func TestRawCheckpointPredicatesFailLikeTheTypedDecode(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, c := range checkpointCases() {
		base, err := durable.ToJsonObject(c.base)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.reference(base); err != nil {
			t.Fatalf("%s: base does not decode: %v", c.name, err)
		}
		definition := c.definable.AnyDefinition()
		var wellFormed, malformed, bases int
		for range 6000 {
			working, err := delta.CloneJSON(base)
			if err != nil {
				t.Fatal(err)
			}
			var ops []delta.Op
			applied := working
			for range 1 + rng.IntN(4) {
				op := randomOp(rng, applied, max(c.depth, 4))
				if op == nil {
					continue
				}
				next, err := delta.Apply(applied, []delta.Op{op})
				if err != nil {
					continue
				}
				applied = next
				ops = append(ops, op)
			}
			final, ok := applied.(durable.JsonObject)
			if !ok || len(ops) == 0 {
				continue
			}
			wantBase, wantErr := c.reference(final)
			gotBase, gotErr := definition.CheckpointWhen(final, ops, durable.CheckpointInfo{})
			if (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()) || (wantErr == nil && wantBase != gotBase) {
				t.Fatalf("%s: ops %v\nfinal %v\nreference (%v, %v), erased (%v, %v)", c.name, ops, final, wantBase, wantErr, gotBase, gotErr)
			}
			if wantErr != nil {
				malformed++
			} else {
				wellFormed++
				if wantBase {
					bases++
				}
			}
		}
		t.Logf("%s: %d well-formed (%d base), %d malformed", c.name, wellFormed, bases, malformed)
		if wellFormed < 100 || malformed < 100 {
			t.Fatalf("%s: generator reached %d well-formed and %d malformed values; want at least 100 of each", c.name, wellFormed, malformed)
		}
	}
}

// The operations a streamed partial makes, and the ones that break its decode, are checked against the whole value's decode: an append to
// text, thinking or an argument is not decoded, and every other operation inside the message decodes the touched blocks and members alone.
func TestRawCheckpointPredicateChecksTheOperationsInsideAPartialMessage(t *testing.T) {
	var partial checkpointCase
	for _, c := range checkpointCases() {
		if c.name == "pi.live partial" {
			partial = c
		}
	}
	base, err := durable.ToJsonObject(partial.base)
	if err != nil {
		t.Fatal(err)
	}
	message := []any{"generation", "message"}
	at := func(rest ...any) []any { return append(slices.Clone(message), rest...) }
	cases := []struct {
		name string
		ops  []delta.Op
	}{
		{"append to a text", []delta.Op{{"a", at("content", 0, "text"), " more"}}},
		{"append to a thinking", []delta.Op{{"a", at("content", float64(1), "thinking"), " more"}}},
		{"append to an argument", []delta.Op{{"a", at("content", 2, "arguments", "content"), " more"}}},
		{"append to a string in the arguments", []delta.Op{{"a", at("content", 2, "arguments", "path"), "yy"}}},
		{"set an argument in a list", []delta.Op{{"s", at("content", 2, "arguments", "nested", "list", 1), "yy"}}},
		{"append to the type of a block", []delta.Op{{"a", at("content", 0, "type"), "s"}}},
		{"append to the signature of a text", []delta.Op{{"a", at("content", 0, "textSignature"), "s"}}},
		{"append to the provider", []delta.Op{{"a", at("provider"), "s"}}},
		{"set a text to a number", []delta.Op{{"s", at("content", 0, "text"), float64(5)}}},
		{"set a text to a number after an append", []delta.Op{{"a", at("content", 0, "text"), "!"}, {"s", at("content", 0, "text"), float64(5)}}},
		{"set the arguments to a string", []delta.Op{{"s", at("content", 2, "arguments"), "text"}}},
		{"set the arguments to a number", []delta.Op{{"s", at("content", 2, "arguments"), float64(1)}}},
		{"set the id of a call to a number", []delta.Op{{"s", at("content", 2, "id"), float64(1)}}},
		{"set a block to a string", []delta.Op{{"s", at("content", 3), "text"}}},
		{"set a block of an unknown type", []delta.Op{{"s", at("content", 3), delta.JsonObjectOf("type", "image", "data", "d", "mimeType", "m")}}},
		{"set a block of another type", []delta.Op{{"s", at("content", 3), delta.JsonObjectOf("type", "text", "text", "ok")}}},
		{"set the type of a block", []delta.Op{{"s", at("content", 3, "type"), "bogus"}}},
		{"delete a member of a block", []delta.Op{{"d", at("content", 0, "text")}}},
		{"delete the type of a block", []delta.Op{{"d", at("content", 0, "type")}}},
		{"delete a block", []delta.Op{{"d", at("content", 1)}}},
		{"delete the content", []delta.Op{{"d", at("content")}}},
		{"set the content to a string", []delta.Op{{"s", at("content"), "x"}}},
		{"splice a block in", []delta.Op{{"p", at("content"), float64(1), float64(0), []any{delta.JsonObjectOf("type", "text", "text", "new")}}}},
		{"splice a bad block in", []delta.Op{{"p", at("content"), float64(1), float64(0), []any{float64(3)}}}},
		{"set a usage member to a string", []delta.Op{{"s", at("usage", "input"), "many"}}},
		{"set a usage member", []delta.Op{{"s", at("usage", "input"), float64(9)}}},
		{"set the stop reason to a number", []delta.Op{{"s", at("stopReason"), float64(1)}}},
		{"set the timestamp to a string", []delta.Op{{"s", at("timestamp"), "now"}}},
		{"append with a block set before", []delta.Op{{"s", at("content", 3), delta.JsonObjectOf("type", "text", "text", "n")}, {"a", at("content", 3, "text"), "!"}}},
		{"set the message to a string", []delta.Op{{"s", message, "text"}}},
		{"set the generation to a number", []delta.Op{{"s", []any{"generation"}, float64(1)}}},
		{"append and set a usage member to a string", []delta.Op{{"a", at("content", 0, "text"), "!"}, {"s", at("usage", "output"), "x"}}},
	}
	definition := partial.definable.AnyDefinition()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			working, err := delta.CloneJSON(base)
			if err != nil {
				t.Fatal(err)
			}
			final, err := delta.Apply(working, c.ops)
			if err != nil {
				t.Fatalf("the operations do not apply: %v", err)
			}
			object, ok := final.(durable.JsonObject)
			if !ok {
				t.Fatalf("applied to %T", final)
			}
			wantBase, wantErr := partial.reference(object)
			gotBase, gotErr := definition.CheckpointWhen(object, c.ops, durable.CheckpointInfo{})
			if (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()) || (wantErr == nil && wantBase != gotBase) {
				t.Fatalf("reference (%v, %v), erased (%v, %v)", wantBase, wantErr, gotBase, gotErr)
			}
		})
	}
}
