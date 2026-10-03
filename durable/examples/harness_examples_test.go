// Ports packages/durable/test/examples/06-harness.ts, 07-configuration.ts, 08-harness-conversations.ts and
// 10-registry-reload.ts.

package examples_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// exampleTool is the examples' tool: a path parameter and a text result.
func exampleTool(name, description string) *durable.ToolRegistration {
	return &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{
			Name:        name,
			Description: description,
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string"}},
				"required":   []any{"path"},
			},
		},
		Execute: func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			path, _ := args.(map[string]any)["path"].(string)
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: name + " " + path}}}, nil
		},
	}
}

func openHarness(t *testing.T, registry harness.Registry, settings func() *harness.HarnessSettings) harness.Harness {
	t.Helper()
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	return opened
}

func installed(t *testing.T, registry harness.Registry, extension *durable.Extension) {
	t.Helper()
	if err := registry.Install(extension); err != nil {
		t.Fatal(err)
	}
}

func toolNames(t *testing.T, conversation harness.Conversation) []string {
	t.Helper()
	agent, err := conversation.Agent(background)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tool := range agent.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func toolDescriptions(t *testing.T, conversation harness.Conversation) []string {
	t.Helper()
	agent, err := conversation.Agent(background)
	if err != nil {
		t.Fatal(err)
	}
	descriptions := []string{}
	for _, tool := range agent.Tools {
		descriptions = append(descriptions, tool.Name+": "+tool.Description)
	}
	return descriptions
}

func expectEqual(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// 06-harness.ts: a Harness over a Session resolves each conversation's agent from a registry of extensions.
func TestExample06Harness(t *testing.T) {
	read := exampleTool("read", "Read a file")
	registry := harness.CreateRegistry()
	installed(t, registry, new(durable.Extension{Name: "files", Tools: []*durable.ToolRegistration{read}}))
	opened := openHarness(t, registry, nil)

	level := ai.ModelThinkingLevel("low")
	root, err := opened.Root(background, &harness.RootOptions{
		Agent: &harness.AgentChange{ThinkingLevel: harness.SetTo(level)},
		Init: func(tx durable.Tx, rootId durable.ConversationId) error {
			return setNotes(tx, rootId, "root notes")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshotNotes(t, opened, root.Id()); got != "root notes" {
		t.Fatalf("root notes: %q", got)
	}
	stored, err := durable.Snapshot[harness.AgentState](background, opened, harness.AgentDoc, root.Id())
	if err != nil || stored == nil || stored.ThinkingLevel != level {
		t.Fatalf("stored agent: %+v %v", stored, err)
	}
	agent, err := root.Agent(background)
	if err != nil {
		t.Fatal(err)
	}
	var extensions []string
	for _, extension := range agent.Extensions {
		extensions = append(extensions, extension.Name)
	}
	expectEqual(t, "resolved thinking level", agent.ThinkingLevel, level)
	expectEqual(t, "resolved extensions", extensions, []string{"files"})
	expectEqual(t, "resolved tools", toolNames(t, root), []string{"read"})
	closeSession(t, opened)
}

// 07-configuration.ts: a conversation stores its agent configuration; the registry and settings resolve live.
func TestExample07Configuration(t *testing.T) {
	read := exampleTool("read", "Read a file")
	write := exampleTool("write", "Write a file")
	grep := exampleTool("grep", "Search files")
	files := new(durable.Extension{Name: "files", Tools: []*durable.ToolRegistration{read, write}})
	search := new(durable.Extension{Name: "search", Tools: []*durable.ToolRegistration{grep}})
	snippets := map[string]string{"read": "Use read for files.", "write": "Use write for files.", "grep": "Use grep for files."}
	snippetSection := new(durable.Extension{
		Name: "snippets",
		Sections: []*durable.PromptSection{{Key: "tool_snippets", Render: func(_ context.Context, input durable.PromptInput) (*string, error) {
			text := ""
			for i, tool := range input.Agent.Tools {
				if i > 0 {
					text += "\n"
				}
				text += snippets[tool.Name]
			}
			return &text, nil
		}}},
	})
	registry := harness.CreateRegistry()
	installed(t, registry, files)
	installed(t, registry, search)
	installed(t, registry, snippetSection)

	timeoutMs := 60_000
	retries := 5
	opened := openHarness(t, registry, func() *harness.HarnessSettings {
		return &harness.HarnessSettings{
			Stream:        &durable.ConversationStreamOptions{TimeoutMs: &timeoutMs},
			Retry:         &harness.RetryPolicyPatch{MaxRetries: &retries},
			ToolExecution: durable.ToolExecutionSequential,
		}
	})
	root, err := opened.Root(background, nil)
	if err != nil {
		t.Fatal(err)
	}
	expectEqual(t, "default tools", toolNames(t, root), []string{"read", "write", "grep"})

	model := durable.ModelRef{Provider: "anthropic", ModelId: "claude-sonnet-4-5"}
	if err := root.Configure(background, harness.AgentChange{
		Model:         harness.SetTo(model),
		ThinkingLevel: harness.SetTo(ai.ModelThinkingLevel("high")),
		Tools:         harness.SetTo(harness.ToolChange{Exact: true, List: []*durable.ToolRegistration{write, read}}),
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := durable.Snapshot[harness.AgentState](background, opened, harness.AgentDoc, root.Id())
	if err != nil || stored == nil || stored.Model == nil || *stored.Model != model || stored.ThinkingLevel != "high" {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	agent, err := root.Agent(background)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Model == nil || *agent.Model != model || agent.ThinkingLevel != "high" {
		t.Fatalf("agent: model %v thinking %v", agent.Model, agent.ThinkingLevel)
	}
	expectEqual(t, "configured tools", toolNames(t, root), []string{"write", "read"})

	if err := root.Configure(background, harness.AgentChange{
		Extensions: harness.SetTo(harness.ExtensionChange{Remove: []*durable.Extension{search}}),
		Tools:      harness.Cleared[harness.ToolChange](),
	}); err != nil {
		t.Fatal(err)
	}
	expectEqual(t, "without search", toolNames(t, root), []string{"read", "write"})

	if err := root.Configure(background, harness.AgentChange{
		Extensions: harness.SetTo(harness.ExtensionChange{Exact: true, List: []*durable.Extension{files, search}}),
	}); err != nil {
		t.Fatal(err)
	}
	registry.Uninstall(files)
	expectEqual(t, "files uninstalled", toolNames(t, root), []string{"grep"})
	installed(t, registry, files)
	expectEqual(t, "files reinstalled", toolNames(t, root), []string{"read", "write", "grep"})

	// Settings resolve at every use: the next request sees the new timeout.
	timeoutMs = 120_000
	resolved, err := root.Agent(background)
	if err != nil || resolved.Model == nil {
		t.Fatalf("agent after the settings change: %v", err)
	}
	closeSession(t, opened)
}

type messageData struct {
	From string `json:"from"`
}

// 08-harness-conversations.ts: typed entries, helper conversations, forks and lookup.
func TestExample08HarnessConversations(t *testing.T) {
	opened := openHarness(t, harness.CreateRegistry(), nil)
	root, err := opened.Root(background, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Configure(background, harness.AgentChange{ThinkingLevel: harness.SetTo(ai.ModelThinkingLevel("high"))}); err != nil {
		t.Fatal(err)
	}

	message := durable.DefineEntry[messageData]("message")
	hello := commit(t, root, func(tx durable.Tx) (*durable.TypedEntry[messageData], error) {
		return durable.TxAppendEntry(tx, message, root.Id(), durable.TypedEntryDraft[messageData]{
			Data:  messageData{From: "example"},
			Model: []ai.Message{ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}},
		})
	})
	if !message.Is(&hello.EntryRecord) || hello.TypedData.From != "example" {
		t.Fatalf("typed entry: %+v", hello)
	}

	minimal := ai.ModelThinkingLevel("minimal")
	helper, err := opened.CreateConversation(background, harness.ConversationCreateOptions{Ownership: ownerless.Ownership, Agent: &harness.AgentChange{ThinkingLevel: harness.SetTo(minimal)}})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := root.Fork(background, hello.Id, harness.ConversationCreateOptions{Ownership: ownerless.Ownership})
	if err != nil {
		t.Fatal(err)
	}
	helperAgent := must(helper.Agent(background))
	forkAgent := must(retry.Agent(background))
	expectEqual(t, "helper thinking", helperAgent.ThinkingLevel, minimal)
	expectEqual(t, "fork thinking", forkAgent.ThinkingLevel, ai.ModelThinkingLevel("high"))
	found, err := opened.Conversation(background, retry.Id())
	if err != nil || found == nil || found.Id() != retry.Id() {
		t.Fatalf("lookup: %v %v", found, err)
	}
	closeSession(t, opened)
}

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

// 10-registry-reload.ts: reinstalling an extension, adding a wrapper and uninstalling it reach every conversation.
func TestExample10RegistryReload(t *testing.T) {
	read := exampleTool("read", "Read a file")
	registry := harness.CreateRegistry()
	installed(t, registry, new(durable.Extension{Name: "files", Tools: []*durable.ToolRegistration{read, exampleTool("grep", "Search files")}}))
	opened := openHarness(t, registry, nil)
	root, err := opened.Root(background, nil)
	if err != nil {
		t.Fatal(err)
	}

	installed(t, registry, new(durable.Extension{Name: "files", Tools: []*durable.ToolRegistration{read, exampleTool("grep", "Search files, faster")}}))
	expectEqual(t, "after reload", toolDescriptions(t, root), []string{"read: Read a file", "grep: Search files, faster"})

	audit := new(durable.Extension{
		Name: "audit",
		Wraps: []durable.Wrap{{Tool: "read", WrapTool: func(tool *durable.ToolRegistration) *durable.ToolRegistration {
			wrapped := *tool
			wrapped.Description = tool.Description + " (audited)"
			return &wrapped
		}}},
	})
	installed(t, registry, audit)
	expectEqual(t, "with audit", toolDescriptions(t, root), []string{"read: Read a file (audited)", "grep: Search files, faster"})

	registry.Uninstall(audit)
	expectEqual(t, "after uninstall", toolDescriptions(t, root), []string{"read: Read a file", "grep: Search files, faster"})
	closeSession(t, opened)
}
