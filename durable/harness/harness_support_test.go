// Ports packages/durable/test/harness-support.ts.

package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

var testContext = context.Background()

// supportTool is harness-support.ts tool(): an object-parameter tool whose execution returns no content.
func supportTool(name string, description ...string) *durable.ToolRegistration {
	tool := registryTool(name)
	if len(description) > 0 {
		tool.Description = description[0]
	}
	return tool
}

func user(content string) ai.UserMessage {
	return ai.UserMessage{Content: ai.UserText(content), Timestamp: 1}
}

type assistantOptions struct {
	calls      []string
	stopReason ai.StopReason
}

func assistant(content string, options ...assistantOptions) ai.AssistantMessage {
	var option assistantOptions
	if len(options) > 0 {
		option = options[0]
	}
	blocks := []ai.AssistantContentBlock{ai.TextContent{Text: content}}
	for _, id := range option.calls {
		blocks = append(blocks, ai.ToolCall{ID: id, Name: "tool-" + id, Arguments: ai.JsonObject{}})
	}
	stopReason := option.stopReason
	if stopReason == "" {
		stopReason = "stop"
		if len(option.calls) > 0 {
			stopReason = "toolUse"
		}
	}
	return ai.AssistantMessage{
		Content:    blocks,
		API:        "faux",
		Provider:   "faux",
		Model:      "faux",
		StopReason: stopReason,
		Timestamp:  2,
	}
}

func toolResult(id string, content ...string) ai.ToolResultMessage {
	text := "result " + id
	if len(content) > 0 {
		text = content[0]
	}
	return ai.ToolResultMessage{
		ToolCallID: id,
		ToolName:   "tool-" + id,
		Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
		Timestamp:  3,
	}
}

func system(sections ...ai.PromptSection) ai.SystemMessage {
	return ai.SystemMessage{Content: ai.SystemText(""), Sections: sections, Timestamp: 4}
}

// describeMessage renders a message compactly for assertions.
func describeMessage(message ai.Message) string {
	switch typed := message.(type) {
	case ai.UserMessage:
		return "user:" + string(typed.Content.(ai.UserText))
	case ai.AssistantMessage:
		for _, block := range typed.Content {
			if text, ok := block.(ai.TextContent); ok {
				return "assistant:" + text.Text
			}
		}
		return "assistant:"
	case ai.ToolResultMessage:
		if typed.IsError {
			return "result:" + typed.ToolCallID + ":error"
		}
		for _, block := range typed.Content {
			if text, ok := block.(ai.TextContent); ok {
				return "result:" + typed.ToolCallID + ":" + text.Text
			}
		}
		return "result:" + typed.ToolCallID + ":"
	case ai.SystemMessage:
		names := []string{}
		for _, section := range typed.Sections {
			names = append(names, section.Name)
		}
		return "system:" + strings.Join(names, ",")
	}
	panic(fmt.Sprintf("unknown message %T", message))
}

func describeMessages(messages []ai.Message) []string {
	out := []string{}
	for _, message := range messages {
		out = append(out, describeMessage(message))
	}
	return out
}

// installed uninstalls what one of the helpers below installed.
type installed struct{ dispose func() }

func installOne(t *testing.T, registry Registry, extension *durable.Extension) installed {
	t.Helper()
	mustInstall(t, registry, extension)
	return installed{dispose: func() { registry.Uninstall(extension) }}
}

// addTool installs a one-tool extension named after the tool.
func addTool(t *testing.T, registry Registry, tool *durable.ToolRegistration, name ...string) installed {
	extension := "tool:" + tool.Name
	if len(name) > 0 {
		extension = name[0]
	}
	return installOne(t, registry, new(durable.Extension{Name: extension, Tools: []*durable.ToolRegistration{tool}}))
}

// addTask installs a one-task extension named after the task.
func addTask(t *testing.T, registry Registry, task durable.AnyTask, name ...string) installed {
	extension := "task:" + task.AnyDefinition().Name
	if len(name) > 0 {
		extension = name[0]
	}
	return installOne(t, registry, new(durable.Extension{Name: extension, Tasks: []durable.AnyTask{task}}))
}

var hookExtensions = 0

// addHooks installs an extension with one hook registration for task; handlers is untyped so a test can register a handler set the task does not use.
func addHooks(t *testing.T, registry Registry, task durable.AnyTask, handlers any) installed {
	hookExtensions++
	return installOne(t, registry, new(durable.Extension{Name: fmt.Sprintf("hooks:%d", hookExtensions), Hooks: []durable.HookRegistration{{Task: task.AnyDefinition().Name, Handlers: handlers}}}))
}

// addSection installs a one-section extension named after the section.
func addSection(t *testing.T, registry Registry, key string, render func(context.Context, durable.PromptInput) (*string, error), options ...SectionOptions) installed {
	return installOne(t, registry, new(durable.Extension{Name: "section:" + key, Sections: []*durable.PromptSection{Section(key, render, options...)}}))
}
