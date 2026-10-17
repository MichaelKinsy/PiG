package coding

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func systemMessagesWithTools(depth int) []ai.SystemMessage {
	schema := map[string]any{"type": "string"}
	for i := range depth {
		schema = map[string]any{"type": "object", "properties": map[string]any{fmt.Sprintf("p%d", i): schema, "q": []any{"a", "b"}}}
	}
	tools := make([]ai.ToolSchema, 6)
	for i := range tools {
		tools[i] = ai.ToolSchema{Name: fmt.Sprintf("tool%d", i), Description: "d", Parameters: schema}
	}
	return []ai.SystemMessage{
		{Content: ai.SystemText("base"), Sections: ai.OrderedSections{{Name: "a", Value: new("1")}, {Name: "b", Value: new("2")}}, ToolsAdded: tools, Timestamp: 1},
		{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "a", Value: nil}, {Name: "c", Value: new("3")}}, ToolsRemoved: []ai.ToolReference{{Name: "tool0"}}, ToolsAdded: tools[:2], Timestamp: 2},
		{Content: ai.SystemText("more"), ToolsAdded: tools[2:3], Timestamp: 3},
	}
}

// promptSectionUpdate replays the transcript's system messages for their sections only. Without the tool changes the replay must still find the same current message, in the same sections, so the diff it computes against the desired sections is the one it computed with them.
func TestSystemMessageWithoutToolsReplaysTheSameSections(t *testing.T) {
	for name, systems := range map[string][]ai.SystemMessage{
		"replayed": systemMessagesWithTools(3),
		"first":    systemMessagesWithTools(3)[:1],
		"none":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			var full, stripped []ai.Message
			for _, message := range systems {
				full = append(full, message)
				stripped = append(stripped, systemMessageWithoutTools(message))
			}
			got, want := ai.GetCurrentSystemMessage(stripped), ai.GetCurrentSystemMessage(full)
			if (got == nil) != (want == nil) {
				t.Fatalf("with the tool changes removed the current message is %v, want %v", got, want)
			}
			if want == nil {
				return
			}
			if !reflect.DeepEqual(got.Sections, want.Sections) || !reflect.DeepEqual(got.Content, want.Content) || got.Timestamp != want.Timestamp {
				t.Fatalf("sections %#v content %#v, want %#v %#v", got.Sections, got.Content, want.Sections, want.Content)
			}
		})
	}
}

// The replay copies no tool schema: its allocations stay those of the sections however large the schemas are.
func TestSystemMessageWithoutToolsReplayDoesNotCopySchemas(t *testing.T) {
	allocs := func(depth int) float64 {
		systems := systemMessagesWithTools(depth)
		return testing.AllocsPerRun(20, func() {
			var replayed []ai.Message
			for _, message := range systems {
				replayed = append(replayed, systemMessageWithoutTools(message))
			}
			_ = ai.GetCurrentSystemMessage(replayed)
		})
	}
	if small, large := allocs(1), allocs(12); large > small {
		t.Fatalf("the replay allocates %.0f times over deep tool schemas and %.0f over shallow ones: it copies the schemas", large, small)
	}
}
