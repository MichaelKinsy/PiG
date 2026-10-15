package agent

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// declareToolChangesReference is declareToolChanges replaying the whole context, as it did before the replay read only the system messages.
func declareToolChangesReference(tools []AgentTool, context, pending []AgentMessage) []AgentMessage {
	index := -1
	for i, pendingMessage := range slices.Backward(pending) {
		if pendingMessage.System != nil {
			index = i
			break
		}
	}
	baseline := slices.Clone(pending)
	if index >= 0 {
		baseline[index] = withToolChanges(pending[index], ai.ToolStateChanges{})
	}
	changes := ai.GetToolStateChangesFrom(slices.Concat(context, baseline), toolSchemas(tools))
	unchanged := len(changes.ToolsAdded) == 0 && len(changes.ToolsRemoved) == 0
	if index >= 0 {
		old := pending[index].System
		if unchanged && len(old.ToolsAdded) == 0 && len(old.ToolsRemoved) == 0 {
			return pending
		}
		baseline[index] = withToolChanges(pending[index], changes)
		return baseline
	}
	if unchanged {
		return pending
	}
	update := withToolChanges(AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText(""), Timestamp: time.Now().UnixMilli()}}, changes)
	index = slices.IndexFunc(pending, func(m AgentMessage) bool { return m.System == nil })
	if index < 0 {
		index = len(pending)
	}
	return slices.Concat(pending[:index], []AgentMessage{update}, pending[index:])
}

func withoutTimestamps(messages []AgentMessage) []AgentMessage {
	out := slices.Clone(messages)
	for i := range out {
		if out[i].System != nil {
			system := *out[i].System
			system.Timestamp = 0
			out[i].System = &system
		}
	}
	return out
}

// The tool-change replay reads only the system messages of the context, so reading them out first gives the answer a replay of the whole context gives: with system messages that add and remove tools at any position, a user message whose role says "system", a custom message with that role, an empty and a nil context, and every pending shape.
func TestDeclareToolChangesReadsOnlySystemMessagesLikeAWholeReplay(t *testing.T) {
	tools, _ := steadyToolFixtureWithProperties(3, 0, 1)
	schemas := toolSchemas(tools)
	declaration := func(name string) ai.ToolSchema {
		for _, schema := range schemas {
			if schema.Name == name {
				return schema
			}
		}
		return ai.ToolSchema{Name: name, Parameters: map[string]any{"type": "object"}}
	}
	system := func(added []string, removed ...string) AgentMessage {
		message := ai.SystemMessage{Content: ai.SystemText("s")}
		for _, name := range added {
			message.ToolsAdded = append(message.ToolsAdded, declaration(name))
		}
		for _, name := range removed {
			message.ToolsRemoved = append(message.ToolsRemoved, ai.ToolReference{Name: name})
		}
		return AgentMessage{System: &message}
	}
	user := AgentMessage{User: &UserMessage{Role: "user", Content: ai.UserText("u")}}
	impostor := AgentMessage{User: &UserMessage{Role: "system", Content: ai.UserText("not a system message")}}
	custom := AgentMessage{Custom: map[string]any{"role": "system", "content": "custom"}}
	contexts := map[string][]AgentMessage{
		"nil":               nil,
		"no system":         {user, impostor, custom, user},
		"all declared":      {user, system([]string{"tool_0", "tool_1", "tool_2"}), impostor, user},
		"partly declared":   {system([]string{"tool_0"}), user, custom, system([]string{"tool_1"}), user},
		"stale tool":        {system([]string{"tool_0", "ghost"}), user, impostor, system(nil, "tool_0"), user},
		"only systems":      {system([]string{"tool_0", "tool_1", "tool_2"}), system(nil, "tool_1")},
		"declared late":     {user, user, user, system([]string{"tool_0", "tool_1", "tool_2"})},
		"removed and added": {system([]string{"tool_0"}), system(nil, "tool_0"), system([]string{"tool_0", "tool_1", "tool_2"})},
	}
	pendings := map[string][]AgentMessage{
		"nil":                 nil,
		"empty":               {},
		"user":                {user},
		"system":              {system([]string{"tool_2"})},
		"system after user":   {user, system(nil)},
		"user after system":   {system(nil, "tool_0"), user},
		"impostor and custom": {impostor, custom},
	}
	for contextName, context := range contexts {
		for pendingName, pending := range pendings {
			t.Run(fmt.Sprintf("%s/%s", contextName, pendingName), func(t *testing.T) {
				got := withoutTimestamps(declareToolChanges(tools, slices.Clone(context), slices.Clone(pending)))
				want := withoutTimestamps(declareToolChangesReference(tools, slices.Clone(context), slices.Clone(pending)))
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %#v\nwant %#v", got, want)
				}
			})
		}
	}
}
