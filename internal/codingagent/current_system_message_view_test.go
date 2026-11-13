package codingagent

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func viewTool(name string, properties int) ai.ToolSchema {
	members := map[string]any{}
	for i := range properties {
		members[fmt.Sprintf("field%d", i)] = map[string]any{"type": "string", "description": "an argument with a description of realistic length"}
	}
	return ai.ToolSchema{Name: name, Description: "tool " + name, Parameters: map[string]any{"type": "object", "properties": members, "required": []any{"field0"}}}
}

func viewSystem(content string, sections ai.OrderedSections, added []ai.ToolSchema, removed ...string) agent.AgentMessage {
	message := ai.SystemMessage{Content: ai.SystemText(content), Sections: sections, ToolsAdded: added, Timestamp: 7}
	for _, name := range removed {
		message.ToolsRemoved = append(message.ToolsRemoved, ai.ToolReference{Name: name})
	}
	return agent.AgentMessage{System: &message}
}

func viewUser(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(text)}}
}

// TestCurrentSystemMessageViewEqualsTheCopyingReplay: the view is the replay the copying path returns, value for value, for
// transcripts with no system message, tools added, redeclared and removed, and prompt sections patched.
func TestCurrentSystemMessageViewEqualsTheCopyingReplay(t *testing.T) {
	changed := viewTool("b", 2)
	changed.Description = "tool b, changed"
	stripped := func(messages []agent.AgentMessage) *ai.SystemMessage {
		var systems []ai.Message
		for _, message := range messages {
			if message.System != nil {
				systems = append(systems, *message.System)
			}
		}
		return ai.GetCurrentSystemMessage(systems)
	}
	value := "v"
	cases := map[string][]agent.AgentMessage{
		"empty":         nil,
		"conversation":  {viewUser("hello")},
		"one system":    {viewSystem("prompt", nil, []ai.ToolSchema{viewTool("a", 3), viewTool("b", 3)}), viewUser("hello")},
		"redeclared":    {viewSystem("prompt", nil, []ai.ToolSchema{viewTool("a", 3), viewTool("b", 3)}), viewUser("hello"), viewSystem("", nil, []ai.ToolSchema{changed}, "b")},
		"removed":       {viewSystem("prompt", nil, []ai.ToolSchema{viewTool("a", 3)}), viewSystem("", nil, nil, "a")},
		"sections":      {viewSystem("prompt", ai.OrderedSections{{Name: "x", Value: &value}}, []ai.ToolSchema{viewTool("a", 1)}), viewSystem("more", ai.OrderedSections{{Name: "x"}}, nil)},
		"tools only":    {viewSystem("", nil, []ai.ToolSchema{viewTool("a", 1)})},
		"no tool added": {viewSystem("prompt", nil, nil)},
	}
	for name, messages := range cases {
		if got, want := CurrentSystemMessageView(messages), stripped(messages); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: view = %#v, copying replay = %#v", name, got, want)
		}
	}
}

// TestCurrentSystemMessageViewDoesNotCopyToolSchemas: what the view allocates does not follow the size of the declared schemas.
func TestCurrentSystemMessageViewDoesNotCopyToolSchemas(t *testing.T) {
	allocs := func(properties int) float64 {
		tools := []ai.ToolSchema{viewTool("a", properties), viewTool("b", properties), viewTool("c", properties)}
		messages := []agent.AgentMessage{viewSystem("prompt", nil, tools), viewUser("hello")}
		return testing.AllocsPerRun(20, func() {
			if CurrentSystemMessageView(messages) == nil {
				t.Fatal("no system message")
			}
		})
	}
	if small, large := allocs(2), allocs(60); large != small {
		t.Errorf("allocations per view: %v with 2-property schemas, %v with 60-property ones: the schemas are copied", small, large)
	}
}
