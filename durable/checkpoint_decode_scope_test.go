package durable

import (
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

type scopedGeneration struct {
	Message *ai.AssistantMessage `json:"message,omitempty"`
}

type scopedLive struct {
	Generation *scopedGeneration `json:"generation,omitempty"`
}

// The check of the operations inside a partial message decodes the touched blocks and members, not the message: a commit that changes a
// member or a small block of a message that holds a large one costs what it changed.
func TestCheckDecodesOfAnOperationInsideAMessageDoesNotDecodeTheUntouchedBlocks(t *testing.T) {
	const large = 2 << 20
	live := scopedLive{Generation: &scopedGeneration{Message: &ai.AssistantMessage{
		Content: []ai.AssistantContentBlock{ai.TextContent{Text: "small"}, ai.TextContent{Text: strings.Repeat("large text ", large/11)}},
		API:     "faux", Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse,
	}}}
	base, err := ToJsonObject(live)
	if err != nil {
		t.Fatal(err)
	}
	message := []any{"generation", "message"}
	at := func(rest ...any) []any { return append(append([]any(nil), message...), rest...) }
	for _, c := range []struct {
		name string
		op   delta.Op
	}{
		{"a member", delta.Op{"s", at("usage", "input"), float64(9)}},
		{"the stop reason", delta.Op{"s", at("stopReason"), "stop"}},
		{"a small block", delta.Op{"s", at("content", 0, "text"), "other"}},
		{"a member of a small block", delta.Op{"s", at("content", 0, "textSignature"), "sig"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			working, err := delta.CloneJSON(base)
			if err != nil {
				t.Fatal(err)
			}
			final, err := delta.Apply(working, []delta.Op{c.op})
			if err != nil {
				t.Fatal(err)
			}
			object := final.(JsonObject)
			const runs = 10
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range runs {
				if err := checkDecodes[scopedLive](object, []Op{c.op}); err != nil {
					t.Fatal(err)
				}
			}
			runtime.ReadMemStats(&after)
			if perCheck := (after.TotalAlloc - before.TotalAlloc) / runs; perCheck > large/8 {
				t.Fatalf("a check allocated %d bytes, with a %d byte block untouched", perCheck, large)
			}
		})
	}
}
